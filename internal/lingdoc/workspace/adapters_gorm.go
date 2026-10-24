package workspace

import (
	"context"
	"errors"
	"io"
	"time"

	"github.com/Tencent/WeKnora/internal/evidence"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"gorm.io/gorm"
)

// GORMSourcePorts contains the concrete source/evidence adapters used by the
// container when it assembles the workspace application services.
type GORMSourcePorts struct {
	Bindings BindingStore
	Gateway  evidence.AssetGateway
	Catalog  SourceCatalog
	Origins  evidence.OriginReader
}

// NewGORMSourcePorts constructs GORM-backed source adapters. Application
// service and HTTP handler composition stays in internal/container.
func NewGORMSourcePorts(db *gorm.DB, kbShares interfaces.KBShareService) GORMSourcePorts {
	bindings := evidence.NewBindings(db)
	knowledgeReader := dbKnowledgeReader{db: db}
	bindings.SetKnowledgeSignalReader(knowledgeReader)
	authorizer := evidence.NewFixedAuthorizer(bindings, kbReadChecker{shares: kbShares})
	return GORMSourcePorts{
		Bindings: bindings,
		Gateway:  evidence.NewAssetGateway(bindings, authorizer),
		Catalog:  dbSourceCatalog{db: db},
		Origins:  evidence.NewOriginReader(knowledgeReader),
	}
}

// OriginFileReader reads the source bytes of a material.
//
// It is the storage-side counterpart of the database-side facts: the database
// says which file a material came from, the storage layer says what is in
// it. The origin text (what a quoted text is proven against) needs both.
type OriginFileReader interface {
	OriginBytes(ctx context.Context, knowledgeID string) (data []byte, ok bool, err error)
}

// KnowledgeFileSource is the storage-side capability the origin reader needs:
// the bytes of a material's source file.
//
// It is deliberately narrow — one method — so the reader depends on the
// storage seam, not on the whole knowledge service. The production knowledge
// service satisfies it (it resolves the storage reference of the file);
// a test can satisfy it with a stub.
type KnowledgeFileSource interface {
	GetKnowledgeFile(ctx context.Context, id string) (io.ReadCloser, string, error)
}

// KnowledgeFileOriginReader reads a material's source bytes through the
// knowledge service, which resolves the storage reference of the file.
type KnowledgeFileOriginReader struct {
	Service KnowledgeFileSource
}

// OriginBytes reads the source file of a material.
//
// A read that fails is a "no origin" answer, not a failure of the request:
// the caller then degrades to the self-consistency tier, which is the same
// answer it gave before this reader existed. What it must not do is claim a
// verbatim match it could not prove.
func (r KnowledgeFileOriginReader) OriginBytes(ctx context.Context, knowledgeID string) ([]byte, bool, error) {
	if r.Service == nil || knowledgeID == "" {
		return nil, false, nil
	}
	file, _, err := r.Service.GetKnowledgeFile(ctx, knowledgeID)
	if err != nil {
		return nil, false, nil
	}
	if file == nil {
		return nil, false, nil
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, evidence.MaxOriginBytes+1))
	if err != nil {
		return nil, false, nil
	}
	if len(data) == 0 || len(data) > evidence.MaxOriginBytes {
		return nil, false, nil
	}
	return data, true, nil
}

type dbSourceCatalog struct{ db *gorm.DB }

func (r dbSourceCatalog) Knowledge(ctx context.Context, id string) (*SourceKnowledge, error) {
	var knowledge types.Knowledge
	err := r.db.WithContext(ctx).Where("id = ? AND deleted_at IS NULL", id).First(&knowledge).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &SourceKnowledge{
		ID: knowledge.ID, TenantID: knowledge.TenantID, KnowledgeBaseID: knowledge.KnowledgeBaseID,
		Title: knowledge.Title, ParseStatus: knowledge.ParseStatus, FileHash: knowledge.FileHash,
		FileSize: knowledge.FileSize, ProcessedAt: knowledge.ProcessedAt,
	}, nil
}

func (r dbSourceCatalog) Chunk(ctx context.Context, id string) (*SourceChunk, error) {
	var chunk types.Chunk
	err := r.db.WithContext(ctx).Where("id = ?", id).First(&chunk).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &SourceChunk{
		ID: chunk.ID, KnowledgeID: chunk.KnowledgeID, ChunkIndex: chunk.ChunkIndex,
		StartAt: chunk.StartAt, EndAt: chunk.EndAt, Content: chunk.Content,
		ContentRevision: chunk.ContentRevision,
	}, nil
}

type dbKnowledgeReader struct {
	db *gorm.DB
	// files reads the material's source bytes. It is optional: without it the
	// origin reader falls back to the local file path, which is what a reader
	// that only knows local paths can offer.
	files OriginFileReader
}

func (r dbKnowledgeReader) KnowledgeForOrigin(ctx context.Context, knowledgeID string) (*types.Knowledge, error) {
	var knowledge types.Knowledge
	err := r.db.WithContext(ctx).Where("id = ?", knowledgeID).First(&knowledge).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &knowledge, nil
}

func (r dbKnowledgeReader) CurrentKnowledgeSignal(ctx context.Context, knowledgeID string) (evidence.KnowledgeSignal, bool, error) {
	var knowledge types.Knowledge
	err := r.db.WithContext(ctx).Where("id = ? AND deleted_at IS NULL", knowledgeID).First(&knowledge).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return evidence.KnowledgeSignal{}, false, nil
	}
	if err != nil {
		return evidence.KnowledgeSignal{}, false, err
	}
	return evidence.KnowledgeSignal{
		KnowledgeID: knowledge.ID,
		ParseStatus: knowledge.ParseStatus,
		FileHash:    knowledge.FileHash,
		FileSize:    knowledge.FileSize,
		ProcessedAt: valueTime(knowledge.ProcessedAt),
	}, true, nil
}

func (r dbKnowledgeReader) UsesBuiltinConverter(ctx context.Context, knowledgeID string) (bool, error) {
	var knowledge types.Knowledge
	if err := r.db.WithContext(ctx).Where("id = ?", knowledgeID).First(&knowledge).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return false, nil
		}
		return false, err
	}
	if knowledge.KnowledgeBaseID == "" {
		return false, nil
	}
	var kb types.KnowledgeBase
	if err := r.db.WithContext(ctx).Where("id = ? AND tenant_id = ?", knowledge.KnowledgeBaseID, knowledge.TenantID).First(&kb).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return false, nil
		}
		return false, err
	}
	chunking := kb.ChunkingConfig
	overrides, err := knowledge.ProcessOverrides()
	if err != nil {
		return false, nil
	}
	if overrides != nil && len(overrides.ParserEngineRules) > 0 {
		chunking.ParserEngineRules = overrides.ParserEngineRules
	}
	return chunking.ResolveParserEngine(knowledge.FileType) == "", nil
}

// OriginBytes returns the source bytes of a material.
//
// The origin text is what the quoted text is proven against, so it has to be
// the file the ingest chain actually read. For an uploaded material that file
// lives in storage: its file_path is a storage reference, not a filesystem
// path. The bytes therefore come from the storage layer (files), not from the
// database.
func (r dbKnowledgeReader) OriginBytes(ctx context.Context, knowledgeID string) ([]byte, bool, error) {
	if r.files == nil {
		return nil, false, nil
	}
	return r.files.OriginBytes(ctx, knowledgeID)
}

func valueTime(value *time.Time) time.Time {
	if value == nil {
		return time.Time{}
	}
	return *value
}

var _ SourceCatalog = dbSourceCatalog{}
var _ evidence.KnowledgeReader = dbKnowledgeReader{}
