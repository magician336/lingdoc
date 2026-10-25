package workspacecore

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

// #619: the license change set advances the material it re-authorizes, so the
// snapshot the assessment stored could never be current again, and the change
// set was structurally impossible to stabilize.
//
// The tests below pin the three answers that made it possible:
//
//   - A1: a current version that is EXACTLY the version the change set's own
//     apply declared to produce is that change set's own step.
//   - A1: every other version - including a LATER one somebody else produced -
//     is still drift and still blocks. The comparison is equality, never "at
//     least".
//   - B: a material is judged on version validity (the content fingerprint the
//     grant was pinned to), not on a re-confirmation, and the other impacted
//     objects keep the re-confirmation they always needed.
//
// and the audit record that makes the stabilization attributable to a real
// subject, an authorization and a channel - and honest about the ones the
// server cannot prove.

// assetLicenseFixture drives the real F01-shaped flow: a material is bound and
// already carries a grant, a license change set is drafted against the
// material's current revision, and that change set is assessed and applied
// through the production path. Nothing here is hand-inserted.
type assetLicenseFixture struct {
	svc     *Service
	owner   Actor
	project Project
	change  ChangeSet
	asset   string
}

func newAssetLicenseFixture(t *testing.T, key string) assetLicenseFixture {
	t.Helper()
	f := newAgentFixture(t, false)
	seedNativeEvidence(t, f)
	f.svc.SetImpactRuleChecker(cleanRuleChecker{})
	// The material already carries a grant, so the change set is a RE-grant
	// and not a first grant.
	require.NoError(t, f.svc.repository.Transaction(context.Background(), TransactionOptions{}, func(tx Transaction) error {
		return storeStoredObject(tx, StoredObject{}, f.project.ID, AssetPolicyKind, "asset-1", 1, ProjectAssetPolicy{
			ID: "asset-1", ProjectID: f.project.ID, AssetID: "asset-1", Version: 1, AssetRevision: 1,
			PolicyVersion: 1, Scope: "project", Purpose: "原始人工用途", CreatedBy: f.owner.UserID})
	}), "seed the material's grant")
	change := semanticDraft(t, f.svc, f.owner, f.project, key+"-draft", SemanticDelta{
		Kind: "ProjectAssetScope", TargetID: "asset-1", ExpectedVersion: 1,
		ProposedAssetPolicy: &ProjectAssetPolicy{Scope: "personal", Purpose: "仅本人复核", ModelUseAllowed: false}})
	semanticApply(t, f.svc, f.owner, f.project, change, key)
	return assetLicenseFixture{svc: f.svc, owner: f.owner, project: reloadProject(t, f.svc, f.owner, f.project.ID), change: change, asset: "asset-1"}
}

// currentAssetRevision reads the material's current revision through the real
// domain seam.
func currentAssetRevision(t *testing.T, svc *Service, projectID, assetID string) string {
	t.Helper()
	var revision int64
	require.NoError(t, svc.repository.Transaction(context.Background(), TransactionOptions{ReadOnly: true}, func(tx Transaction) error {
		truth, err := tx.(assetPolicyTransaction).AssetTruth(Project{ID: projectID}, assetID)
		if err != nil {
			return err
		}
		revision = truth.Asset.AssetRevision
		return nil
	}))
	return strconv.FormatInt(revision, 10)
}

// advanceAssetRevision moves the material on by one revision WITHOUT going
// through a ChangeSet, which is what another actor's asset-level operation
// does. The context revision stays where it is, so the change set under test
// is still the one that targets the current revision.
func advanceAssetRevision(t *testing.T, svc *Service, projectID, assetID string) {
	t.Helper()
	require.NoError(t, svc.repository.Transaction(context.Background(), TransactionOptions{}, func(tx Transaction) error {
		port, ok := tx.(assetPolicyTransaction)
		if !ok {
			return ErrInvalidState
		}
		truth, err := port.AssetTruth(Project{ID: projectID}, assetID)
		if err != nil {
			return err
		}
		return port.AdvanceAssetPolicy(truth)
	}), "advance the material's revision")
}

