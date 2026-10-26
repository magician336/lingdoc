package embedding

// This file is a LOCAL acceptance probe, not a product test: it proves the
// model-row -> provider-request -> budget chain for the authorized Flash model
// against the REAL isolated database, WITHOUT a single network call.
//
// It is moved out of the working tree once it has run (the zero-deletion rule:
// the probe is preserved in the evidence directory, not deleted).

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "github.com/mattn/go-sqlite3"

	"github.com/Tencent/WeKnora/internal/models/acceptancebudget"
	"github.com/Tencent/WeKnora/internal/types"
)

const (
	zzDBPath       = `D:\wt\lingdoc-484-evidence\data\lingdoc-484-final.db`
	zzEmbedModelID = "lingdoc-embed-qwen3.7-text-embedding-iso"
	zzChatModelID  = "lingdoc-chat-qwen3.8-flash-iso"
	zzTargetKBID   = "7c104e1b-eb03-48ff-9d79-8a62b73cf082"
	zzFlashName    = "qwen3.7-text-embedding-flash"
	zzPhaseBQuery  = "mesoporous oxide thin film porosity compressive strength"
	// the bound the previous (failed) window reserved for the same two chunks
	zzHistoricalBatchBound = 3085
)

func zzOpenDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", "file:"+zzDBPath+"?mode=ro")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	return db
}

func zzModelRow(t *testing.T, db *sql.DB, id string) types.Model {
	t.Helper()
	var (
		model   types.Model
		raw     []byte
		deleted sql.NullString
	)
	row := db.QueryRow(
		"select id, tenant_id, name, display_name, type, source, parameters, deleted_at from models where id = ?",
		id)
	if err := row.Scan(&model.ID, &model.TenantID, &model.Name, &model.DisplayName,
		&model.Type, &model.Source, &raw, &deleted); err != nil {
		t.Fatalf("read model %s: %v", id, err)
	}
	if deleted.Valid {
		t.Fatalf("model %s is deleted", id)
	}
	if err := json.Unmarshal(raw, &model.Parameters); err != nil {
		t.Fatalf("decode parameters of %s: %v", id, err)
	}
	return model
}

// zzIndexedTexts rebuilds the EXACT strings the indexing pipeline hands to the
// embedder: buildKnowledgeIndexContent(knowledge, chunk.EmbeddingContent()).
func zzIndexedTexts(t *testing.T, db *sql.DB) (string, []string) {
	t.Helper()
	var title string
	if err := db.QueryRow(
		"select k.title from knowledges k join knowledge_bases kb on kb.id = k.knowledge_base_id "+
			"where k.id = (select knowledge_id from chunks where knowledge_base_id = ? order by chunk_index limit 1)",
		zzTargetKBID).Scan(&title); err != nil {
		t.Fatalf("read title: %v", err)
	}
	rows, err := db.Query(
		"select content, context_header from chunks where knowledge_base_id = ? order by chunk_index",
		zzTargetKBID)
	if err != nil {
		t.Fatalf("read chunks: %v", err)
	}
	defer rows.Close()
	var texts []string
	for rows.Next() {
		var content, contextHeader string
		if err := rows.Scan(&content, &contextHeader); err != nil {
			t.Fatalf("scan chunk: %v", err)
		}
		body := strings.TrimSpace(content)
		if contextHeader != "" {
			body = contextHeader + "\n\n" + body
		}
		texts = append(texts, title+"\n"+body)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate chunks: %v", err)
	}
	if len(texts) != 2 {
		t.Fatalf("the target knowledge base has %d chunks, want 2", len(texts))
	}
	return title, texts
}

type zzRoundTripper func(*http.Request) (*http.Response, error)

