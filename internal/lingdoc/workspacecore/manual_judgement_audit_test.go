package workspacecore

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"slices"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

// #649: the server-side half of the manual judgement.
//
// #633 gave the runner a ledger of human judgements and three tiers of
// authority, and no judgement could ever reach the top tier: the runner
// proves a judgement against a SERVER-SIDE audit record, field by field,
// and the server had no audit record that carried the seven facts a
// judgement is made of.
//
// The tests below pin the answers this file implements:
//
//   - the caller supplies five fields and the server derives the other
//     seven; a judgement that names its own actor is not a judgement the
//     server can attest to;
//   - the seven the runner compares are equal, BYTE FOR BYTE, between the
//     authoritative record the service returns and the row the export
//     projects;
//   - a machine principal is refused before anything is written;
//   - an audit row that cannot say who acted is refused rather than
//     recorded;
//   - the channel says what the server can prove and nothing more;
//   - one assertion of one batch is judged once: the same judgement
//     replays, a different one conflicts;
//   - a failed human judgement stays a failure, and a machine evaluation
//     never writes a human judgement;
//   - an assertion nobody judged stays unjudged;
//   - the judgement and its audit row commit together or not at all.

// runnerAuditFields are the seven facts the runner compares, field by field,
// to decide whether a judgement is authoritative.
//
// They are the runner's own tuple (scripts/lingdoc_mock/manual_decision_audit.py),
// reproduced here so that the test compares what the runner compares.
var runnerAuditFields = []string{
	"actor",
	"authorization",
	"channel",
	"occurred_at",
	"assertion_id",
	"run_id",
	"evidence_ref",
}

// manualJudgementFixture drives a real project through the production path:
// a tenant member, a project, a spec and an activation. Nothing about the
// judgement itself is seeded.
func manualJudgementFixture(t *testing.T, key string) (*Service, Actor, Project) {
	t.Helper()
	svc := impactTaskStore(t, filepath.Join(t.TempDir(), key+".db"))
	owner := Actor{TenantID: 649, UserID: "owner"}
	project := seedActiveImpactProject(t, svc, owner)
	return svc, owner, project
}

// manualJudgementInput is a judgement as the runner's ledger holds it: the
// five fields a human states, and nothing else.
func manualJudgementInput(runID, assertionID string) ManualJudgementInput {
	return ManualJudgementInput{
		AssertionID:   assertionID,
		AssertionText: "把「允许用这份资料做模型生成」写成一条显式提案，绑定该资料当前的修订号与项目的上下文修订号。",
		RunID:         runID,
		Verdict:       "passed",
		EvidenceRef:   "run:" + runID + "/f01-report.json",
	}
}

// countManualJudgementRecords counts the judgement results the project holds.
func countManualJudgementRecords(t *testing.T, svc *Service, projectID string) int64 {
	t.Helper()
	var count int64
	require.NoError(t, svc.testDB().Table("lingdoc_g5_records").
		Where("project_id = ? AND kind = ?", projectID, manualJudgementRecordKind).
		Count(&count).Error)
	return count
}

// countManualJudgementOperations counts the operation receipts of this
// operation, so a test can prove that a failed attempt left nothing.
func countManualJudgementOperations(t *testing.T, svc *Service) int64 {
	t.Helper()
	var count int64
	require.NoError(t, svc.testDB().Table("lingdoc_operations").
		Where("operation = ?", string(operationManualJudgement)).
		Count(&count).Error)
	return count
}

// decodeManualJudgement decodes the authoritative record the service
// returned.
func decodeManualJudgement(t *testing.T, raw json.RawMessage) ManualJudgementRecord {
	t.Helper()
	var record ManualJudgementRecord
	require.NoError(t, json.Unmarshal(raw, &record))
	return record
}