// closeBlockReleaseTask closes the material's BLOCK_RELEASE obligation the way
// the preflight acknowledgment closes it - against a preflight proof - but
// through the store seam. The acknowledgment itself resolves the proof from a
// persisted delivery snapshot, which lives in the delivery module; the
// discharge mechanism itself is pinned by the preflight guard tests.
func closeBlockReleaseTask(t *testing.T, f assetLicenseFixture, key string) {
	t.Helper()
	require.NoError(t, f.svc.repository.Transaction(context.Background(), TransactionOptions{}, func(tx Transaction) error {
		store := tx.(impactTransaction)
		tasks, err := store.ImpactTasks(f.project.ID)
		if err != nil {
			return err
		}
		for _, task := range tasks {
			if task.ChangeSetID != f.change.ID || task.Action != "BLOCK_RELEASE" || task.Status != "OPEN" {
				continue
			}
			next := task
			next.Status = "DONE"
			next.Version++
			next.Disposition = "当前领域Preflight通过"
			next.ClosureEvidence = []ObjectVersionRef{{Kind: "ProjectSpec", ID: f.project.ID, Version: "fixture-preflight-digest"}}
			next.UpdatedAt = time.Now().UTC()
			return store.UpdateImpactTask(task, next)
		}
		return ErrNotFound
	}), "close the BLOCK_RELEASE obligation")
	f.svc.SetImpactPreflightChecker(fixturePreflightChecker{project: reloadProject(t, f.svc, f.owner, f.project.ID), current: true})
}

// A1 accepted: the version the change set's OWN apply produced is not drift.
// Before the fix this change set could never stabilize: the assessment
// recorded the material at revision 1, the apply advanced it to revision 2,
// and the re-read called the difference drift.
func TestStabilizationAcceptsTheChangeSetsOwnMaterialAdvance(t *testing.T) {
	f := newAssetLicenseFixture(t, "asset-own-advance")

	// The assessment recorded the material at the version it had BEFORE the
	// change set's own apply advanced it.
	assessment := readPersistedAssessment(t, f.svc, f.project.ID, f.change.ID)
	recorded := ""
	for _, object := range assessment.Objects {
		if object.Ref.Kind == "ProjectAsset" && object.Ref.ID == f.asset {
			recorded = object.Ref.Version
		}
	}
	require.Equal(t, "1", recorded, "the assessment must record the pre-apply revision: %+v", assessment.Objects)
	// The apply produced exactly the declared step.
	require.Equal(t, strconv.FormatInt(int64(1+assetPolicyVersionAdvance), 10), currentAssetRevision(t, f.svc, f.project.ID, f.asset),
		"the apply must produce exactly the declared advance")

	closeBlockReleaseTask(t, f, "asset-own-advance")
	result, err := runStabilization(t, f.svc, f.owner, f.project.ID, f.change.ID, "asset-own-advance-stabilize")
	require.NoError(t, err)
	require.Equal(t, "STABILIZED", result.Status,
		"the change set's own advance must not block stabilization: %+v", result.Blockers)
}

// A1 rejected: a LATER version is somebody else's work. The comparison is
// equality, so "at least the declared target" would wrongly accept this.
func TestStabilizationStillBlocksWhenTheMaterialMovedOnAfterTheApply(t *testing.T) {
	f := newAssetLicenseFixture(t, "asset-drift")
	closeBlockReleaseTask(t, f, "asset-drift")

	// Somebody else advances the material once more, without a ChangeSet.
	advanceAssetRevision(t, f.svc, f.project.ID, f.asset)
	require.Equal(t, "3", currentAssetRevision(t, f.svc, f.project.ID, f.asset))

	result, err := runStabilization(t, f.svc, f.owner, f.project.ID, f.change.ID, "asset-drift-stabilize")
	require.NoError(t, err)
	require.Equal(t, "BLOCKED", result.Status, "a later version is drift and must block: %+v", result.Blockers)
	blocker := blockerFor(t, result, "impact_object_stale")
	require.Equal(t, f.asset, blocker.TargetID)
}

