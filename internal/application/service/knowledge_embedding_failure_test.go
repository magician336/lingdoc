package service

import (
	"context"
	"errors"
	"github.com/Tencent/WeKnora/internal/models/embedding"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"strings"
	"testing"
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

type unavailableEmbeddingModel struct{ interfaces.ModelService }

func (unavailableEmbeddingModel) GetEmbeddingModel(context.Context, string) (embedding.Embedder, error) {
	return nil, errors.New("model ID cannot be empty")
}
func TestMissingEmbeddingModelTerminatesKnowledgeProcessing(t *testing.T) {
	k := &types.Knowledge{ID: "missing-model", TenantID: 1, ParseStatus: types.ParseStatusProcessing}
	repo := &embeddingFailureRepo{knowledge: k}
	s := &knowledgeService{repo: repo, modelService: unavailableEmbeddingModel{}}
	s.processChunks(context.Background(), &types.KnowledgeBase{IndexingStrategy: types.IndexingStrategy{KeywordEnabled: true}}, k, nil)
	if !repo.updated || repo.knowledge.ParseStatus != types.ParseStatusFailed || !strings.Contains(repo.knowledge.ErrorMessage, "model ID") {
		t.Fatalf("processing did not report actionable failure: updated=%v knowledge=%+v", repo.updated, repo.knowledge)
	}
}
