package service

import (
	"context"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

type embeddingFailureRepo struct {
	interfaces.KnowledgeRepository
	knowledge *types.Knowledge
	updated   bool
}

func (r *embeddingFailureRepo) GetKnowledgeByID(context.Context, uint64, string) (*types.Knowledge, error) {
	return r.knowledge, nil
}

func (r *embeddingFailureRepo) UpdateKnowledge(_ context.Context, k *types.Knowledge) error {
	r.knowledge = k
	r.updated = true
	return nil
}

// A knowledge base without a usable embedding model must NOT take the
// document down with it.
//
// The chunks are the carrier of the source anchors (coordinates plus quoted
// text). When the embedding model is missing, the document is still parsed
// and its chunks are stored; only the retrieval index is missing, and the
// chunks say so (index_status=failed). Terminating the processing instead
// turns "this document is not searchable" into "this document does not
// exist" — and every operation that re-checks the sources of that material
// is then refused for a reason that has nothing to do with the material
// (#485 / D5: the material could not be ingested at all).
func TestMissingEmbeddingModelDoesNotTerminateKnowledgeProcessing(t *testing.T) {
	k := &types.Knowledge{ID: "missing-model", TenantID: 1, ParseStatus: types.ParseStatusProcessing}
	repo := &embeddingFailureRepo{knowledge: k}
	chunks := &embeddingDecoupledChunkService{}
	s := &knowledgeService{
		repo:           repo,
		chunkRepo:      chunks,
		modelService:   &unavailableEmbeddingModelService{},
		retrieveEngine: parentChildRetrieveRegistry{engine: &parentChildRetrieveEngine{}},
		graphEngine:    parentChildGraphRepo{},
		tenantRepo:     parentChildTenantRepo{},
		task:           parentChildTaskEnqueuer{},
	}
	ctx := context.WithValue(context.Background(), types.TenantInfoContextKey, embeddingDecoupledTestTenant())
	s.processChunks(
		ctx,
		&types.KnowledgeBase{IndexingStrategy: types.IndexingStrategy{KeywordEnabled: true}},
		k,
		[]types.ParsedChunk{{Content: "第一段", Seq: 0, Start: 0, End: 3}},
	)
	if repo.knowledge.ParseStatus == types.ParseStatusFailed {
		t.Fatalf("a missing embedding model must not fail the parse: %+v", repo.knowledge)
	}
	if strings.Contains(repo.knowledge.ErrorMessage, "model ID") {
		t.Fatalf("the parse must not carry the embedding failure as its own error: %q", repo.knowledge.ErrorMessage)
	}
	if len(chunks.created) != 1 {
		t.Fatalf("the chunks must be stored even without an embedding model: %+v", chunks.created)
	}
	if chunks.created[0].IndexStatus != "failed" {
		t.Fatalf("a chunk that was never indexed must say so: %+v", chunks.created[0])
	}
}
