package workspacecore

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type quotaPolicyFixture struct{ value ResearchQuotaPolicy }

func (p *quotaPolicyFixture) ResearchQuotaPolicy(context.Context, Actor, string) (ResearchQuotaPolicy, error) {
	return p.value, nil
}

func quotaFixture(t *testing.T) (agentFixture, ResearchTask, *ResearchQuotaLedger, *quotaPolicyFixture) {
	t.Helper()
	f := newAgentFixture(t, true)
	db, err := f.svc.testDB().DB()
	require.NoError(t, err)
	db.SetMaxOpenConns(1) // production SQLite's one-writer pool
	task := f.task(t)
	now := time.Now().UTC()
	policy := &quotaPolicyFixture{ResearchQuotaPolicy{Version: "synthetic-policy-v1", ApprovedBy: "synthetic-fixture-only", WindowID: "synthetic-window-1", StartsAt: now.Add(-time.Hour), EndsAt: now.Add(time.Hour), Limit: ResearchQuotaAmount{Searches: 6, Results: 120, ModelCalls: 2}}}
	ledger := NewResearchQuotaLedger(f.svc, policy)
	return f, task, ledger, policy
}

// Persist a pending operation to exercise the ledger independently of a search
// provider. The separate application test below uses the real RunResearchTask.
func quotaPendingRun(t *testing.T, f agentFixture, task ResearchTask, id string) ResearchTask {
	t.Helper()
	task.ID, task.Version, task.Status, task.LatestRunID = "quota-task-"+id, 1, "running", id
	run := ResearchRun{ID: id, ProjectID: f.project.ID, TaskID: task.ID, TaskVersion: 1, Version: 1, Basis: task.Basis, Status: "running", CreatedBy: f.owner.UserID, Usage: newResearchUsageLedger()}
	require.NoError(t, f.svc.repository.Transaction(context.Background(), TransactionOptions{}, func(tx Transaction) error {
		if err := storeStoredObject(tx, StoredObject{}, f.project.ID, ResearchTaskKind, task.ID, 1, task); err != nil {
			return err
		}
		return storeStoredObject(tx, StoredObject{}, f.project.ID, ResearchRunKind, id, 1, run)
	}))
	return task
}

func quotaProgress(t *testing.T, f agentFixture, id, status string, searches, results int) ResearchUsageLedger {
	t.Helper()
	usage := newResearchUsageLedger()
	usage.SearchesUsed, usage.ResultsUsed = searches, results
	require.NoError(t, f.svc.repository.Transaction(context.Background(), TransactionOptions{}, func(tx Transaction) error {
		run, old, err := loadStoredObject[ResearchRun](tx, f.project.ID, ResearchRunKind, id)
		if err != nil {
			return err
		}
		run.Version++
		run.Status, run.Usage = status, usage
		return storeStoredObject(tx, old, f.project.ID, ResearchRunKind, id, run.Version, run)
	}))
	return usage
}

func TestResearchQuotaLedgerRestartPauseSettleAndReplay(t *testing.T) {
	f, task, ledger, policy := quotaFixture(t)
	task = quotaPendingRun(t, f, task, "restart")
	ctx := context.Background()
	require.NoError(t, ledger.Reserve(ctx, f.owner, f.project.ID, "restart", task))
	require.NoError(t, ledger.Reserve(ctx, f.owner, f.project.ID, "restart", task))
	usage := quotaProgress(t, f, "restart", "paused", 1, 1)
	require.NoError(t, ledger.Settle(ctx, f.owner, f.project.ID, "restart", usage))
	snapshot, err := ledger.Snapshot(ctx, f.owner, f.project.ID)
	require.NoError(t, err)
	require.Equal(t, ResearchQuotaAmount{Searches: 2, Results: 40}, snapshot.Charged)
	require.Equal(t, ResearchQuotaAmount{Searches: 1, Results: 1}, snapshot.Measured)
	// Re-open the same file with a new service/ledger; no process counter survives.
	restarted := NewResearchQuotaLedger(testStoreWithPolicy(t, f.path, f.policy), policy)
	require.NoError(t, restarted.Reserve(ctx, f.owner, f.project.ID, "restart", task))
	usage = quotaProgress(t, f, "restart", "completed", 2, 3)
	require.NoError(t, restarted.Settle(ctx, f.owner, f.project.ID, "restart", usage))
	require.NoError(t, restarted.Settle(ctx, f.owner, f.project.ID, "restart", usage))
	snapshot, err = ledger.Snapshot(ctx, f.owner, f.project.ID)
	require.NoError(t, err)
	require.Equal(t, ResearchQuotaAmount{Searches: 2, Results: 3}, snapshot.Charged)
	require.Equal(t, snapshot.Charged, snapshot.Measured)
	require.ErrorIs(t, ledger.Reserve(ctx, f.owner, f.project.ID, "restart", task), ErrInvalidState)
	var count int64
	require.NoError(t, f.svc.testDB().Table("lingdoc_g2_object_versions").Where("project_id = ? AND kind = ?", f.project.ID, researchQuotaEntryKind).Count(&count).Error)
	require.EqualValues(t, 3, count, "only reserve, paused measurement and final settlement may append")
}

