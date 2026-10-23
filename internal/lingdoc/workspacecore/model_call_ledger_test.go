package workspacecore_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/Tencent/WeKnora/internal/lingdoc/workspace"
	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
	"github.com/Tencent/WeKnora/internal/models/acceptancebudget"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// The model call ledger is the fact table every model call and every model
// switch leaves behind (55-Spec, issue #453).
//
// What these tests assert is what the ledger can ANSWER, not how many columns
// the table has:
//
//   - one call leaves one row, with the provider, the model, the parameters,
//     the budget, the tokens, the duration and the status;
//   - one switch leaves one row, with the model it came from, the reason and
//     the steps it affected;
//   - a failed call is recorded as a failure with its error code, never as a
//     success;
//   - the rows can be filtered by run mode, so a real sample can be told from
//     a mock one mechanically;
//   - the rows point at the product action that caused them;
//   - the model adapter really leaves the row (the application seam, not a
//     direct store call).
//
// The model never runs against a real provider: the model service is a stub
// that answers with fixed tokens, so the numbers below are deterministic.

var (
	ledgerSnapshotOnce sync.Once
	ledgerSnapshotPath string
	ledgerSnapshotErr  error
)

// ledgerMigratedSnapshot builds the production SQLite migration chain once
// and hands every test its own copy of the result. Replaying the chain per
// test would cost the whole package a second of setup for no extra coverage.
func ledgerMigratedSnapshot(tb testing.TB) string {
	tb.Helper()
	ledgerSnapshotOnce.Do(func() {
		dir, err := os.MkdirTemp("", "lingdoc-model-call-ledger-")
		if err != nil {
			ledgerSnapshotErr = err
			return
		}
		seed := filepath.Join(dir, "seed.db")
		snapshot := filepath.Join(dir, "snapshot.db")
		db, err := openLedgerSQLite(seed)
		if err != nil {
			ledgerSnapshotErr = err
			return
		}
		defer func() {
			if sqlDB, dbErr := db.DB(); dbErr == nil {
				_ = sqlDB.Close()
			}
		}()
		for _, name := range ledgerMigrationNames(tb) {
			body, readErr := os.ReadFile(filepath.Join("..", "..", "..", "migrations", "sqlite", name))
			if readErr != nil {
				ledgerSnapshotErr = readErr
				return
			}
			if execErr := db.Exec(string(body)).Error; execErr != nil {
				ledgerSnapshotErr = execErr
				return
			}
		}
		if execErr := db.Exec("VACUUM INTO ?", snapshot).Error; execErr != nil {
			ledgerSnapshotErr = execErr
			return
		}
		ledgerSnapshotPath = snapshot
	})
	if ledgerSnapshotErr != nil {
		tb.Fatalf("build the migrated SQLite snapshot: %v", ledgerSnapshotErr)
	}
	return ledgerSnapshotPath
}

func ledgerMigrationNames(tb testing.TB) []string {
	tb.Helper()
	matches, err := filepath.Glob(filepath.Join("..", "..", "..", "migrations", "sqlite", "*_lingdoc_*.up.sql"))
	if err != nil || len(matches) == 0 {
		tb.Fatalf("find the production LingDoc migrations: count=%d error=%v", len(matches), err)
	}
	sort.Strings(matches)
	names := make([]string, 0, len(matches))
	for _, match := range matches {
		names = append(names, filepath.Base(match))
	}
	return names
}

func openLedgerSQLite(path string) (*gorm.DB, error) {
	return gorm.Open(sqlite.Dialector{DriverName: "sqlite", DSN: path + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)"}, &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
}

// ledgerTestDB gives the test its own copy of the migrated database.
func ledgerTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	body, err := os.ReadFile(ledgerMigratedSnapshot(t))
	require.NoError(t, err)
	path := filepath.Join(t.TempDir(), "ledger.db")
	require.NoError(t, os.WriteFile(path, body, 0o600))
	db, err := openLedgerSQLite(path)
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	return db
}