// A1 rejected: a version this change set's apply never produced cannot be
// claimed as its own. The apply carries the optimistic lock that makes the
// attribution possible in the first place, so the lock has to hold: a material
// that moved between the assessment and the apply makes the apply refuse.
func TestStabilizationRefusesToApplyOverAConcurrentMaterialChange(t *testing.T) {
	f := newAgentFixture(t, false)
	seedNativeEvidence(t, f)
	f.svc.SetImpactRuleChecker(cleanRuleChecker{})
	change := semanticDraft(t, f.svc, f.owner, f.project, "asset-concurrent-draft", SemanticDelta{
		Kind: "ProjectAssetScope", TargetID: "asset-1", ExpectedVersion: 1,
		ProposedAssetPolicy: &ProjectAssetPolicy{Scope: "personal", Purpose: "仅本人复核", ModelUseAllowed: false}})
	if _, _, _, err := f.svc.AssessChangeSet(context.Background(), f.owner, f.project.ID, change.ID, "asset-concurrent-assess"); err != nil {
		t.Fatalf("assess: %v", err)
	}

	// Somebody else advances the material after the assessment.
	advanceAssetRevision(t, f.svc, f.project.ID, "asset-1")
	require.Equal(t, "2", currentAssetRevision(t, f.svc, f.project.ID, "asset-1"))

	// The apply's optimistic lock refuses: the material was no longer at the
	// version this change set declared, so this change set produced nothing.
	_, _, _, err := f.svc.ApplyChangeSet(context.Background(), f.owner, f.project.ID, change.ID, "asset-concurrent-apply")
	require.Error(t, err, "an apply over a concurrent material change must be refused")
	require.True(t, errors.Is(err, ErrVersionConflict), "the refusal must be a version conflict, got %v", err)
	require.Equal(t, "assessed", changeSetStatus(t, f.svc, f.owner, f.project.ID, change.ID),
		"a refused apply must not leave the change set applied")
}

// B: a material needs a still-valid version, not a re-confirmation. It is
// never reported as "unconfirmed" (a material has no confirmation to
// re-bind), and when its CONTENT is replaced under the same id the grant
// pinned to the old content is invalid - which is a different repair from a
// replaced version.
func TestStabilizationJudgesTheMaterialByVersionValidityNotByConfirmation(t *testing.T) {
	f := newAssetLicenseFixture(t, "asset-validity")
	closeBlockReleaseTask(t, f, "asset-validity")

	// The material carries no confirmation at all, and must not be asked for
	// one: the version is what has to be valid.
	result, err := runStabilization(t, f.svc, f.owner, f.project.ID, f.change.ID, "asset-validity-stabilize")
	require.NoError(t, err)
	require.Equal(t, "STABILIZED", result.Status, "a material needs a valid version, not a confirmation: %+v", result.Blockers)
	for _, blocker := range result.Blockers {
		require.NotEqual(t, "impact_object_unconfirmed", blocker.Code,
			"a material must not be asked for a confirmation: %+v", result.Blockers)
	}

	// The material's content moves on under the same id. The revision number
	// is untouched, so only the content fingerprint can tell that the grant
	// no longer covers what the material carries.
	require.NoError(t, f.svc.testDB().Exec("UPDATE knowledges SET file_hash = ? WHERE id = ?", "replaced-file-hash", "knowledge-1").Error)

	invalid, err := runStabilization(t, f.svc, f.owner, f.project.ID, f.change.ID, "asset-validity-replaced")
	require.NoError(t, err)
	require.Equal(t, "BLOCKED", invalid.Status, "a replaced material must block: %+v", invalid.Blockers)
	blocker := blockerFor(t, invalid, "impact_object_authorization_invalid")
	require.Equal(t, f.asset, blocker.TargetID)
}

