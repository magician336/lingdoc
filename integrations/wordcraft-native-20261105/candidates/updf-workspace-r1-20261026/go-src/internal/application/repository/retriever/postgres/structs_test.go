package postgres

import (
	"github.com/Tencent/WeKnora/internal/types"
	"testing"
)

func TestKeywordOnlyRowHasValidHalfvecButNoSemanticDimension(t *testing.T) {
	row := toDBVectorEmbedding(&types.IndexInfo{SourceID: "source", Content: "keyword content"}, nil)
	if row.Dimension != 0 || len(row.Embedding.Slice()) != 1 || row.Embedding.Slice()[0] != 0 {
		t.Fatalf("invalid keyword-only storage: dimension=%d vector=%v", row.Dimension, row.Embedding.Slice())
	}
	vector := toDBVectorEmbedding(&types.IndexInfo{SourceID: "source"}, map[string]any{"embedding": map[string][]float32{"source": {.1, .2}}})
	if vector.Dimension != 2 || len(vector.Embedding.Slice()) != 2 {
		t.Fatal("semantic vector changed")
	}
}
