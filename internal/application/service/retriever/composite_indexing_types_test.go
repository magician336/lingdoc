package retriever

import (
	"context"
	"errors"
	"github.com/Tencent/WeKnora/internal/models/embedding"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"slices"
	"testing"
)

type indexingRoutingSpy struct {
	interfaces.RetrieveEngineService
	routed []types.RetrieverType
}

func (s *indexingRoutingSpy) BatchIndex(_ context.Context, _ embedding.Embedder, _ []*types.IndexInfo, kinds []types.RetrieverType) error {
	s.routed = slices.Clone(kinds)
	return nil
}
func TestKeywordOnlyIndexingDoesNotRouteVectorPipeline(t *testing.T) {
	spy := &indexingRoutingSpy{}
	original := &CompositeRetrieveEngine{engineInfos: []*engineInfo{{retrieveEngine: spy, retrieverType: []types.RetrieverType{types.VectorRetrieverType, types.KeywordsRetrieverType}}}}
	keyword, err := original.WithRetrieverTypes([]types.RetrieverType{types.KeywordsRetrieverType})
	if err != nil {
		t.Fatal(err)
	}
	if err := keyword.BatchIndex(context.Background(), nil, []*types.IndexInfo{{SourceID: "document-part"}}); err != nil {
		t.Fatal(err)
	}
	if len(spy.routed) != 1 || spy.routed[0] != types.KeywordsRetrieverType {
		t.Fatalf("keyword-only indexing routed %v", spy.routed)
	}
	if !original.SupportRetriever(types.VectorRetrieverType) || keyword.SupportRetriever(types.VectorRetrieverType) {
		t.Fatal("filter mutated another routing view or retained vector indexing")
	}
}

func TestRetrieverTypeFilteringRejectsMissingRoutesBeforeBatchIndex(t *testing.T) {
	spy := &indexingRoutingSpy{}
	original := &CompositeRetrieveEngine{engineInfos: []*engineInfo{{
		retrieveEngine: spy,
		retrieverType:  []types.RetrieverType{types.KeywordsRetrieverType},
	}}}

	for _, allowed := range [][]types.RetrieverType{
		nil,
		{types.VectorRetrieverType},
	} {
		filtered, err := original.WithRetrieverTypes(allowed)
		if !errors.Is(err, ErrNoRetrieverForTypes) {
			t.Fatalf("allowed=%v error = %v, want ErrNoRetrieverForTypes", allowed, err)
		}
		if filtered != nil {
			t.Fatalf("allowed=%v returned a routing view after a missing route", allowed)
		}
	}
	if len(spy.routed) != 0 {
		t.Fatalf("missing route reached BatchIndex with %v", spy.routed)
	}
}
