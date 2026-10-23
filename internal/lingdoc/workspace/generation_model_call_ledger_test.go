package workspace

import (
	"context"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/lingdoc/generation"
	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
	"github.com/Tencent/WeKnora/internal/types"
)

// recordingModelCallLedger keeps the rows one run leaves.
type recordingModelCallLedger struct {
	entries []core.ModelCallLedgerEntry
}

func (l *recordingModelCallLedger) RecordModelCall(_ context.Context, entry core.ModelCallLedgerEntry) error {
	l.entries = append(l.entries, entry)
	return nil
}

func (l *recordingModelCallLedger) RecordModelSwitch(context.Context, core.ModelCallLedgerEntry) error {
	return nil
}

func (l *recordingModelCallLedger) ModelCallEntriesBySource(context.Context, uint64, string, string) ([]core.ModelCallLedgerEntry, error) {
	return l.entries, nil
}

func (l *recordingModelCallLedger) ModelSwitchEntriesByProject(context.Context, uint64, string) ([]core.ModelCallLedgerEntry, error) {
	return nil, nil
}

func generationLedgerFixture() (*acceptanceChatStub, *acceptanceModelServiceStub) {
	chat := &acceptanceChatStub{response: &types.ChatResponse{
		Content: `{"body_markdown":"合成正文 [[source:source-1]]","source_ids":["source-1"],"review_items":[]}`,
		Usage:   types.TokenUsage{PromptTokens: 128, CompletionTokens: 64},
	}}
	service := &acceptanceModelServiceStub{models: []*types.Model{acceptanceDeepSeekModel()}, chatModel: chat}
	return chat, service
}

func TestGenerationHostModelLeavesOneLedgerRowPerModelCall(t *testing.T) {
	_, service := generationLedgerFixture()
	ledger := &recordingModelCallLedger{}
	host := generationHostModel{models: service, templates: core.ContractDemoTemplate{}, ledger: ledger}

	ctx := generation.WithRunID(context.Background(), "run-1")
	if _, err := host.Generate(ctx, acceptanceGenerationInput()); err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	if len(ledger.entries) != 1 {
		t.Fatalf("ledger rows = %d, want exactly one row per model call", len(ledger.entries))
	}
	row := ledger.entries[0]
	if row.SourceKind != core.ModelCallLedgerSourceGenerationRun {
		t.Errorf("source kind = %q, want %q", row.SourceKind, core.ModelCallLedgerSourceGenerationRun)
	}
	if row.SourceID != "run-1" {
		t.Errorf("source id = %q, want the generation run id", row.SourceID)
	}
	if row.Status != core.ModelCallLedgerStatusSucceeded {
		t.Errorf("status = %q, want %q", row.Status, core.ModelCallLedgerStatusSucceeded)
	}
	if row.ModelID == "" || row.Provider == "" {
		t.Errorf("the row does not name the model that answered: %+v", row)
	}
	if row.RunMode == "" {
		t.Errorf("the row does not carry a run mode: %+v", row)
	}
	if row.InputTokens != 128 || row.OutputTokens != 64 {
		t.Errorf("the row does not carry the reported tokens: %+v", row)
	}
}

func TestGenerationHostModelRecordsAFailedCallAsAFailedRow(t *testing.T) {
	chat, service := generationLedgerFixture()
	chat.response = nil
	chat.err = errors.New("provider refused the call")
	ledger := &recordingModelCallLedger{}
	host := generationHostModel{models: service, templates: core.ContractDemoTemplate{}, ledger: ledger}

	ctx := generation.WithRunID(context.Background(), "run-2")
	if _, err := host.Generate(ctx, acceptanceGenerationInput()); err == nil {
		t.Fatal("a provider that refused must not look like a successful draft")
	}
	if len(ledger.entries) != 1 {
		t.Fatalf("ledger rows = %d, want one row even though the call failed", len(ledger.entries))
	}
	if ledger.entries[0].Status != core.ModelCallLedgerStatusFailed {
		t.Errorf("status = %q, want %q", ledger.entries[0].Status, core.ModelCallLedgerStatusFailed)
	}
}

func TestGenerationHostModelLeavesOneRowPerCallNotPerRun(t *testing.T) {
	_, service := generationLedgerFixture()
	ledger := &recordingModelCallLedger{}
	host := generationHostModel{models: service, templates: core.ContractDemoTemplate{}, ledger: ledger}

	ctx := generation.WithRunID(context.Background(), "run-3")
	for i := 0; i < 2; i++ {
		if _, err := host.Generate(ctx, acceptanceGenerationInput()); err != nil {
			t.Fatalf("Generate() call %d error = %v", i+1, err)
		}
	}
	if len(ledger.entries) != 2 {
		t.Fatalf("ledger rows = %d, want one row per call (2), not one row per run", len(ledger.entries))
	}
}

func TestGenerationHostModelWithoutALedgerStillGenerates(t *testing.T) {
	_, service := generationLedgerFixture()
	host := generationHostModel{models: service, templates: core.ContractDemoTemplate{}}

	ctx := generation.WithRunID(context.Background(), "run-4")
	if _, err := host.Generate(ctx, acceptanceGenerationInput()); err != nil {
		t.Fatalf("Generate() error = %v: no ledger must not change what the run does", err)
	}
}