func TestResearchQuotaLedgerConcurrentReservationsCannotOverspend(t *testing.T) {
	f, task, ledger, policy := quotaFixture(t)
	policy.value.Limit = ResearchQuotaAmount{Searches: 2, Results: 40}
	first := quotaPendingRun(t, f, task, "first")
	second := quotaPendingRun(t, f, task, "second")
	start := make(chan struct{})
	var wait sync.WaitGroup
	errorsFound := make(chan error, 2)
	for i, plan := range []ResearchTask{first, second} {
		id := []string{"first", "second"}[i]
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			errorsFound <- ledger.Reserve(context.Background(), f.owner, f.project.ID, id, plan)
		}()
	}
	close(start)
	wait.Wait()
	close(errorsFound)
	winners, refused := 0, 0
	for err := range errorsFound {
		if err == nil {
			winners++
		} else if errors.Is(err, ErrResearchQuotaExhausted) {
			refused++
		} else {
			t.Fatal(err)
		}
	}
	require.Equal(t, 1, winners)
	require.Equal(t, 1, refused)
	snapshot, err := ledger.Snapshot(context.Background(), f.owner, f.project.ID)
	require.NoError(t, err)
	require.Equal(t, policy.value.Limit, snapshot.Charged)
}

func TestResearchQuotaLedgerUnknownOrRegressingUsageRetainsReservation(t *testing.T) {
	f, task, ledger, _ := quotaFixture(t)
	task = quotaPendingRun(t, f, task, "unknown")
	ctx := context.Background()
	require.NoError(t, ledger.Reserve(ctx, f.owner, f.project.ID, "unknown", task))
	usage := newResearchUsageLedger()
	usage.SearchesUsed = 1
	require.ErrorIs(t, ledger.Settle(ctx, f.owner, f.project.ID, "unknown", usage), ErrResearchBudgetSettlementUnknown)
	snapshot, err := ledger.Snapshot(ctx, f.owner, f.project.ID)
	require.NoError(t, err)
	require.Equal(t, ResearchQuotaAmount{Searches: 2, Results: 40}, snapshot.Charged)
	require.Equal(t, ResearchQuotaAmount{}, snapshot.Measured)
	usage = quotaProgress(t, f, "unknown", "paused", 1, 1)
	require.NoError(t, ledger.Settle(ctx, f.owner, f.project.ID, "unknown", usage))
	usage = quotaProgress(t, f, "unknown", "completed", 0, 0)
	require.ErrorIs(t, ledger.Settle(ctx, f.owner, f.project.ID, "unknown", usage), ErrVersionConflict)
	snapshot, err = ledger.Snapshot(ctx, f.owner, f.project.ID)
	require.NoError(t, err)
	require.Equal(t, ResearchQuotaAmount{Searches: 1, Results: 1}, snapshot.Measured)
	require.Equal(t, ResearchQuotaAmount{Searches: 2, Results: 40}, snapshot.Charged)
}