// judgementField reads one of the runner's seven facts out of the
// authoritative record.
func judgementField(record ManualJudgementRecord, field string) string {
	switch field {
	case "actor":
		return record.Actor
	case "authorization":
		return record.Authorization
	case "channel":
		return record.Channel
	case "occurred_at":
		return record.OccurredAt
	case "assertion_id":
		return record.AssertionID
	case "run_id":
		return record.RunID
	case "evidence_ref":
		return record.EvidenceRef
	}
	return ""
}

// auditField reads one of the runner's seven facts out of an exported row.
func auditField(audit ManualJudgementAudit, field string) string {
	switch field {
	case "actor":
		return audit.Actor
	case "authorization":
		return audit.Authorization
	case "channel":
		return audit.Channel
	case "occurred_at":
		return audit.OccurredAt
	case "assertion_id":
		return audit.AssertionID
	case "run_id":
		return audit.RunID
	case "evidence_ref":
		return audit.EvidenceRef
	}
	return ""
}

// #1: the server derives the facts, and the record it returns and the row
// the export projects are equal field by field, byte for byte.
func TestManualJudgementRecordsTheServerDerivedFactsAndExportsThem(t *testing.T) {
	svc, owner, project := manualJudgementFixture(t, "mj-authoritative")
	input := manualJudgementInput("f01-20261025T010000Z-abcd", "F01-06b")

	record := decodeManualJudgement(t, submitManualJudgement(t, svc, owner, project.ID, "manual-judgement-0001", input))

	// The five fields the caller stated are kept as they were stated.
	require.Equal(t, input.RunID, record.RunID)
	require.Equal(t, input.AssertionID, record.AssertionID)
	require.Equal(t, input.AssertionText, record.AssertionText)
	require.Equal(t, input.Verdict, record.Verdict)
	require.Equal(t, input.EvidenceRef, record.EvidenceRef)

	// The other five are the server's statements about the call.
	require.Equal(t, owner.UserID, record.Actor, "the actor is the authenticated subject, not a claim")
	require.NotEmpty(t, record.Authorization, "the authorization is the server's statement")
	require.Equal(t, manualJudgementChannelUnverified, record.Channel)
	require.NotEmpty(t, record.OccurredAt)
	require.NotEmpty(t, record.AuditID)

	// The export projects the very same strings.
	exported, err := svc.ExportManualJudgements(context.Background(), owner, project.ID)
	require.NoError(t, err)
	require.Len(t, exported, 1)
	require.Equal(t, record.AuditID, exported[0].AuditID, "the export is keyed by the audit id")

	// This is the runner's own comparison, reproduced: the seven facts have
	// to be equal, and equal means the same bytes.
	for _, field := range runnerAuditFields {
		require.Equal(t, judgementField(record, field), auditField(exported[0], field),
			"the authoritative record and the exported row must be equal in %q", field)
	}

	// And the exported row is shaped the way the runner reads it: the seven
	// facts at the top level, next to the audit id.
	rawExport, err := json.Marshal(exported[0])
	require.NoError(t, err)
	var shape map[string]any
	require.NoError(t, json.Unmarshal(rawExport, &shape))
	require.Contains(t, shape, "audit_id")
	for _, field := range runnerAuditFields {
		require.Contains(t, shape, field)
	}

	// One judgement is one audit row.
	require.Equal(t, int64(1), countAuditEvents(t, svc, manualJudgementAuditAction))
	require.Equal(t, int64(1), countManualJudgementRecords(t, svc, project.ID))
}

