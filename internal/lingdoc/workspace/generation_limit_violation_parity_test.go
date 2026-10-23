package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/Tencent/WeKnora/internal/lingdoc/candidateadoption"
	"github.com/Tencent/WeKnora/internal/lingdoc/generation"
	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
	"github.com/Tencent/WeKnora/internal/models/acceptancebudget"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// The tests below are offline. The chat client is a stub that counts the
// calls it receives, so every assertion about "what the provider was asked
// for" is a fact about the stub and not an inference.
//
// What is pinned down here is ONE fact seen from two ledgers:
//
//   - the round ledger (the generation ledger of one turn) and
//   - the shared budget ledger (the ledger every controlled call shares)
//
// must give the SAME verdict about the SAME answer. An answer over the
// active profile's ceiling is a limit violation in both of them, and an
// answer inside the ceiling is not a violation in either of them.
//
// The two ledgers are compared the way a reader compares them: both are
// read as raw JSON files and the "status" field of their one request is
// taken at face value. Neither ledger's own parser is used for the
// comparison, so the assertion cannot be satisfied by one ledger's reader
// agreeing with itself.

type parityModelServiceStub struct {
	interfaces.ModelService
	models    []*types.Model
	chatModel chat.Chat
}

func (s *parityModelServiceStub) ListModels(context.Context) ([]*types.Model, error) {
	return s.models, nil
}

func (s *parityModelServiceStub) GetChatModel(_ context.Context, _ string) (chat.Chat, error) {
	return s.chatModel, nil
}

// parityChatStub answers with one fixed response and counts the calls.
type parityChatStub struct {
	response *types.ChatResponse
	calls    int
	options  *chat.ChatOptions
}

func (s *parityChatStub) Chat(_ context.Context, _ []chat.Message, options *chat.ChatOptions) (*types.ChatResponse, error) {
	s.calls++
	s.options = options
	return s.response, nil
}

func (*parityChatStub) ChatStream(context.Context, []chat.Message, *chat.ChatOptions) (<-chan types.StreamResponse, error) {
	return nil, nil
}

func (*parityChatStub) GetModelName() string { return acceptancebudget.QwenChatModel }
func (*parityChatStub) GetModelID() string   { return "qwen-1" }

// parityApprovedQwenModel is the model row the controlled Qwen profile
// approves.
func parityApprovedQwenModel() *types.Model {
	return &types.Model{
		ID: "qwen-1", Name: acceptancebudget.QwenChatModel,
		Type: types.ModelTypeKnowledgeQA, Source: types.ModelSourceRemote, Status: types.ModelStatusActive,
		Parameters: types.ModelParameters{
			Provider: acceptancebudget.QwenProvider,
			BaseURL:  acceptancebudget.QwenApprovedBaseURLs[0],
		},
	}
}

// parityApprovedDeepSeekModel is the model row the priced profile approves.
func parityApprovedDeepSeekModel() *types.Model {
	return &types.Model{
		ID: "ds-1", Name: acceptancebudget.DeepSeekModel,
		Type: types.ModelTypeKnowledgeQA, Source: types.ModelSourceRemote, Status: types.ModelStatusActive,
		Parameters: types.ModelParameters{
			Provider: "generic", BaseURL: "https://api.deepseek.com",
			ExtraConfig: map[string]string{chat.ExtraConfigThinkingControl: "thinking_type"},
		},
	}
}

// parityBudget arms the controlled budget of one profile in a fresh
// directory, so every case owns its own two ledgers.
func parityBudget(t *testing.T, profile string) *generationAcceptanceBudget {
	t.Helper()
	t.Setenv(generationAcceptanceProfileEnv, profile)
	t.Setenv(generationAcceptanceLedgerEnv, filepath.Join(t.TempDir(), "round.json"))
	budget, err := loadGenerationAcceptanceBudget()
	if err != nil || budget == nil {
		t.Fatalf("loadGenerationAcceptanceBudget() = %v, %v; want the %s profile armed", budget, err, profile)
	}
	return budget
}

// parityLedgerStatus reads the status a reader of the ledger file sees.
//
// It reads the raw JSON on purpose: the point of the assertion is the field
// a reader of the ledger file sees, not an in-memory copy of it.
func parityLedgerStatus(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read the ledger %s: %v", path, err)
	}
	var record struct {
		Requests []struct {
			Status string `json:"status"`
		} `json:"requests"`
	}
	if err := json.Unmarshal(data, &record); err != nil {
		t.Fatalf("decode the ledger %s: %v", path, err)
	}
	if len(record.Requests) != 1 {
		t.Fatalf("the ledger %s holds %d requests, want exactly 1", path, len(record.Requests))
	}
	return record.Requests[0].Status
}

