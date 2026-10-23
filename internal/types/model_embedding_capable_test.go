package types

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
)

// TestIsEmbeddingCapableModelType pins the rule the embedding client path
// relies on: only the model types whose upstream speaks the embeddings
// protocol may be turned into an embedder.
//
// KnowledgeQA, Rerank, ASR and VLLM talk to other endpoint families, so an
// embedder built for them produces a call the provider rejects (HTTP 400, no
// vector) — and both the ingestion and the retrieval side of a knowledge base
// break while the cause (a knowledge base bound to the wrong model row) stays
// far away.
func TestIsEmbeddingCapableModelType(t *testing.T) {
	cases := []struct {
		modelType ModelType
		want      bool
	}{
		{ModelTypeEmbedding, true},
		{ModelTypeKnowledgeQA, false},
		{ModelTypeRerank, false},
		{ModelTypeASR, false},
		{ModelTypeVLLM, false},
		{ModelType(""), false},
		{ModelType("Chat"), false},
	}
	for _, c := range cases {
		if got := IsEmbeddingCapableModelType(c.modelType); got != c.want {
			t.Errorf("IsEmbeddingCapableModelType(%q) = %v, want %v", c.modelType, got, c.want)
		}
	}
}

// TestIsEmbeddingCapableModelTypeFollowsTypeNotName pins that the verdict
// follows the model type: renaming a model must not change what it can serve.
func TestIsEmbeddingCapableModelTypeFollowsTypeNotName(t *testing.T) {
	// A chat-named model of Embedding type is still an embedding model.
	if !IsEmbeddingCapableModelType(ModelTypeEmbedding) {
		t.Fatal("an Embedding model must stay embedding capable, whatever it is called")
	}
	// An embedding-named model of KnowledgeQA type is a chat model.
	if IsEmbeddingCapableModelType(ModelTypeKnowledgeQA) {
		t.Fatal("a KnowledgeQA model must not be embedding capable, whatever it is called")
	}
}

// TestModelCapabilityPredicatesHaveOneDeclarationSite pins that the
// "model type × purpose" rules live in exactly one place.
//
// The chat side already had a predicate (IsChatCapableModelType) and the
// embedding side had none, so a second, hand-written word list could have
// appeared anywhere. This test walks the Go sources of the main module and
// asserts that each capability predicate is declared exactly once, and that
// the embedding predicate sits next to the other two — the model TYPE
// vocabulary has a single home.
//
// The walk covers internal/ and cmd/ — the Go roots of the main module.
// cli/ and client/ are separate modules with their own SDK mirror of the type
// vocabulary (client/model.go); a predicate there would be a different
// contract, not a second source for the server's rules.
func TestModelCapabilityPredicatesHaveOneDeclarationSite(t *testing.T) {
	predicates := []string{
		"IsSummaryCapableModelType",
		"IsChatCapableModelType",
		"IsEmbeddingCapableModelType",
	}

	declarations := map[string][]string{}
	fset := token.NewFileSet()
	root := filepath.Join("..", "..")

	for _, sourceRoot := range []string{"internal", "cmd"} {
		err := filepath.WalkDir(filepath.Join(root, sourceRoot),
			func(path string, entry fs.DirEntry, err error) error {
				if err != nil {
					return err
				}
				if entry.IsDir() {
					return nil
				}
				if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
					return nil
				}
				file, parseErr := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
				if parseErr != nil {
					return parseErr
				}
				rel, relErr := filepath.Rel(root, path)
				if relErr != nil {
					return relErr
				}
				for _, decl := range file.Decls {
					fn, ok := decl.(*ast.FuncDecl)
					if !ok || fn.Recv != nil {
						continue
					}
					for _, predicate := range predicates {
						if fn.Name.Name == predicate {
							declarations[predicate] = append(
								declarations[predicate], filepath.ToSlash(rel))
						}
					}
				}
				return nil
			})
		if err != nil {
			t.Fatalf("walk %s: %v", sourceRoot, err)
		}
	}

	for _, predicate := range predicates {
		sites := declarations[predicate]
		if len(sites) == 0 {
			t.Errorf("%s is not declared anywhere in the main module", predicate)
			continue
		}
		if len(sites) > 1 {
			t.Errorf("%s is declared more than once: %v — the type vocabulary must have one home",
				predicate, sites)
			continue
		}
		if sites[0] != "internal/types/model.go" {
			t.Errorf("%s is declared in %q, want internal/types/model.go", predicate, sites[0])
		}
	}
}