// #2: a subject that may read the project but is not its Owner may not
// judge, and the refusal leaves nothing behind.
func TestManualJudgementRefusesASubjectWithoutAuthority(t *testing.T) {
	svc, owner, project := manualJudgementFixture(t, "mj-authority")

	// A project administrator may MANAGE the project - the capability check
	// passes - and an administrator is not the Owner. The judgement speaks
	// for the project, so the Owner rule has to be a rule of its own.
	admin := Actor{TenantID: owner.TenantID, UserID: "admin"}
	seedTenantMember(t, svc, admin)
	current, err := svc.GetProject(context.Background(), owner, project.ID)
	require.NoError(t, err)
	member := Member{UserID: admin.UserID, Role: "collaborator", GovernanceRole: "admin", Status: "active"}
	require.NoError(t, svc.repository.Transaction(context.Background(), TransactionOptions{}, func(tx Transaction) error {
		return tx.ReplaceMembers(project.ID, append(slices.Clone(current.Members), member))
	}), "seed the administrator")

	_, _, _, err = svc.SubmitManualJudgement(context.Background(), admin, project.ID,
		"manual-judgement-0002", manualJudgementInput("f01-run-0002", "F01-06b"))
	require.Error(t, err, "a subject that is not the Owner must not judge")
	require.True(t, errors.Is(err, ErrNotFound), "the refusal must not disclose the project, got %v", err)

	require.Equal(t, int64(0), countManualJudgementRecords(t, svc, project.ID),
		"a refused judgement must not leave a judgement result")
	require.Equal(t, int64(0), countAuditEvents(t, svc, manualJudgementAuditAction),
		"a refused judgement must not leave an audit row")
}

// #3: a machine identity may not judge, and the refusal happens before
// anything is written - not even a rolled-back row.
func TestManualJudgementRefusesAMachinePrincipalAndLeavesNoRecord(t *testing.T) {
	svc, owner, project := manualJudgementFixture(t, "mj-machine")

	for _, principal := range []types.Principal{
		{Type: types.PrincipalAPITenant, ID: "machine-key"},
		{Type: types.PrincipalEmbedChannel, ID: "embed-channel"},
	} {
		ctx := types.WithPrincipal(context.Background(), principal)
		_, _, _, err := svc.SubmitManualJudgement(ctx, owner, project.ID,
			"manual-judgement-0003", manualJudgementInput("f01-run-0003", "F01-06b"))
		require.Error(t, err, "a machine principal must not judge")
		require.True(t, errors.Is(err, ErrHumanDecisionRequired),
			"the refusal must be the human-decision gate, got %v", err)
	}

	require.Equal(t, int64(0), countManualJudgementRecords(t, svc, project.ID),
		"a refused attempt must not leave a judgement result")
	require.Equal(t, int64(0), countAuditEvents(t, svc, manualJudgementAuditAction),
		"a refused attempt must not leave an audit row")
	require.Equal(t, int64(0), countManualJudgementOperations(t, svc),
		"a refused attempt must not leave an operation receipt")
}

// #4: the hard gate. An audit row that cannot say who acted, how they were
// authorized, through which channel and when is not a weaker audit, it is a
// missing one, and it is refused rather than recorded.
func TestManualJudgementAuditGateRefusesARecordThatCannotSayWhoActed(t *testing.T) {
	complete := map[string]any{
		"actor":              "owner",
		"authorization":      "method=project_membership_capability_and_owner_check",
		"channel":            manualJudgementChannelUnverified,
		"occurred_at":        "2026-10-25T01:00:00Z",
		"assertion_id":       "F01-06b",
		"run_id":             "f01-run-0004",
		"evidence_ref":       "run:f01-run-0004/f01-report.json",
		"channel_class":      stabilizationChannelUserSession,
		"channel_ui_origin":  stabilizationUnprovable,
		"channel_automation": stabilizationUnprovable,
	}
	require.True(t, manualJudgementAuditComplete(complete), "a complete record must pass")

	required := append(append([]string{}, manualJudgementAuditFields...), manualJudgementChannelFields...)
	for _, field := range required {
		missing := map[string]any{}
		for key, value := range complete {
			missing[key] = value
		}
		delete(missing, field)
		require.False(t, manualJudgementAuditComplete(missing), "a record without %q must be refused", field)

		blank := map[string]any{}
		for key, value := range complete {
			blank[key] = value
		}
		blank[field] = "   "
		require.False(t, manualJudgementAuditComplete(blank), "a record with a blank %q must be refused", field)

		wrongType := map[string]any{}
		for key, value := range complete {
			wrongType[key] = value
		}
		wrongType[field] = 42
		require.False(t, manualJudgementAuditComplete(wrongType), "a record whose %q is not a string must be refused", field)
	}
}