// parityLedgerOutputLimit reads the ceiling the ledger recorded for its one
// request, so the two ledgers can be shown to measure the answer against
// the same ceiling.
func parityLedgerOutputLimit(t *testing.T, path string) int {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read the ledger %s: %v", path, err)
	}
	var record struct {
		Requests []struct {
			OutputLimit int `json:"output_limit_tokens"`
		} `json:"requests"`
	}
	if err := json.Unmarshal(data, &record); err != nil {
		t.Fatalf("decode the ledger %s: %v", path, err)
	}
	if len(record.Requests) != 1 {
		t.Fatalf("the ledger %s holds %d requests, want exactly 1", path, len(record.Requests))
	}
	return record.Requests[0].OutputLimit
}

// The two ledgers must give the same verdict about the same answer.
//
// The first case is the one that used to break: under the Qwen profile an
// answer of 4097 tokens is over the profile's ceiling (4096) and under the
// other profile's constant (6000), so a check that read the constant
// wrote "response_received" into the round ledger while the shared budget
// ledger -- which measures the answer against the ceiling the request
// carried -- wrote "limit_violation".
func TestGenerationGivesBothLedgersTheSameVerdictAboutTheSameAnswer(t *testing.T) {
	cases := []struct {
		name             string
		profile          string
		model            *types.Model
		completionTokens int
		wantViolation    bool
	}{
		{
			name:             "an answer between the profile ceiling and the other profile's constant is a violation in both ledgers",
			profile:          acceptancebudget.QwenProfile,
			model:            parityApprovedQwenModel(),
			completionTokens: acceptancebudget.QwenOutputMax + 1,
			wantViolation:    true,
		},
		{
			name:             "an answer exactly at the profile ceiling is a violation in neither ledger",
			profile:          acceptancebudget.QwenProfile,
			model:            parityApprovedQwenModel(),
			completionTokens: acceptancebudget.QwenOutputMax,
			wantViolation:    false,
		},
		{
			name:             "the priced profile keeps its own ceiling",
			profile:          acceptancebudget.Profile,
			model:            parityApprovedDeepSeekModel(),
			completionTokens: acceptancebudget.OutputMax + 1,
			wantViolation:    true,
		},
		{
			name:             "the priced profile keeps accepting an answer inside its ceiling",
			profile:          acceptancebudget.Profile,
			model:            parityApprovedDeepSeekModel(),
			completionTokens: acceptancebudget.OutputMax,
			wantViolation:    false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			budget := parityBudget(t, tc.profile)
			stub := &parityChatStub{response: &types.ChatResponse{
				Content:      `{"body_markdown":"受控执行结论 [[source:source-1]]","source_ids":["source-1"],"review_items":[]}`,
				FinishReason: "stop",
				Usage: types.TokenUsage{PromptTokens: 320, CompletionTokens: tc.completionTokens,
					TotalTokens: 320 + tc.completionTokens},
			}}
			service := &parityModelServiceStub{models: []*types.Model{tc.model}, chatModel: stub}
			host := generationHostModel{models: service, templates: core.ContractDemoTemplate{},
				acceptanceBudget: budget}

			_, err := host.Generate(context.Background(), parityGenerationInput())
			if tc.wantViolation {
				if !errors.Is(err, generation.ErrAcceptanceBudgetExceeded) {
					t.Fatalf("Generate() error = %v, want the budget to be exceeded", err)
				}
			} else if err != nil {
				t.Fatalf("Generate() error = %v, want a normal answer", err)
			}
			if stub.calls != 1 {
				t.Fatalf("provider calls = %d, want exactly 1", stub.calls)
			}

			roundStatus := parityLedgerStatus(t, budget.ledgerPath)
			sharedStatus := parityLedgerStatus(t, budget.ledgerPath+".calls.json")

			// The two ledgers must measure the answer against the same
			// ceiling, otherwise they could not be expected to agree.
			roundLimit := parityLedgerOutputLimit(t, budget.ledgerPath)
			sharedLimit := parityLedgerOutputLimit(t, budget.ledgerPath+".calls.json")
			if roundLimit != sharedLimit {
				t.Fatalf("the two ledgers measure the answer against different ceilings: %d and %d",
					roundLimit, sharedLimit)
			}

			// The core invariant: one answer, one verdict, in both ledgers.
			roundViolation := roundStatus == "limit_violation"
			sharedViolation := sharedStatus == "limit_violation"
			if roundViolation != sharedViolation {
				t.Fatalf("the two ledgers disagree about the same answer: the round ledger says %q, the shared budget ledger says %q",
					roundStatus, sharedStatus)
			}
			if roundViolation != tc.wantViolation {
				t.Fatalf("the round ledger says %q, want a limit violation = %v", roundStatus, tc.wantViolation)
			}
			if sharedViolation != tc.wantViolation {
				t.Fatalf("the shared budget ledger says %q, want a limit violation = %v", sharedStatus, tc.wantViolation)
			}
		})
	}
}