func (f zzRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestZZFlashChainLocalNoNetwork(t *testing.T) {
	db := zzOpenDB(t)
	defer db.Close()

	embedModel := zzModelRow(t, db, zzEmbedModelID)
	chatModel := zzModelRow(t, db, zzChatModelID)

	// ---- 1. the row names the authorized model ----------------------------
	if embedModel.Name != zzFlashName {
		t.Fatalf("embedding model name = %q, want %q", embedModel.Name, zzFlashName)
	}
	if embedModel.Type != types.ModelTypeEmbedding {
		t.Fatalf("embedding model type = %q, want %q", embedModel.Type, types.ModelTypeEmbedding)
	}
	if chatModel.Name != "qwen3.8-flash" {
		t.Fatalf("chat model name = %q, want qwen3.8-flash", chatModel.Name)
	}
	if chatModel.Type == types.ModelTypeEmbedding {
		t.Fatalf("the chat row must not be an embedding row")
	}

	// ---- 2. the row becomes the provider configuration --------------------
	cfg := ConfigFromModel(&embedModel, "", "")
	if cfg.ModelName != zzFlashName {
		t.Fatalf("config model name = %q", cfg.ModelName)
	}
	if cfg.Provider != "aliyun" {
		t.Fatalf("config provider = %q", cfg.Provider)
	}
	if !acceptanceEmbeddingEndpointAllowed(cfg.ModelName, cfg.BaseURL) {
		t.Fatalf("the endpoint guard refuses the authorized endpoint: %s", cfg.BaseURL)
	}

	// ---- 3. the budget profile accepts the Flash model and refuses chat ---
	if !acceptancebudget.ProfileAllowsModel(acceptancebudget.QwenEmbeddingProfile, cfg.ModelName) {
		t.Fatal("the embedding profile does not allow the Flash model")
	}
	if acceptancebudget.ProfileAllowsModel(acceptancebudget.QwenEmbeddingProfile, chatModel.Name) {
		t.Fatal("the embedding profile must not allow the chat model")
	}
	if acceptancebudget.ProfileAllowsModel(acceptancebudget.QwenProfile, cfg.ModelName) {
		t.Fatal("the chat profile must not allow the embedding model")
	}

	// ---- 4. the token arithmetic of this round ----------------------------
	title, texts := zzIndexedTexts(t, db)
	t.Logf("knowledge title: %q", title)
	batchBound, err := acceptanceEmbeddingInputUpperBound(texts)
	if err != nil {
		t.Fatalf("batch bound: %v", err)
	}
	if batchBound != zzHistoricalBatchBound {
		t.Fatalf("the batch bound is %d, want %d (the bound the failed window reserved)",
			batchBound, zzHistoricalBatchBound)
	}
	queryBound, err := acceptanceEmbeddingInputUpperBound([]string{zzPhaseBQuery})
	if err != nil {
		t.Fatalf("query bound: %v", err)
	}
	cumulative := zzHistoricalBatchBound + batchBound + queryBound
	ceiling := acceptancebudget.ProfileCumulativeTokenMax(acceptancebudget.QwenEmbeddingProfile)
	if cumulative > ceiling {
		t.Fatalf("the round would reserve %d tokens, over the window ceiling %d", cumulative, ceiling)
	}
	batchCost := int64(batchBound) * acceptancebudget.QwenFlashNanoCNYPerToken
	queryCost := int64(queryBound) * acceptancebudget.QwenFlashNanoCNYPerToken
	t.Logf("batch bound=%d (cost %d nano-CNY), query bound=%d (cost %d nano-CNY), "+
		"cumulative with the settled request=%d of %d tokens",
		batchBound, batchCost, queryBound, queryCost, cumulative, ceiling)

	// ---- 5. one batch request carries both chunks -------------------------
	ledgerPath := filepath.Join(t.TempDir(), "flash.json")
	t.Setenv(acceptancebudget.ProfileEnv, acceptancebudget.QwenEmbeddingProfile)
	t.Setenv(acceptancebudget.LedgerEnv, ledgerPath)

	embedder, err := NewOpenAIEmbedder("local-probe-key", cfg.BaseURL, cfg.ModelName,
		0, cfg.Dimensions, cfg.ModelID, nil)
	if err != nil {
		t.Fatalf("new embedder: %v", err)
	}
	calls := 0
	var seenModel string
	var seenInputs []string
	var seenURL string
	embedder.httpClient = &http.Client{Transport: zzRoundTripper(func(req *http.Request) (*http.Response, error) {
		calls++
		seenURL = req.URL.String()
		var body OpenAIEmbedRequest
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			return nil, err
		}
		seenModel = body.Model
		seenInputs = body.Input
		vectors := make([]map[string]any, 0, len(body.Input))
		for index := range body.Input {
			vectors = append(vectors, map[string]any{"embedding": make([]float32, 1024), "index": index})
		}
		payload, _ := json.Marshal(map[string]any{
			"data":  vectors,
			"usage": map[string]int{"prompt_tokens": 7, "total_tokens": 7},
		})
		return &http.Response{StatusCode: 200,
			Header: http.Header{"Content-Type": []string{"application/json"}},
			Body:   io.NopCloser(strings.NewReader(string(payload)))}, nil
	})}

	vectors, err := embedder.BatchEmbed(context.Background(), texts)
	if err != nil {
		t.Fatalf("batch embed: %v", err)
	}
	if calls != 1 {
		t.Fatalf("the two chunks took %d provider calls, want 1", calls)
	}
	if len(vectors) != 2 {
		t.Fatalf("got %d vectors, want 2", len(vectors))
	}
	if seenModel != zzFlashName {
		t.Fatalf("the request body carries model %q, want %q", seenModel, zzFlashName)
	}
	if len(seenInputs) != 2 {
		t.Fatalf("the request body carries %d inputs, want 2", len(seenInputs))
	}
	if !strings.Contains(seenURL, "/compatible-mode/v1/embeddings") {
		t.Fatalf("unexpected request target: %s", seenURL)
	}

	// ---- 6. the ledger records one Flash call at the Flash price ----------
	raw, err := os.ReadFile(ledgerPath + ".calls.json")
	if err != nil {
		t.Fatalf("read ledger: %v", err)
	}
	var ledger acceptancebudget.Ledger
	if err := json.Unmarshal(raw, &ledger); err != nil {
		t.Fatalf("decode ledger: %v", err)
	}
	if len(ledger.Requests) != 1 {
		t.Fatalf("the ledger has %d requests, want 1", len(ledger.Requests))
	}
	request := ledger.Requests[0]
	if request.Model != zzFlashName {
		t.Fatalf("the ledger records model %q", request.Model)
	}
	if request.Status != "response_received" {
		t.Fatalf("the ledger records status %q", request.Status)
	}
	if request.InputUpperBound != batchBound {
		t.Fatalf("the ledger records the bound %d, want %d", request.InputUpperBound, batchBound)
	}
	if request.ReservedNanoCNY != batchCost {
		t.Fatalf("the ledger reserves %d nano-CNY, want %d", request.ReservedNanoCNY, batchCost)
	}
	if request.ActualNanoCNY != 7*acceptancebudget.QwenFlashNanoCNYPerToken {
		t.Fatalf("the ledger charges %d nano-CNY for 7 reported tokens", request.ActualNanoCNY)
	}
	if ledger.Blocked {
		t.Fatalf("the window is blocked: %s", ledger.BlockedReason)
	}
	t.Logf("ledger: model=%s status=%s bound=%d reserved=%d actual=%d",
		request.Model, request.Status, request.InputUpperBound,
		request.ReservedNanoCNY, request.ActualNanoCNY)
}