// #5, first half: the batch binding. The evidence a judgement rests on must
// be THIS batch's evidence.
func TestManualJudgementRefusesEvidenceFromAnotherBatch(t *testing.T) {
	svc, owner, project := manualJudgementFixture(t, "mj-batch")

	input := manualJudgementInput("f01-run-0005", "F01-06b")
	input.EvidenceRef = "run:f01-run-9999/f01-report.json"
	_, _, _, err := svc.SubmitManualJudgement(context.Background(), owner, project.ID,
		"manual-judgement-0005", input)
	require.Error(t, err, "a judgement that rests on another batch's evidence must be refused")
	require.True(t, errors.Is(err, ErrInvalidRequest), "the refusal must be a bad request, got %v", err)

	require.Equal(t, int64(0), countManualJudgementRecords(t, svc, project.ID))
	require.Equal(t, int64(0), countAuditEvents(t, svc, manualJudgementAuditAction))
}

// #5, second half: the judged object. One assertion of one batch is judged
// once, and a second statement about it that quotes another prose is not a
// replay of it - it is a statement about something else.
func TestManualJudgementRefusesAJudgementThatQuotesAnotherAssertion(t *testing.T) {
	svc, owner, project := manualJudgementFixture(t, "mj-object")

	first := manualJudgementInput("f01-run-0006", "F01-06b")
	record := decodeManualJudgement(t, submitManualJudgement(t, svc, owner, project.ID,
		"manual-judgement-0006", first))

	second := manualJudgementInput("f01-run-0006", "F01-06b")
	second.AssertionText = "变更集评估后才允许应用；服务端的合法前态只有 assessed。"
	_, _, _, err := svc.SubmitManualJudgement(context.Background(), owner, project.ID,
		"manual-judgement-0006-other", second)
	require.Error(t, err, "a judgement that quotes another prose must be refused")
	require.True(t, errors.Is(err, ErrInvalidRequest), "the refusal must be a bad request, got %v", err)

	// The judgement that was made is untouched, and nothing was added.
	exported, err := svc.ExportManualJudgements(context.Background(), owner, project.ID)
	require.NoError(t, err)
	require.Len(t, exported, 1)
	require.Equal(t, record.AuditID, exported[0].AuditID)
	require.Equal(t, int64(1), countManualJudgementRecords(t, svc, project.ID))
}