func TestResearchQuotaLedgerPolicyAndWindowsCannotResetExistingUsage(t *testing.T) {
	f, task, ledger, policy := quotaFixture(t)
	first := quotaPendingRun(t, f, task, "window-1")
	second := quotaPendingRun(t, f, task, "window-2")
	ctx := context.Background()
	require.NoError(t, ledger.Reserve(ctx, f.owner, f.project.ID, "window-1", first))
	original := policy.value
	policy.value.Version = "synthetic-edited"
	require.ErrorIs(t, ledger.Reserve(ctx, f.owner, f.project.ID, "window-1", first), ErrVersionConflict)
	require.ErrorIs(t, ledger.Reserve(ctx, f.owner, f.project.ID, "window-2", second), ErrVersionConflict)
	policy.value = original
	policy.value.WindowID = "overlapping"
	require.ErrorIs(t, ledger.Reserve(ctx, f.owner, f.project.ID, "window-2", second), ErrVersionConflict)
	policy.value = original
	ledger.now = func() time.Time { return original.EndsAt.Add(time.Minute) }
	require.ErrorIs(t, ledger.Reserve(ctx, f.owner, f.project.ID, "window-2", second), ErrResearchBudgetUnavailable)
	policy.value.WindowID, policy.value.StartsAt, policy.value.EndsAt = "window-next", original.EndsAt, original.EndsAt.Add(time.Hour)
	require.ErrorIs(t, ledger.Reserve(ctx, f.owner, f.project.ID, "window-1", first), ErrVersionConflict)
	require.NoError(t, ledger.Reserve(ctx, f.owner, f.project.ID, "window-2", second))
	usage := quotaProgress(t, f, "window-1", "completed", 1, 1)
	require.NoError(t, ledger.Settle(ctx, f.owner, f.project.ID, "window-1", usage), "late settlement belongs to the original window")
	snapshot, err := ledger.Snapshot(ctx, f.owner, f.project.ID)
	require.NoError(t, err)
	require.Equal(t, ResearchQuotaAmount{Searches: 2, Results: 40}, snapshot.Charged)
	require.Equal(t, ResearchQuotaAmount{}, snapshot.Measured)
}

func TestResearchQuotaLedgerAuditFailureAndPermissionsLeaveNoRows(t *testing.T) {
	f, task, ledger, _ := quotaFixture(t)
	task = quotaPendingRun(t, f, task, "atomic")
	ctx := context.Background()
	other := f.owner
	other.TenantID++
	require.Error(t, ledger.Reserve(ctx, other, f.project.ID, "atomic", task))
	require.Error(t, ledger.Reserve(ctx, f.owner, f.project.ID, "missing", task))
	require.NoError(t, f.svc.testDB().Exec("CREATE TRIGGER quota_audit_failure BEFORE INSERT ON lingdoc_project_audits WHEN NEW.action = 'g2.research_budget.reserved' BEGIN SELECT RAISE(ABORT, 'synthetic audit failure'); END").Error)
	require.Error(t, ledger.Reserve(ctx, f.owner, f.project.ID, "atomic", task))
	var rows int64
	require.NoError(t, f.svc.testDB().Table("lingdoc_g2_objects").Where("project_id = ? AND kind IN ?", f.project.ID, []string{researchQuotaWindowKind, researchQuotaEntryKind, researchQuotaHeadKind}).Count(&rows).Error)
	require.Zero(t, rows, "reservation, window, head and versions must roll back together")
	require.NoError(t, f.svc.testDB().Table("lingdoc_g2_object_versions").Where("project_id = ? AND kind IN ?", f.project.ID, []string{researchQuotaWindowKind, researchQuotaEntryKind, researchQuotaHeadKind}).Count(&rows).Error)
	require.Zero(t, rows, "audit failure must not retain historical quota versions")
	require.NoError(t, f.svc.testDB().Exec("DROP TRIGGER quota_audit_failure").Error)
	require.NoError(t, ledger.Reserve(ctx, f.owner, f.project.ID, "atomic", task))
	require.NoError(t, f.svc.testDB().Exec("DELETE FROM tenant_members WHERE tenant_id = ? AND user_id = ?", f.owner.TenantID, f.owner.UserID).Error)
	require.Error(t, ledger.Reserve(ctx, f.owner, f.project.ID, "atomic", task), "replay must recheck current membership")
}