// The request carries the profile's ceiling, and both ledgers record that
// same ceiling: the number the answer is measured against is the number the
// request asked for.
func TestGenerationMeasuresTheAnswerAgainstTheCeilingTheRequestCarried(t *testing.T) {
	budget := parityBudget(t, acceptancebudget.QwenProfile)
	stub := &parityChatStub{response: &types.ChatResponse{
		Content:      `{"body_markdown":"受控执行结论 [[source:source-1]]","source_ids":["source-1"],"review_items":[]}`,
		FinishReason: "stop",
		Usage:        types.TokenUsage{PromptTokens: 320, CompletionTokens: 128, TotalTokens: 448},
	}}
	service := &parityModelServiceStub{models: []*types.Model{parityApprovedQwenModel()}, chatModel: stub}
	host := generationHostModel{models: service, templates: core.ContractDemoTemplate{},
		acceptanceBudget: budget}

	if _, err := host.Generate(context.Background(), parityGenerationInput()); err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	if stub.options == nil || stub.options.MaxCompletionTokens != acceptancebudget.QwenOutputMax {
		t.Fatalf("request options = %+v, want the profile's output ceiling %d in the request",
			stub.options, acceptancebudget.QwenOutputMax)
	}
	roundLimit := parityLedgerOutputLimit(t, budget.ledgerPath)
	sharedLimit := parityLedgerOutputLimit(t, budget.ledgerPath+".calls.json")
	if roundLimit != stub.options.MaxCompletionTokens || sharedLimit != stub.options.MaxCompletionTokens {
		t.Fatalf("the ledgers record the ceilings %d and %d, but the request asked for %d",
			roundLimit, sharedLimit, stub.options.MaxCompletionTokens)
	}
}

// The uncontrolled path is not the controlled profile's business: without a
// budget the generation adapter keeps the output budget it always had and
// writes no limit violation anywhere.
func TestUncontrolledGenerationKeepsItsOwnOutputBudget(t *testing.T) {
	t.Setenv(generationAcceptanceProfileEnv, "")
	stub := &parityChatStub{response: &types.ChatResponse{
		Content:      `{"body_markdown":"受控执行结论 [[source:source-1]]","source_ids":["source-1"],"review_items":[]}`,
		FinishReason: "stop",
		Usage:        types.TokenUsage{PromptTokens: 100, CompletionTokens: 30, TotalTokens: 130},
	}}
	service := &parityModelServiceStub{models: []*types.Model{parityApprovedDeepSeekModel()}, chatModel: stub}
	host := generationHostModel{models: service, templates: core.ContractDemoTemplate{}}

	if _, err := host.Generate(context.Background(), parityGenerationInput()); err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	if stub.options == nil || stub.options.MaxCompletionTokens != 4096 {
		t.Fatalf("request options = %+v, want the uncontrolled budget 4096", stub.options)
	}
}

func parityGenerationInput() generation.Input {
	return generation.Input{
		Actor: generation.Actor{TenantID: 1, UserID: "synthetic-parity-user"},
		Workspace: candidateadoption.GenerationContext{
			ProjectID: "synthetic-project", ChapterID: "synthetic-chapter", SpecRevision: 1,
			Chapter: candidateadoption.Chapter{ID: "synthetic-chapter", ProjectID: "synthetic-project",
				SectionID: "question", Title: "合成研究问题"},
		},
		Basis:       candidateadoption.Basis{TemplateID: "template-demo", TemplateVersion: "1", SpecRevision: 1},
		ProjectSpec: map[string]string{"research_subject": "合成课题", "research_goal": "验证两本账的一致性"},
		Sources: []generation.Source{{ID: "source-1", ProjectID: "synthetic-project", AssetID: "synthetic-asset",
			AssetRevision: 1, Locator: "page:1", QuotedText: "本轮合成资料只用于验证两本账对同一事实的结论一致。"}},
		Request: generation.Request{ChapterID: "synthetic-chapter", Instruction: "依据合成资料写一段短研究问题"},
	}
}