// ledgerModelService is a model registry that answers with one fixed model.
type ledgerModelService struct {
	interfaces.ModelService
	models    []*types.Model
	chatModel chat.Chat
}

func (s *ledgerModelService) ListModels(context.Context) ([]*types.Model, error) {
	return s.models, nil
}

func (s *ledgerModelService) GetChatModel(context.Context, string) (chat.Chat, error) {
	return s.chatModel, nil
}

// ledgerChat answers with a fixed response: no provider is ever contacted.
type ledgerChat struct {
	response *types.ChatResponse
	err      error
	options  *chat.ChatOptions
}

func (c *ledgerChat) Chat(_ context.Context, _ []chat.Message, options *chat.ChatOptions) (*types.ChatResponse, error) {
	c.options = options
	return c.response, c.err
}

func (c *ledgerChat) ChatStream(context.Context, []chat.Message, *chat.ChatOptions) (<-chan types.StreamResponse, error) {
	return nil, nil
}

func (c *ledgerChat) GetModelName() string { return "ledger-model" }

func (c *ledgerChat) GetModelID() string { return "ledger-model-id" }

func ledgerRegisteredModel(provider string) *types.Model {
	return &types.Model{
		ID: "ledger-model-id", Name: "ledger-model", IsDefault: true,
		Type: types.ModelTypeKnowledgeQA, Source: types.ModelSourceRemote, Status: types.ModelStatusActive,
		Parameters: types.ModelParameters{Provider: provider, BaseURL: "https://example.invalid"},
	}
}

func ledgerModelContext() core.AgentContextPackage {
	return core.AgentContextPackage{
		ProjectID: "ledger-project", ProjectVersion: 1, ContextRevision: 1,
		AuthorizationState: "allowed", SourceRefs: []core.AgentSourceRef{}, CorrelationID: "ledger-correlation",
	}
}

// workspaceHostAgentModel builds the model adapter the way the composition
// root does: with the ledger installed, or without one.
func workspaceHostAgentModel(service interfaces.ModelService, ledger core.ModelCallLedger) core.AgentModel {
	if ledger == nil {
		return workspace.NewHostAgentModel(service)
	}
	return workspace.NewHostAgentModel(service, workspace.WithModelCallLedger(ledger))
}

// ledgerCallEntry is one call row with every fact a reader would ask for.
func ledgerCallEntry() core.ModelCallLedgerEntry {
	return core.ModelCallLedgerEntry{
		TenantID: 7, ProjectID: "ledger-project", Provider: "generic", ModelID: "ledger-model-id",
		RunMode: "real", ParametersJSON: `{"temperature":0.2,"max_completion_tokens":2048}`,
		InputTokens: 100, OutputTokens: 30, ReservedNanoUSD: 4000, ActualNanoUSD: 3600,
		DurationMS: 42, Status: core.ModelCallLedgerStatusSucceeded,
		SourceKind: core.ModelCallLedgerSourceAgentTurn, SourceID: "ledger-correlation",
	}
}

// One model call leaves exactly one row, and that row carries every fact the
// reader needs: who answered, with which model, with which parameters, for
// how much budget, how many tokens, how long, and with which status.
func TestModelCallLedgerRecordsOneCallRowWithProviderParametersAndBudget(t *testing.T) {
	db := ledgerTestDB(t)
	ledger := core.NewGORMModelCallLedger(db)
	require.NotNil(t, ledger)

	entry := ledgerCallEntry()
	require.NoError(t, ledger.RecordModelCall(context.Background(), entry))

	rows, err := ledger.ModelCallEntriesBySource(context.Background(), 7, core.ModelCallLedgerSourceAgentTurn, "ledger-correlation")
	require.NoError(t, err)
	require.Len(t, rows, 1)

	row := rows[0]
	require.NotEmpty(t, row.ID)
	require.Equal(t, core.ModelCallLedgerKindCall, row.Kind)
	require.Equal(t, uint64(7), row.TenantID)
	require.Equal(t, "ledger-project", row.ProjectID)
	require.Equal(t, "generic", row.Provider)
	require.Equal(t, "ledger-model-id", row.ModelID)
	require.Equal(t, "real", row.RunMode)
	require.Contains(t, row.ParametersJSON, `"temperature":0.2`)
	require.Equal(t, 100, row.InputTokens)
	require.Equal(t, 30, row.OutputTokens)
	require.Equal(t, int64(4000), row.ReservedNanoUSD)
	require.Equal(t, int64(3600), row.ActualNanoUSD)
	require.Equal(t, int64(42), row.DurationMS)
	require.Equal(t, core.ModelCallLedgerStatusSucceeded, row.Status)
	require.Equal(t, core.ModelCallLedgerSourceAgentTurn, row.SourceKind)
	require.Equal(t, "ledger-correlation", row.SourceID)
	require.False(t, row.CreatedAt.IsZero())

	// A call row carries no switch facts: the two row kinds are told apart by
	// their own columns, not by a reader's guess.
	require.Empty(t, row.SwitchedFromModelID)
	require.Empty(t, row.SwitchReason)
	require.Empty(t, row.AffectedStepsJSON)
}