func TestResearchQuotaLedgerRejectsChangedPlanAndInvalidPolicy(t *testing.T) {
	f, task, ledger, policy := quotaFixture(t)
	task = quotaPendingRun(t, f, task, "policy-guard")
	ctx := context.Background()
	original := policy.value
	for _, alter := range []func(*ResearchQuotaPolicy){
		func(p *ResearchQuotaPolicy) { p.ApprovedBy = "" },
		func(p *ResearchQuotaPolicy) { p.Limit.Results = -1 },
		func(p *ResearchQuotaPolicy) { p.StartsAt = time.Now().Add(time.Hour) },
		func(p *ResearchQuotaPolicy) { p.EndsAt = p.StartsAt },
	} {
		policy.value = original
		alter(&policy.value)
		require.ErrorIs(t, ledger.Reserve(ctx, f.owner, f.project.ID, "policy-guard", task), ErrResearchBudgetUnavailable)
	}
	policy.value = original
	policy.value.Limit = ResearchQuotaAmount{}
	require.ErrorIs(t, ledger.Reserve(ctx, f.owner, f.project.ID, "policy-guard", task), ErrResearchQuotaExhausted)
	policy.value = original
	require.NoError(t, ledger.Reserve(ctx, f.owner, f.project.ID, "policy-guard", task))
	changed := task
	changed.Queries = []string{"altered same operation"}
	require.ErrorIs(t, ledger.Reserve(ctx, f.owner, f.project.ID, "policy-guard", changed), ErrVersionConflict)
	snapshot, err := ledger.Snapshot(ctx, f.owner, f.project.ID)
	require.NoError(t, err)
	require.Equal(t, ResearchQuotaAmount{Searches: 2, Results: 40}, snapshot.Charged)
}

func TestResearchQuotaLedgerModelFailuresStillCountAndPolicyMissingFailsClosed(t *testing.T) {
	f, _, ledger, policy := quotaFixture(t)
	policy.value.Limit.ModelCalls = 1
	ctx := context.Background()
	for _, id := range []string{"one", "two"} {
		require.NoError(t, f.svc.repository.Transaction(ctx, TransactionOptions{}, func(tx Transaction) error {
			turn := AgentTurn{ID: id, ProjectID: f.project.ID, Version: 1, Agent: AgentResearch, CreatedBy: f.owner.UserID, Status: "running"}
			return storeStoredObject(tx, StoredObject{}, f.project.ID, AgentTurnKind, id, 1, turn)
		}))
	}
	require.ErrorIs(t, NewResearchQuotaLedger(f.svc, nil).ReserveModelCall(ctx, f.owner, f.project.ID, "one"), ErrResearchBudgetUnavailable)
	require.NoError(t, ledger.ReserveModelCall(ctx, f.owner, f.project.ID, "one"))
	require.NoError(t, ledger.ReserveModelCall(ctx, f.owner, f.project.ID, "one"))
	usage := ResearchModelCallUsage{ModelCallsUsed: 1, InputTokens: 10, OutputTokens: 2, Outcome: "failed"}
	require.NoError(t, ledger.SettleModelCall(ctx, f.owner, f.project.ID, "one", usage))
	require.NoError(t, ledger.SettleModelCall(ctx, f.owner, f.project.ID, "one", usage))
	require.ErrorIs(t, ledger.ReserveModelCall(ctx, f.owner, f.project.ID, "two"), ErrResearchQuotaExhausted)
	usage.Outcome = "succeeded"
	require.ErrorIs(t, ledger.SettleModelCall(ctx, f.owner, f.project.ID, "one", usage), ErrVersionConflict)
	snapshot, err := ledger.Snapshot(ctx, f.owner, f.project.ID)
	require.NoError(t, err)
	require.EqualValues(t, 1, snapshot.Charged.ModelCalls)
	require.EqualValues(t, 1, snapshot.Measured.ModelCalls)
}

type quotaSearcherFixture struct {
	*testSearcher
	ledger *ResearchQuotaLedger
}