// #6: the idempotency semantics, stated and measurable.
//
//   - the same key and the same body replays and writes no second row;
//   - the same key and a different body is a conflict;
//   - a different key and the same business key and the same content
//     replays the judgement that was made;
//   - a different key and the same business key and a different content is a
//     conflict: one assertion of one batch is not re-judged.
func TestManualJudgementReplaysTheSameJudgementAndConflictsOnAnother(t *testing.T) {
	svc, owner, project := manualJudgementFixture(t, "mj-idempotency")
	input := manualJudgementInput("f01-run-0007", "F01-06b")

	first := decodeManualJudgement(t, submitManualJudgement(t, svc, owner, project.ID,
		"manual-judgement-0007", input))
	require.Equal(t, int64(1), countAuditEvents(t, svc, manualJudgementAuditAction))

	// (a) the same key and the same body replays.
	raw, status, replayed, err := svc.SubmitManualJudgement(context.Background(), owner, project.ID,
		"manual-judgement-0007", input)
	require.NoError(t, err)
	require.True(t, replayed, "the same key and the same body must replay")
	require.Equal(t, 201, status, "a replay answers with the stored status")
	var replayedRecord ManualJudgementRecord
	require.NoError(t, json.Unmarshal(raw, &replayedRecord))
	require.Equal(t, first.AuditID, replayedRecord.AuditID, "a replay returns the judgement that was made")
	require.Equal(t, int64(1), countAuditEvents(t, svc, manualJudgementAuditAction),
		"a replay must not write a second audit row")

	// (b) the same key and a different body is a conflict.
	changed := input
	changed.Verdict = "rejected"
	_, _, _, err = svc.SubmitManualJudgement(context.Background(), owner, project.ID,
		"manual-judgement-0007", changed)
	require.Error(t, err, "the same key with a different body must be refused")
	require.True(t, errors.Is(err, ErrIdempotencyConflict), "the refusal must be an idempotency conflict, got %v", err)

	// (c) a different key and the same business key and the same content
	// replays the judgement that was made.
	raw, _, _, err = svc.SubmitManualJudgement(context.Background(), owner, project.ID,
		"manual-judgement-0007-other", input)
	require.NoError(t, err, "the same judgement under another key must replay")
	var sameContent ManualJudgementRecord
	require.NoError(t, json.Unmarshal(raw, &sameContent))
	require.Equal(t, first.AuditID, sameContent.AuditID, "the judgement that was made is the one that is returned")
	require.Equal(t, first.OccurredAt, sameContent.OccurredAt, "a replay keeps the moment the judgement was made")
	require.Equal(t, int64(1), countAuditEvents(t, svc, manualJudgementAuditAction),
		"a replay under another key must not write a second audit row")
	require.Equal(t, int64(1), countManualJudgementRecords(t, svc, project.ID),
		"a replay under another key must not write a second judgement result")

	// (d) a different key and the same business key and a different content
	// is a conflict.
	_, _, _, err = svc.SubmitManualJudgement(context.Background(), owner, project.ID,
		"manual-judgement-0007-third", changed)
	require.Error(t, err, "the same assertion of the same batch may not be re-judged")
	require.True(t, errors.Is(err, ErrIdempotencyConflict), "the refusal must be an idempotency conflict, got %v", err)
	require.Equal(t, int64(1), countManualJudgementRecords(t, svc, project.ID),
		"a refused re-judgement must not write a second judgement result")
}

// #7: a human judgement that says the assertion did not hold stays a
// failure, and a machine evaluation never writes a human judgement.
func TestManualJudgementKeepsAFailedHumanVerdictAFailure(t *testing.T) {
	svc := impactTaskStore(t, filepath.Join(t.TempDir(), "mj-verdict.db"))
	actor := Actor{TenantID: 6497, UserID: "owner"}
	project, _, change := stabilizationFixture(t, svc, actor)

	// The machine evaluation path writes its own record, and it says what it
	// is: a machine evaluation.
	_, err := runStabilization(t, svc, actor, project.ID, change.ID, "mj-verdict-stabilize")
	require.NoError(t, err)
	require.Equal(t, int64(0), countManualJudgementRecords(t, svc, project.ID),
		"a machine evaluation must not write a human judgement")

	input := manualJudgementInput("f01-run-0008", "F01-06b")
	input.Verdict = "rejected"
	record := decodeManualJudgement(t, submitManualJudgement(t, svc, actor, project.ID,
		"manual-judgement-0008", input))
	require.Equal(t, "rejected", record.Verdict, "a human failure must not become a pass")
	require.NotEqual(t, "passed", record.Verdict)

	// The stored judgement says a HUMAN judged it.
	var stored manualJudgementStored
	require.NoError(t, svc.repository.Transaction(context.Background(), TransactionOptions{ReadOnly: true}, func(tx Transaction) error {
		var err error
		stored, err = readManualJudgement(tx.(projectRecordTransaction), project.ID, input.RunID, input.AssertionID)
		return err
	}))
	require.Equal(t, manualJudgementVerdictSourceHuman, stored.VerdictSource,
		"the judgement must say that a human reached it")
	require.Equal(t, "rejected", stored.Verdict)
	require.Equal(t, int64(1), countManualJudgementRecords(t, svc, project.ID))
}