// A failed call is recorded as a failure with its error code. It is never
// recorded as a success: a retry must not be able to look like a first
// success.
func TestModelCallLedgerRecordsAFailedCallWithItsErrorCode(t *testing.T) {
	db := ledgerTestDB(t)
	ledger := core.NewGORMModelCallLedger(db)

	entry := ledgerCallEntry()
	entry.Status = core.ModelCallLedgerStatusFailed
	entry.ErrorCode = "provider_failure"
	entry.InputTokens = 0
	entry.OutputTokens = 0
	require.NoError(t, ledger.RecordModelCall(context.Background(), entry))

	rows, err := ledger.ModelCallEntriesBySource(context.Background(), 7, core.ModelCallLedgerSourceAgentTurn, "ledger-correlation")
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, core.ModelCallLedgerStatusFailed, rows[0].Status)
	require.Equal(t, "provider_failure", rows[0].ErrorCode)
	require.NotEqual(t, core.ModelCallLedgerStatusSucceeded, rows[0].Status)
}

// One model switch leaves one row that says where the model came from, why it
// changed and which steps it affected.
func TestModelCallLedgerRecordsAModelSwitchWithItsReasonAndAffectedSteps(t *testing.T) {
	db := ledgerTestDB(t)
	ledger := core.NewGORMModelCallLedger(db)

	steps := `[{"step":"draft_section","from_model":"ledger-model-id","to_model":"ledger-model-next"}]`
	switchEntry := core.ModelCallLedgerEntry{
		TenantID: 7, ProjectID: "ledger-project", Kind: core.ModelCallLedgerKindSwitch,
		Provider: "generic", ModelID: "ledger-model-next", RunMode: "real",
		Status:     core.ModelCallLedgerStatusSucceeded,
		SourceKind: core.ModelCallLedgerSourceGenerationRun, SourceID: "ledger-run",
		SwitchedFromModelID: "ledger-model-id", SwitchReason: "provider_limit",
		AffectedStepsJSON: steps,
	}
	require.NoError(t, ledger.RecordModelSwitch(context.Background(), switchEntry))

	rows, err := ledger.ModelSwitchEntriesByProject(context.Background(), 7, "ledger-project")
	require.NoError(t, err)
	require.Len(t, rows, 1)

	row := rows[0]
	require.Equal(t, core.ModelCallLedgerKindSwitch, row.Kind)
	require.Equal(t, "ledger-model-id", row.SwitchedFromModelID)
	require.Equal(t, "provider_limit", row.SwitchReason)
	require.Equal(t, "ledger-model-next", row.ModelID)
	require.JSONEq(t, steps, row.AffectedStepsJSON)

	// The switch is not a call: the call rows of the same project do not carry
	// it, and the switch row does not answer as one.
	calls, err := ledger.ModelCallEntriesBySource(context.Background(), 7, core.ModelCallLedgerSourceAgentTurn, "ledger-correlation")
	require.NoError(t, err)
	require.Empty(t, calls)
}