// B does not weaken the other objects: a chapter that is an impacted object
// and is not confirmed still blocks, with the literal code it always had.
//
// The chapter's task is driven to STALE on purpose. A STALE task is neither a
// blocker nor closure, so the chapter stops being "accounted for" and the
// impacted-object re-read has to check its confirmation - which is exactly
// the check the material exemption must not have weakened.
func TestStabilizationKeepsTheConfirmationRequirementForOtherImpactedObjects(t *testing.T) {
	svc := impactTaskStore(t, filepath.Join(t.TempDir(), "asset-other-objects.db"))
	actor := Actor{TenantID: 3209, UserID: "owner"}
	p, chapters, change := stabilizationFixture(t, svc, actor)

	// The chapter is a recorded impacted object of this assessment.
	assessment := readPersistedAssessment(t, svc, p.ID, change.ID)
	target := ""
	for _, object := range assessment.Objects {
		if object.Ref.Kind == "ChapterVersion" {
			target = object.Ref.ID
		}
	}
	require.NotEmpty(t, target, "a chapter must be a recorded impacted object: %+v", assessment.Objects)

	// Replace the chapter body so the task's recorded target version stops
	// being current, then act on it: it transitions to STALE.
	task := taskFor(t, tasksInProject(t, svc, actor, p.ID), change.ID)
	replaceTargetChapterBody(t, svc, actor, p, target, "asset-other-objects-replace", "稳定化 替换正文")
	raw, _, _, err := svc.ActOnImpactTask(context.Background(), actor, p.ID, task.ID, "asset-other-objects-stale", ImpactTaskActionInput{
		ExpectedVersion: task.Version, TargetRevision: p.CurrentContextRevision, Action: "start", Reason: "推进"})
	require.NoError(t, err)
	require.Equal(t, "STALE", decodeImpactTask(t, raw).Status)

	// Close every remaining real task, so the STALE task's object is the only
	// outstanding question.
	closeBlockingTasks(t, svc, actor, p.ID)
	revokeChapterConfirmation(t, svc, actor, p.ID, target)

	result, err := runStabilization(t, svc, actor, p.ID, change.ID, "asset-other-objects-stabilize")
	require.NoError(t, err)
	require.Equal(t, "BLOCKED", result.Status, "an unconfirmed impacted chapter must still block: %+v", result.Blockers)
	blocker := blockerFor(t, result, "impact_object_unconfirmed")
	require.Equal(t, target, blocker.TargetID)
	require.NotContains(t, blockerCodesOf(result), "impact_object_authorization_invalid",
		"the material's rule must not leak onto a chapter")
	_ = chapters
}

// C: the stabilization audit record says who acted, how they were authorized,
// through which channel, when, against what, on what evidence, and under which
// correlation id - and it does not dress the machine's verdict up as a human
// judgement.
func TestStabilizationAuditRecordsExecutorAuthorizationAndChannel(t *testing.T) {
	f := newAssetLicenseFixture(t, "asset-audit")
	closeBlockReleaseTask(t, f, "asset-audit")
	_, err := runStabilization(t, f.svc, f.owner, f.project.ID, f.change.ID, "asset-audit-stabilize")
	require.NoError(t, err)

	row := lastStabilizationAudit(t, f.svc)
	require.Equal(t, f.owner.UserID, row.ActorID, "the audit must name the executing subject")
	require.Equal(t, f.owner.UserID, row.UserID)
	require.Equal(t, "manage", row.Capability, "the audit must name the capability that was exercised")
	require.Equal(t, "STABILIZED", row.Decision)

	var details map[string]any
	require.NoError(t, json.Unmarshal([]byte(row.DetailsJSON), &details))
	record, ok := details["stabilization_audit"].(map[string]any)
	require.True(t, ok, "the audit must carry the stabilization record: %s", row.DetailsJSON)

	executor, ok := record["executor"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, f.owner.UserID, executor["user_id"])

	authorization, ok := record["authorization"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "manage", authorization["capability"])
	require.Equal(t, true, authorization["project_owner"])

	channel, ok := record["channel"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, stabilizationChannelUnbound, channel["class"],
		"a call without a bound principal must not be dressed up as a UI session: %v", channel)
	require.Equal(t, stabilizationUnprovable, channel["ui_origin"])
	require.Equal(t, stabilizationUnprovable, channel["automation"])
	require.NotEmpty(t, channel["limitation"], "the capability boundary must be stated in words")

	decision, ok := record["decision"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "machine_evaluation", decision["verdict_source"],
		"the verdict is computed and must not be recorded as a human judgement")
	require.Equal(t, "STABILIZED", decision["verdict"])

	target, ok := record["target"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, f.project.ID, target["project_id"])
	require.Equal(t, f.change.ID, target["change_set_id"])

	evidence, ok := record["evidence"].(map[string]any)
	require.True(t, ok)
	require.NotEmpty(t, evidence["closure_evidence"], "the record must name the evidence the verdict leaned on")

	require.NotEmpty(t, record["audit_correlation_id"], "the record must carry an audit correlation id")
	require.NotEmpty(t, row.ID)
}