// #8: an assertion nobody judged stays unjudged. The reader may not fill
// the gap in.
func TestManualJudgementLeavesAnUnjudgedAssertionUnjudged(t *testing.T) {
	svc, owner, project := manualJudgementFixture(t, "mj-unjudged")

	status, err := svc.ManualJudgementStatus(context.Background(), owner, project.ID, "f01-run-0009", "F01-06b")
	require.NoError(t, err)
	require.Equal(t, manualJudgementStatusUnjudged, status,
		"an assertion nobody judged must not be reported as judged")

	exported, err := svc.ExportManualJudgements(context.Background(), owner, project.ID)
	require.NoError(t, err)
	require.Empty(t, exported, "nothing was judged, so there is nothing to export")

	// Judging another assertion does not judge this one.
	submitManualJudgement(t, svc, owner, project.ID, "manual-judgement-0009",
		manualJudgementInput("f01-run-0009", "F01-06c"))

	status, err = svc.ManualJudgementStatus(context.Background(), owner, project.ID, "f01-run-0009", "F01-06b")
	require.NoError(t, err)
	require.Equal(t, manualJudgementStatusUnjudged, status,
		"another assertion's judgement must not judge this one")

	status, err = svc.ManualJudgementStatus(context.Background(), owner, project.ID, "f01-run-0009", "F01-06c")
	require.NoError(t, err)
	require.Equal(t, manualJudgementStatusJudged, status)
}

// The status check: a record that is not a human judgement is not a
// judgement, and it may not be reported as one. Reporting it would put a
// verdict on an assertion that nobody gave.
func TestManualJudgementStatusRefusesARecordThatIsNotAHumanJudgement(t *testing.T) {
	svc, owner, project := manualJudgementFixture(t, "mj-source")
	input := manualJudgementInput("f01-run-0012", "F01-06b")
	submitManualJudgement(t, svc, owner, project.ID, "manual-judgement-0012", input)

	// Something that is not a human judgement takes the record's place.
	require.NoError(t, svc.repository.Transaction(context.Background(), TransactionOptions{}, func(tx Transaction) error {
		store, ok := tx.(projectRecordTransaction)
		if !ok {
			return ErrInvalidState
		}
		var stored manualJudgementStored
		if _, err := store.GetProjectRecord(project.ID, manualJudgementRecordKind,
			manualJudgementRecordID(input.RunID, input.AssertionID), &stored); err != nil {
			return err
		}
		stored.VerdictSource = "machine_evaluation"
		stored.Verdict = "passed"
		return store.PutProjectRecord(project.ID, manualJudgementRecordKind, stored.ID, 1, stored)
	}), "replace the judgement with a machine evaluation")

	_, err := svc.ManualJudgementStatus(context.Background(), owner, project.ID, input.RunID, input.AssertionID)
	require.Error(t, err, "a record that is not a human judgement must not be reported as judged")
	require.True(t, errors.Is(err, ErrInvalidState), "the refusal must be an invalid state, got %v", err)
}

// #9: the judgement and its audit row commit together. A failed audit write
// takes the judgement result with it.
func TestManualJudgementRollsBackTheJudgementWhenTheAuditWriteFails(t *testing.T) {
	svc, owner, project := manualJudgementFixture(t, "mj-atomic")
	require.NoError(t, svc.testDB().Exec(
		"CREATE TRIGGER manual_judgement_audit_failure BEFORE INSERT ON lingdoc_project_audits "+
			"WHEN NEW.action = 'manual.judgement' BEGIN SELECT RAISE(ABORT, 'manual judgement audit unavailable'); END").Error)

	_, _, _, err := svc.SubmitManualJudgement(context.Background(), owner, project.ID,
		"manual-judgement-0010", manualJudgementInput("f01-run-0010", "F01-06b"))
	require.Error(t, err, "a failed audit write must fail the judgement")
	require.NoError(t, svc.testDB().Exec("DROP TRIGGER manual_judgement_audit_failure").Error)

	require.Equal(t, int64(0), countManualJudgementRecords(t, svc, project.ID),
		"a failed audit write must not leave a judgement result behind")
	require.Equal(t, int64(0), countAuditEvents(t, svc, manualJudgementAuditAction),
		"a failed audit write must not leave an audit row behind")
	require.Equal(t, int64(0), countManualJudgementOperations(t, svc),
		"a failed audit write must not leave an operation receipt behind")

	// Recovery: once the fault clears the same intent succeeds, which proves
	// the failure left nothing half-written that would block a retry.
	record := decodeManualJudgement(t, submitManualJudgement(t, svc, owner, project.ID,
		"manual-judgement-0010", manualJudgementInput("f01-run-0010", "F01-06b")))
	require.NotEmpty(t, record.AuditID)
	require.Equal(t, int64(1), countManualJudgementRecords(t, svc, project.ID))
	require.Equal(t, int64(1), countAuditEvents(t, svc, manualJudgementAuditAction))
}

