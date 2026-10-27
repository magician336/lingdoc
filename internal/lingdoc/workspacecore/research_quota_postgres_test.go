package workspacecore

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestPostgresResearchQuotaLedgerLockRollbackAndRestart(t *testing.T) {
	fixture := newPostgresTestDatabase(t)
	db, closePool := openSilentPostgres(t, fixture.dsn)
	t.Cleanup(closePool)
	applyVersionedMigrations(t, db)
	require.NoError(t, db.Exec(tenantMembersFixtureDDLPostgres).Error)
	svc := NewServiceWithSources(NewGORMRepository(db), ContractDemoTemplate{}, &fakeSources{})
	owner := Actor{TenantID: 309, UserID: "owner"}
	project := seedActiveImpactProject(t, svc, owner)
	f := agentFixture{svc: svc, owner: owner, project: project}
	plan := ResearchTask{ProjectID: project.ID, Queries: []string{"synthetic first", "synthetic second"}, Basis: versionBasisFor(project, Chapter{})}
	first := quotaPendingRun(t, f, plan, "pg-first")
	second := quotaPendingRun(t, f, plan, "pg-second")
	now := time.Now().UTC()
	policy := &quotaPolicyFixture{ResearchQuotaPolicy{Version: "synthetic-pg-v1", ApprovedBy: "synthetic-only", WindowID: "synthetic-pg-window", StartsAt: now.Add(-time.Hour), EndsAt: now.Add(time.Hour), Limit: ResearchQuotaAmount{Searches: 2, Results: 40}}}
	ledger := NewResearchQuotaLedger(svc, policy)
	ctx := context.Background()
	// First-row creation is protected by the existing project row lock too.
	require.NoError(t, db.Exec(`CREATE FUNCTION block_quota_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.action = 'g2.research_budget.reserved' THEN RAISE EXCEPTION 'synthetic quota audit failure'; END IF; RETURN NEW; END $$;
CREATE TRIGGER block_quota_audit BEFORE INSERT ON lingdoc_project_audits FOR EACH ROW EXECUTE FUNCTION block_quota_audit();`).Error)
	require.Error(t, ledger.Reserve(ctx, owner, project.ID, "pg-first", first))
	var count int64
	require.NoError(t, db.Table("lingdoc_g2_objects").Where("project_id = ? AND kind IN ?", project.ID, []string{researchQuotaWindowKind, researchQuotaEntryKind, researchQuotaHeadKind}).Count(&count).Error)
	require.Zero(t, count)
	require.NoError(t, db.Table("lingdoc_g2_object_versions").Where("project_id = ? AND kind IN ?", project.ID, []string{researchQuotaWindowKind, researchQuotaEntryKind, researchQuotaHeadKind}).Count(&count).Error)
	require.Zero(t, count, "audit failure must roll back quota history too")
	require.NoError(t, db.Exec("DROP TRIGGER block_quota_audit ON lingdoc_project_audits; DROP FUNCTION block_quota_audit();").Error)
	start := make(chan struct{})
	results := make(chan error, 2)
	var wait sync.WaitGroup
	for _, task := range []ResearchTask{first, second} {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			results <- ledger.Reserve(ctx, owner, project.ID, task.LatestRunID, task)
		}()
	}
	close(start)
	wait.Wait()
	close(results)
	wins, refused := 0, 0
	for err := range results {
		if err == nil {
			wins++
		} else if errors.Is(err, ErrResearchQuotaExhausted) {
			refused++
		} else {
			t.Fatal(err)
		}
	}
	require.Equal(t, 1, wins)
	require.Equal(t, 1, refused)
	before, err := ledger.Snapshot(ctx, owner, project.ID)
	require.NoError(t, err)
	require.Equal(t, policy.value.Limit, before.Charged)
	closePool()
	reopened, closeReopened := openSilentPostgres(t, fixture.dsn)
	t.Cleanup(closeReopened)
	restarted := NewResearchQuotaLedger(NewServiceWithSources(NewGORMRepository(reopened), ContractDemoTemplate{}, &fakeSources{}), policy)
	after, err := restarted.Snapshot(ctx, owner, project.ID)
	require.NoError(t, err)
	require.Equal(t, before, after)
}