// The rows can be filtered by run mode, so a real sample can be told from a
// mock one mechanically -- with plain SQL, the way the evidence side reads it.
func TestModelCallLedgerSeparatesRealSamplesFromMockAndFakeModelSamples(t *testing.T) {
	db := ledgerTestDB(t)
	ledger := core.NewGORMModelCallLedger(db)

	for _, runMode := range []string{"real", "mock", "real_api_fake_model"} {
		entry := ledgerCallEntry()
		entry.RunMode = runMode
		entry.SourceID = "source-" + runMode
		require.NoError(t, ledger.RecordModelCall(context.Background(), entry))
	}

	var realRows []struct {
		ID      string
		RunMode string
	}
	require.NoError(t, db.Raw("SELECT id, run_mode FROM lingdoc_model_call_ledger WHERE tenant_id = ? AND run_mode = ?", 7, "real").Scan(&realRows).Error)
	require.Len(t, realRows, 1)
	require.Equal(t, "real", realRows[0].RunMode)

	var mockCount int64
	require.NoError(t, db.Raw("SELECT COUNT(*) FROM lingdoc_model_call_ledger WHERE run_mode IN (?, ?)", "mock", "real_api_fake_model").Scan(&mockCount).Error)
	require.Equal(t, int64(2), mockCount)

	// The ledger's own reader answers the same question.
	realEntries, err := ledger.ModelCallEntriesBySource(context.Background(), 7, core.ModelCallLedgerSourceAgentTurn, "source-real")
	require.NoError(t, err)
	require.Len(t, realEntries, 1)
	require.Equal(t, "real", realEntries[0].RunMode)

	mockEntries, err := ledger.ModelCallEntriesBySource(context.Background(), 7, core.ModelCallLedgerSourceAgentTurn, "source-mock")
	require.NoError(t, err)
	require.Len(t, mockEntries, 1)
	require.Equal(t, "mock", mockEntries[0].RunMode)
}

// A row points at the product action that caused it, so one action's cost can
// be read back as one question.
func TestModelCallLedgerLinksRowsToTheProductActionThatCausedThem(t *testing.T) {
	db := ledgerTestDB(t)
	ledger := core.NewGORMModelCallLedger(db)

	first := ledgerCallEntry()
	first.SourceID = "turn-1"
	require.NoError(t, ledger.RecordModelCall(context.Background(), first))

	second := ledgerCallEntry()
	second.SourceID = "turn-2"
	require.NoError(t, ledger.RecordModelCall(context.Background(), second))

	rows, err := ledger.ModelCallEntriesBySource(context.Background(), 7, core.ModelCallLedgerSourceAgentTurn, "turn-1")
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, "turn-1", rows[0].SourceID)

	// Another tenant's row is not reachable through this tenant's reference.
	other := ledgerCallEntry()
	other.TenantID = 8
	other.SourceID = "turn-1"
	require.NoError(t, ledger.RecordModelCall(context.Background(), other))

	rows, err = ledger.ModelCallEntriesBySource(context.Background(), 7, core.ModelCallLedgerSourceAgentTurn, "turn-1")
	require.NoError(t, err)
	require.Len(t, rows, 1)
}

// A row that does not match its own kind is refused: a call row that also
// carries switch facts is a row nobody can read, and a switch row without a
// reason is not a switch.
func TestModelCallLedgerRefusesARowThatDoesNotMatchItsKind(t *testing.T) {
	db := ledgerTestDB(t)
	ledger := core.NewGORMModelCallLedger(db)

	callWithSwitchFacts := ledgerCallEntry()
	callWithSwitchFacts.SwitchReason = "provider_limit"
	require.ErrorIs(t, ledger.RecordModelCall(context.Background(), callWithSwitchFacts), core.ErrInvalidRequest)

	switchWithoutReason := core.ModelCallLedgerEntry{
		TenantID: 7, ProjectID: "ledger-project", Status: core.ModelCallLedgerStatusSucceeded,
		SourceKind: core.ModelCallLedgerSourceGenerationRun, SourceID: "ledger-run",
		SwitchedFromModelID: "ledger-model-id", AffectedStepsJSON: "[]",
	}
	require.ErrorIs(t, ledger.RecordModelSwitch(context.Background(), switchWithoutReason), core.ErrInvalidRequest)

	// A row without a source cannot be attributed to any product action.
	withoutSource := ledgerCallEntry()
	withoutSource.SourceKind = ""
	require.ErrorIs(t, ledger.RecordModelCall(context.Background(), withoutSource), core.ErrInvalidRequest)

	var count int64
	require.NoError(t, db.Raw("SELECT COUNT(*) FROM lingdoc_model_call_ledger").Scan(&count).Error)
	require.Equal(t, int64(0), count)
}