// C: the channel is classified from what the server can prove, and the facts
// it cannot prove are recorded as unprovable rather than guessed.
func TestStabilizationAuditClassifiesTheChannelAndMarksWhatItCannotProve(t *testing.T) {
	f := newAssetLicenseFixture(t, "asset-channel")
	closeBlockReleaseTask(t, f, "asset-channel")

	// A signed-in session: the server proves the credential class and nothing
	// more. It must NOT claim the request came from the UI.
	ctx := types.WithPrincipal(context.Background(), types.Principal{Type: types.PrincipalWebUser, ID: f.owner.UserID})
	_, _, _, err := f.svc.StabilizeRevision(ctx, f.owner, f.project.ID, f.change.ID, "asset-channel-stabilize")
	require.NoError(t, err)

	row := lastStabilizationAudit(t, f.svc)
	var details map[string]any
	require.NoError(t, json.Unmarshal([]byte(row.DetailsJSON), &details))
	record := details["stabilization_audit"].(map[string]any)
	channel := record["channel"].(map[string]any)
	require.Equal(t, stabilizationChannelUserSession, channel["class"])
	require.Equal(t, types.PrincipalWebUser, channel["principal_type"])
	require.Equal(t, stabilizationUnprovable, channel["ui_origin"],
		"a session credential does not prove a UI click")
	require.Equal(t, stabilizationUnprovable, channel["automation"],
		"a session credential does not prove a person rather than a script")
	require.NotEmpty(t, channel["limitation"])
}

// C: a subject without authority cannot stabilize, and a machine principal is
// refused before it can act.
func TestStabilizationRefusesASubjectWithoutAuthority(t *testing.T) {
	svc := impactTaskStore(t, filepath.Join(t.TempDir(), "asset-authority.db"))
	actor := Actor{TenantID: 3210, UserID: "owner"}
	p, _, change := stabilizationFixture(t, svc, actor)
	closeBlockingTasks(t, svc, actor, p.ID)

	// A member who is not the Owner.
	reader := Actor{TenantID: actor.TenantID, UserID: "reader"}
	seedTenantMember(t, svc, reader)
	require.NoError(t, (gormTransaction{svc.testDB()}).EnsureMember(p.ID, reader.UserID, "collaborator"))
	_, _, _, err := svc.StabilizeRevision(context.Background(), reader, p.ID, change.ID, "asset-authority-reader")
	require.Error(t, err, "a subject that is not the Owner must not stabilize")
	require.True(t, errors.Is(err, ErrNotFound), "the refusal must not disclose the change set, got %v", err)

	// A machine principal is refused by the human-decision gate.
	machine := types.WithPrincipal(context.Background(), types.Principal{Type: types.PrincipalAPITenant, ID: "machine-key"})
	_, _, _, err = svc.StabilizeRevision(machine, actor, p.ID, change.ID, "asset-authority-machine")
	require.Error(t, err, "a machine principal must not stabilize")
	require.True(t, errors.Is(err, ErrHumanDecisionRequired), "the refusal must be the human-decision gate, got %v", err)

	// A refused attempt leaves no stabilization record behind: an API-key
	// caller that could not act must not show up as one that did.
	require.Equal(t, int64(0), countAuditEvents(t, svc, "revision.stabilize"),
		"a refused stabilization must not write a stabilization audit entry")
}

// lastStabilizationAudit reads the most recent stabilization audit row.
func lastStabilizationAudit(t *testing.T, svc *Service) auditEventRow {
	t.Helper()
	var row auditEventRow
	require.NoError(t, svc.testDB().Table("lingdoc_project_audits").Where("action = ?", "revision.stabilize").
		Order("created_at desc, id desc").Take(&row).Error)
	return row
}
