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

// OriginFileReader obtains bytes through the configured resource/storage ports.
type OriginFileReader interface {
	OriginBytes(context.Context, string) ([]byte, bool, error)
}

type KnowledgeFileSource interface {
	GetKnowledgeFile(context.Context, string) (io.ReadCloser, string, error)
}

type KnowledgeFileOriginReader struct{ Service KnowledgeFileSource }

func (r KnowledgeFileOriginReader) OriginBytes(ctx context.Context, id string) ([]byte, bool, error) {
	if r.Service == nil || id == "" {
		return nil, false, nil
	}
	reader, _, err := r.Service.GetKnowledgeFile(ctx, id)
	if err != nil {
		return nil, false, err
	}
	if reader == nil {
		return nil, false, nil
	}
	defer reader.Close()
	data, err := io.ReadAll(io.LimitReader(reader, evidence.MaxOriginBytes+1))
	if err != nil {
		return nil, false, err
	}
	if len(data) == 0 || len(data) > evidence.MaxOriginBytes {
		return nil, false, nil
	}
	return data, true, nil
}

type dbKnowledgeReader struct {
	db    *gorm.DB
	files OriginFileReader
}

func (r dbKnowledgeReader) OriginBytes(ctx context.Context, id string) ([]byte, bool, error) {
	if r.files == nil {
		return nil, false, nil
	}
	return r.files.OriginBytes(ctx, id)
}

func (r dbKnowledgeReader) ParsedOriginForKnowledge(ctx context.Context, row *types.Knowledge) (*evidence.ParsedOriginSnapshot, error) {
	return evidence.NewParsedOriginStore(r.db).ReadCurrent(ctx, row)
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

func valueTime(value *time.Time) time.Time {
	if value == nil {
		return time.Time{}
	}
	return *value
}

var _ SourceCatalog = dbSourceCatalog{}
var _ evidence.KnowledgeReader = dbKnowledgeReader{}