// #10: the channel says what the server can prove and nothing more.
//
// The credential class is provable. The UI origin and the automation are
// not: a session credential cannot be told apart from a script replaying
// it, and a record that claims a person was at the keyboard is a claim the
// server cannot stand behind.
func TestManualJudgementChannelStatesOnlyWhatTheServerCanProve(t *testing.T) {
	svc, owner, project := manualJudgementFixture(t, "mj-channel")

	// A signed-in session: the server proves the credential class and
	// nothing more.
	ctx := types.WithPrincipal(context.Background(), types.Principal{Type: types.PrincipalWebUser, ID: owner.UserID})
	record := decodeManualJudgement(t, submitManualJudgement(t, svc, owner, project.ID,
		"manual-judgement-0011", manualJudgementInput("f01-run-0011", "F01-06b"), ctx))
	require.Equal(t, manualJudgementChannelUnverified, record.Channel,
		"a session credential does not prove a UI click")

	exported, err := svc.ExportManualJudgements(context.Background(), owner, project.ID)
	require.NoError(t, err)
	require.Len(t, exported, 1)
	require.Equal(t, stabilizationChannelUserSession, exported[0].ChannelClass,
		"the credential class is the strongest fact the server holds")
	require.Equal(t, stabilizationUnprovable, exported[0].ChannelUIOrigin,
		"a session credential does not prove a UI click")
	require.Equal(t, stabilizationUnprovable, exported[0].ChannelAutomation,
		"a session credential does not prove a person rather than a script")
	require.NotEqual(t, "ui", exported[0].ChannelUIOrigin)
	require.NotEqual(t, "script", exported[0].ChannelAutomation)

	// A call without a bound principal is not a browser session either.
	other := decodeManualJudgement(t, submitManualJudgement(t, svc, owner, project.ID,
		"manual-judgement-0011-other", manualJudgementInput("f01-run-0011", "F01-06c")))
	require.Equal(t, manualJudgementChannelUnverified, other.Channel)

	exported, err = svc.ExportManualJudgements(context.Background(), owner, project.ID)
	require.NoError(t, err)
	require.Len(t, exported, 2)
	for _, entry := range exported {
		require.Equal(t, stabilizationUnprovable, entry.ChannelUIOrigin)
		require.Equal(t, stabilizationUnprovable, entry.ChannelAutomation)
	}
	var unbound bool
	for _, entry := range exported {
		if entry.ChannelClass == stabilizationChannelUnbound {
			unbound = true
		}
	}
	require.True(t, unbound, "a call without a bound principal must not be dressed up as a UI session")
}

// submitManualJudgement submits one judgement and returns the raw response.
func submitManualJudgement(t *testing.T, svc *Service, actor Actor, projectID, key string, input ManualJudgementInput, ctx ...context.Context) json.RawMessage {
	t.Helper()
	call := context.Background()
	if len(ctx) > 0 {
		call = ctx[0]
	}
	raw, _, _, err := svc.SubmitManualJudgement(call, actor, projectID, key, input)
	require.NoError(t, err)
	return raw
}