func (s *quotaSearcherFixture) ReserveResearchRun(ctx context.Context, actor Actor, project, run string, task ResearchTask) error {
	return s.ledger.Reserve(ctx, actor, project, run, task)
}
func (s *quotaSearcherFixture) SettleResearchRun(ctx context.Context, actor Actor, project, run string, usage ResearchUsageLedger) error {
	return s.ledger.Settle(ctx, actor, project, run, usage)
}

func (s *quotaSearcherFixture) ReserveResearchModelCall(ctx context.Context, actor Actor, project, turn string) error {
	return s.ledger.ReserveModelCall(ctx, actor, project, turn)
}

func (s *quotaSearcherFixture) SettleResearchModelCall(ctx context.Context, actor Actor, project, turn string, usage ResearchModelCallUsage) error {
	return s.ledger.SettleModelCall(ctx, actor, project, turn, usage)
}

func TestResearchQuotaLedgerApplicationModelTurnDoesNotBypassQuota(t *testing.T) {
	f, _, ledger, policy := quotaFixture(t)
	policy.value.Limit.ModelCalls = 1
	f.svc.agentRuntime.Research = &quotaSearcherFixture{testSearcher: f.search, ledger: ledger}
	ctx := context.Background()
	raw, _, _, err := f.svc.CreateAgentSession(ctx, f.owner, f.project.ID, AgentResearch, "quota-agent-session", f.expectation(0))
	session := decodeAgentJSON[AgentSession](t, raw, err)
	input := AgentTurnInput{AgentContextInput: AgentContextInput{VersionExpectation: f.expectation(0), SourceIDs: []string{}, CardRefs: []VersionRef{}}, UserInput: "synthetic query plan"}
	raw, _, _, err = f.svc.SendAgentTurn(ctx, f.owner, f.project.ID, session.ID, "quota-agent-first", input)
	turn := decodeAgentJSON[AgentTurn](t, raw, err)
	require.NotEqual(t, "research_budget_exhausted", turn.FailureCode)
	require.Equal(t, 1, f.model.calls)
	_, _, replay, err := f.svc.SendAgentTurn(ctx, f.owner, f.project.ID, session.ID, "quota-agent-first", input)
	require.NoError(t, err)
	require.True(t, replay)
	raw, _, _, err = f.svc.SendAgentTurn(ctx, f.owner, f.project.ID, session.ID, "quota-agent-next", input)
	turn = decodeAgentJSON[AgentTurn](t, raw, err)
	require.Equal(t, "research_budget_exhausted", turn.FailureCode)
	require.Equal(t, 1, f.model.calls, "the second billable call must never reach the model")
	snapshot, err := ledger.Snapshot(ctx, f.owner, f.project.ID)
	require.NoError(t, err)
	require.EqualValues(t, 1, snapshot.Measured.ModelCalls)
}

func TestResearchQuotaLedgerActualApplicationRunAndReplay(t *testing.T) {
	f, task, ledger, _ := quotaFixture(t)
	f.svc.agentRuntime.Research = &quotaSearcherFixture{testSearcher: f.search, ledger: ledger}
	input := RunResearchTaskInput{VersionExpectation: f.expectation(task.Version)}
	raw, _, replay, err := f.svc.RunResearchTask(context.Background(), f.owner, f.project.ID, task.ID, "quota-application-run", input)
	run := decodeAgentJSON[ResearchRun](t, raw, err)
	require.False(t, replay)
	require.Equal(t, "completed", run.Status)
	require.Equal(t, 2, f.search.calls)
	_, _, replay, err = f.svc.RunResearchTask(context.Background(), f.owner, f.project.ID, task.ID, "quota-application-run", input)
	require.NoError(t, err)
	require.True(t, replay)
	require.Equal(t, 2, f.search.calls)
	snapshot, err := ledger.Snapshot(context.Background(), f.owner, f.project.ID)
	require.NoError(t, err)
	require.Equal(t, ResearchQuotaAmount{Searches: 2, Results: 2}, snapshot.Charged)
	require.Equal(t, snapshot.Charged, snapshot.Measured)
}