// The application seam: one call to the model adapter leaves exactly one
// ledger row, with the provider and the parameters that were actually sent.
func TestHostAgentModelRecordsOneLedgerRowPerModelCall(t *testing.T) {
	t.Setenv(acceptancebudget.ProfileEnv, "")
	t.Setenv(acceptancebudget.LedgerEnv, "")

	db := ledgerTestDB(t)
	ledger := core.NewGORMModelCallLedger(db)
	chatStub := &ledgerChat{response: &types.ChatResponse{
		Content: `{"reply":"合成回复"}`, FinishReason: "stop",
		Usage: types.TokenUsage{PromptTokens: 100, CompletionTokens: 30, TotalTokens: 130},
	}}
	service := &ledgerModelService{models: []*types.Model{ledgerRegisteredModel("generic")}, chatModel: chatStub}

	host := workspaceHostAgentModel(service, ledger)
	reply, err := host.Reply(context.Background(), core.Actor{TenantID: 7, UserID: "owner"}, core.ModelRequest{
		Agent: core.AgentTaskAssistant, Purpose: core.AgentPurposeConversation,
		Context: ledgerModelContext(), UserInput: "解释当前任务",
	})
	require.NoError(t, err)
	require.Equal(t, "合成回复", reply.Content)

	rows, err := ledger.ModelCallEntriesBySource(context.Background(), 7, core.ModelCallLedgerSourceAgentTurn, "ledger-correlation")
	require.NoError(t, err)
	require.Len(t, rows, 1)

	row := rows[0]
	require.Equal(t, core.ModelCallLedgerKindCall, row.Kind)
	require.Equal(t, core.ModelCallLedgerStatusSucceeded, row.Status)
	require.Equal(t, "generic", row.Provider)
	require.Equal(t, "ledger-model-id", row.ModelID)
	require.Equal(t, "real", row.RunMode)
	require.Equal(t, 100, row.InputTokens)
	require.Equal(t, 30, row.OutputTokens)
	require.GreaterOrEqual(t, row.DurationMS, int64(0))
	require.Equal(t, core.ModelCallLedgerSourceAgentTurn, row.SourceKind)
	require.Equal(t, "ledger-correlation", row.SourceID)

	// The parameters are the ones that were sent, not a second configuration.
	require.Contains(t, row.ParametersJSON, `"temperature":0.2`)
	require.Contains(t, row.ParametersJSON, `"max_completion_tokens":2048`)
	require.NotNil(t, chatStub.options)
}

// The application seam for the failure path: a call the provider refused is
// recorded as a failure with its error code.
func TestHostAgentModelRecordsAFailedCallAsAFailure(t *testing.T) {
	t.Setenv(acceptancebudget.ProfileEnv, "")
	t.Setenv(acceptancebudget.LedgerEnv, "")

	db := ledgerTestDB(t)
	ledger := core.NewGORMModelCallLedger(db)
	chatStub := &ledgerChat{err: errors.New("provider is down")}
	service := &ledgerModelService{models: []*types.Model{ledgerRegisteredModel("generic")}, chatModel: chatStub}

	host := workspaceHostAgentModel(service, ledger)
	_, err := host.Reply(context.Background(), core.Actor{TenantID: 7, UserID: "owner"}, core.ModelRequest{
		Agent: core.AgentTaskAssistant, Purpose: core.AgentPurposeConversation,
		Context: ledgerModelContext(), UserInput: "解释当前任务",
	})
	require.Error(t, err)

	rows, err := ledger.ModelCallEntriesBySource(context.Background(), 7, core.ModelCallLedgerSourceAgentTurn, "ledger-correlation")
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, core.ModelCallLedgerStatusFailed, rows[0].Status)
	require.Equal(t, "provider_failure", rows[0].ErrorCode)
	require.NotEqual(t, core.ModelCallLedgerStatusSucceeded, rows[0].Status)
}

