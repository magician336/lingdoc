package workspace

import (
	"context"
	"errors"
	"time"

	"github.com/Tencent/WeKnora/internal/evidence"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"gorm.io/gorm"
)

// NewGORMHandler is the production composition root for the current storage
// adapters. HTTP handlers themselves receive only the domain ports in
// HandlerDependencies.
func NewGORMHandler(db *gorm.DB, kbShares interfaces.KBShareService, knowledge interfaces.KnowledgeBaseService) *Handler {
	bindings := evidence.NewBindings(db)
	knowledgeReader := dbKnowledgeReader{db: db}
	bindings.SetKnowledgeSignalReader(knowledgeReader)
	authorizer := evidence.NewFixedAuthorizer(bindings, kbReadChecker{shares: kbShares})

	return NewHandler(HandlerDependencies{
		Service:   NewService(db, ContractDemoTemplate{}),
		Bindings:  bindings,
		Gateway:   evidence.NewAssetGateway(bindings, authorizer),
		Catalog:   dbSourceCatalog{db: db},
		Origins:   evidence.NewOriginReader(knowledgeReader),
		KBShare:   kbShares,
		Knowledge: knowledge,
	})
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

type dbKnowledgeReader struct{ db *gorm.DB }

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