// The ledger is optional: without it the adapter answers exactly as before.
// The row is what makes the difference between a demo run and a real one, so
// the adapter must not start refusing turns when nobody asked for the ledger.
func TestHostAgentModelWithoutALedgerStillAnswers(t *testing.T) {
	t.Setenv(acceptancebudget.ProfileEnv, "")
	t.Setenv(acceptancebudget.LedgerEnv, "")

	chatStub := &ledgerChat{response: &types.ChatResponse{
		Content: `{"reply":"合成回复"}`, FinishReason: "stop",
		Usage: types.TokenUsage{PromptTokens: 100, CompletionTokens: 30},
	}}
	service := &ledgerModelService{models: []*types.Model{ledgerRegisteredModel("generic")}, chatModel: chatStub}

	host := workspaceHostAgentModel(service, nil)
	reply, err := host.Reply(context.Background(), core.Actor{TenantID: 7, UserID: "owner"}, core.ModelRequest{
		Agent: core.AgentTaskAssistant, Purpose: core.AgentPurposeConversation,
		Context: ledgerModelContext(), UserInput: "解释当前任务",
	})
	require.NoError(t, err)
	require.Equal(t, "合成回复", reply.Content)
}

// The two dialects carry the same columns: the evidence side reads the table
// by column name, so a column that exists on one side only is a silent hole.
// The list below is the table contract both dialects have to match.
func TestModelCallLedgerMigrationsCarryTheSameColumnsInBothDialects(t *testing.T) {
	expected := []string{
		"id", "tenant_id", "project_id", "kind", "provider", "model_id", "run_mode",
		"parameters_json", "input_tokens", "output_tokens", "reserved_nano_usd",
		"actual_nano_usd", "duration_ms", "status", "error_code", "source_kind",
		"source_id", "switched_from_model_id", "switch_reason", "affected_steps_json",
		"created_at",
	}
	sqliteColumns := ledgerMigrationColumns(t, filepath.Join("..", "..", "..", "migrations", "sqlite", "000054_lingdoc_model_call_ledger.up.sql"))
	postgresColumns := ledgerMigrationColumns(t, filepath.Join("..", "..", "..", "migrations", "versioned", "000133_lingdoc_model_call_ledger.up.sql"))
	require.Equal(t, expected, sqliteColumns)
	require.Equal(t, expected, postgresColumns)

	// The table the evidence side reads has the name the contract names.
	db := ledgerTestDB(t)
	var count int64
	require.NoError(t, db.Raw("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?", "lingdoc_model_call_ledger").Scan(&count).Error)
	require.Equal(t, int64(1), count)
}

var ledgerColumnPattern = regexp.MustCompile(`(?m)^\s{4}([a-z_]+)\s+(?:TEXT|VARCHAR|UUID|BIGINT|INTEGER|JSONB|DATETIME|TIMESTAMPTZ)`)

// ledgerMigrationColumns reads the column names out of a CREATE TABLE block.
func ledgerMigrationColumns(t *testing.T, path string) []string {
	t.Helper()
	body, err := os.ReadFile(path)
	require.NoError(t, err)
	text := string(body)
	begin := strings.Index(text, "CREATE TABLE")
	require.NotEqual(t, -1, begin, path)
	remainder := text[begin:]
	end := strings.Index(remainder, ");")
	require.NotEqual(t, -1, end, path)
	block := remainder[:end]
	columns := []string{}
	for _, match := range ledgerColumnPattern.FindAllStringSubmatch(block, -1) {
		columns = append(columns, match[1])
	}
	return columns
}
