package workspacecore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/lingdoc/operationcore"
	"github.com/google/uuid"
)

// The authoritative manual judgement (#649).
//
// #633 gave the runner a ledger of human judgements and three tiers of
// authority. What it could not do was make any of them authoritative: the
// runner proves a judgement against a SERVER-SIDE audit record, field by
// field, and the server had no audit record that carried the seven facts a
// judgement is made of. A judgement could therefore only ever stay "claimed",
// and the four prose assertions of the F01 contract could never be satisfied.
//
// This file is that record. It has two halves that must never drift apart:
//
//   - the write side, which derives every fact it can prove (who acted, how
//     they were authorized, through which channel, when, and under which
//     audit id) and stores the judgement and its audit row in ONE
//     transaction;
//   - the read side, which projects the stored audit rows into the shape the
//     runner compares, byte for byte.
//
// Four things this record must never say:
//
//   - that the caller was a person at a keyboard. The server proves a
//     CREDENTIAL class, not a human presence, so the flat `channel` is
//     "unverified_channel" and the two layers below it say what is provable.
//   - that a machine evaluated the judgement. The verdict came from a human,
//     `verdict_source` says so, and no machine path writes this record type.
//   - that the caller may declare its own authorization. The actor, the
//     authorization and the channel are the server's statements about the
//     call, never the caller's claims about itself.
//   - that a judgement may be rewritten. One assertion of one batch is
//     judged once; a second statement about it either replays or conflicts.
//
// The judgement is a statement ABOUT a project, not a change OF it: it does
// not move a revision, and it is not a write operation the pipeline treats
// as a project mutation. It is a new kind of audit record, and the record is
// what makes it authoritative.

const (
	// operationManualJudgement is this domain's own operation name. It lives
	// here rather than in typed_contracts.go, which belongs to another
	// surface, and it is deliberately NOT in the workspace's human-decision
	// list: the pipeline applies that list, so this operation's gate is
	// applied here, explicitly, before anything is opened or written.
	operationManualJudgement operationName = "manualJudgement"

	// operationOutputManualJudgement is the kind of output this operation
	// writes. The pipeline's own table does not know this name, so it is
	// declared here next to the operation that produces it.
	operationOutputManualJudgement operationOutputKind = "manual_judgement"

	// manualJudgementAuditAction is the audit action a manual judgement
	// writes. It is a new KIND of record, not another write operation: the
	// judgement does not change the project, it states a fact about one.
	manualJudgementAuditAction = "manual.judgement"

	// manualJudgementRecordKind is the kind of the project record that holds
	// the judgement result itself. The judgement and its audit row are two
	// views of one fact, and they are written in one transaction.
	manualJudgementRecordKind = "manual_judgement"

	// manualJudgementVerdictSourceHuman is the only verdict source this
	// record type may carry. A machine evaluation has its own record types
	// and must never write this one.
	manualJudgementVerdictSourceHuman = "human_judgement"

	// manualJudgementChannelUnverified is the flat channel the judgement
	// carries. It is not "ui" and not "api" and not "script": the server
	// cannot tell a browser click from a script replaying the same session
	// credential, so it says the one thing it knows - that the channel is
	// unverified - and puts what it CAN prove into the two layers below it.
	manualJudgementChannelUnverified = "unverified_channel"

	// manualJudgementStatusJudged and manualJudgementStatusUnjudged are the
	// two answers the domain may give about one assertion of one batch.
	// There is no third answer and no default: an assertion nobody judged is
	// unjudged, and it may not be filled in on the reader's behalf.
	manualJudgementStatusJudged   = "judged"
	manualJudgementStatusUnjudged = "unjudged"
)

// manualJudgementVerdicts are the verdicts a human may reach.
//
// They are the runner's vocabulary (#633), not this domain's invention: the
// runner refuses anything else, so the server refuses it here rather than
// recording a judgement no runner could ever read.
var manualJudgementVerdicts = map[string]bool{
	"passed":   true,
	"held":     true,
	"rejected": true,
}

// manualJudgementAuditFields are the seven facts the runner compares, field
// by field, to decide whether a judgement is authoritative.
//
// They are the FLAT top-level keys of the audit row's details, so the export
// is a projection and not a lookup: there is no nesting to disagree about
// and no second spelling of the same fact.
var manualJudgementAuditFields = []string{
	"actor",
	"authorization",
	"channel",
	"occurred_at",
	"assertion_id",
	"run_id",
	"evidence_ref",
}

// manualJudgementChannelFields are the two-layer channel facts.
//
// The flat `channel` says what the judgement's channel could not be pinned
// down to; these say what it could. They are separate keys because they are
// separate claims: a credential class is a fact, a UI origin is a fact the
// server does not have.
var manualJudgementChannelFields = []string{
	"channel_class",
	"channel_ui_origin",
	"channel_automation",
}

// ManualJudgementInput is what the Owner submits.
//
// These five fields are the CALLER's content and nothing else. Everything
// else about the judgement - who acted, how they were authorized, through
// which channel, when, and under which audit id - is derived by the server.
// A caller that names its own actor, its own authorization or its own
// timestamp is describing itself, and the server does not read those fields
// because they are not there.
type ManualJudgementInput struct {
	// AssertionID names the prose assertion the Owner judged.
	AssertionID string `json:"assertion_id"`
	// AssertionText is the prose the Owner judged, quoted verbatim.
	AssertionText string `json:"assertion_text"`
	// RunID names the batch this judgement belongs to.
	RunID string `json:"run_id"`
	// Verdict is what the Owner concluded.
	Verdict string `json:"verdict"`
	// EvidenceRef is the artefact of that batch the judgement rests on.
	EvidenceRef string `json:"evidence_ref"`
}

// ManualJudgementRecord is the authoritative judgement: the ten fields the
// runner's ledger writes.
//
// The seven the runner compares against the audit record are the server's
// statements, and the export reads the very same strings back out of the
// audit row, so the two cannot disagree.
type ManualJudgementRecord struct {
	RunID         string `json:"run_id"`
	AssertionID   string `json:"assertion_id"`
	AssertionText string `json:"assertion_text"`
	Verdict       string `json:"verdict"`
	Actor         string `json:"actor"`
	Authorization string `json:"authorization"`
	Channel       string `json:"channel"`
	OccurredAt    string `json:"occurred_at"`
	EvidenceRef   string `json:"evidence_ref"`
	AuditID       string `json:"audit_id"`
}

// ManualJudgementAudit is one audit row projected into the shape the runner
// compares.
//
// Every value is the string that was stored when the judgement was written,
// read back byte for byte. Nothing here is recomputed: a projection that
// recomputed a timestamp would lose the byte equality the runner needs.
//
// The quote the judgement rests on travels with it.  The runner does not
// compare it - it compares seven facts, not the prose - but an auditor
// reading an exported record has to be able to see WHICH sentence the
// judgement was made about, without opening the audit row's details.
type ManualJudgementAudit struct {
	AuditID           string `json:"audit_id"`
	Actor             string `json:"actor"`
	Authorization     string `json:"authorization"`
	Channel           string `json:"channel"`
	OccurredAt        string `json:"occurred_at"`
	AssertionID       string `json:"assertion_id"`
	RunID             string `json:"run_id"`
	EvidenceRef       string `json:"evidence_ref"`
	AssertionText     string `json:"assertion_text"`
	ChannelClass      string `json:"channel_class"`
	ChannelUIOrigin   string `json:"channel_ui_origin"`
	ChannelAutomation string `json:"channel_automation"`
}

// manualJudgementStored is the judgement result the project keeps.
//
// It is the same fact as the audit row, in the place the domain reads it
// from. The occurred_at it carries is the very string the audit row carries,
// not a re-formatted timestamp.
type manualJudgementStored struct {
	ID            string    `json:"id"`
	ProjectID     string    `json:"project_id"`
	RunID         string    `json:"run_id"`
	AssertionID   string    `json:"assertion_id"`
	AssertionText string    `json:"assertion_text"`
	Verdict       string    `json:"verdict"`
	VerdictSource string    `json:"verdict_source"`
	EvidenceRef   string    `json:"evidence_ref"`
	Actor         string    `json:"actor"`
	Authorization string    `json:"authorization"`
	Channel       string    `json:"channel"`
	OccurredAt    string    `json:"occurred_at"`
	AuditID       string    `json:"audit_id"`
	ContentHash   string    `json:"content_hash"`
	CreatedAt     time.Time `json:"created_at"`
}

// manualJudgementChannel is the two-layer statement about the channel the
// judgement came through.
type manualJudgementChannel struct {
	// Class is the credential class of the caller, which is the most the
	// server can prove.
	Class string `json:"class"`
	// UIOrigin says whether the request can be attributed to the UI.
	UIOrigin string `json:"ui_origin"`
	// Automation says whether the request can be attributed to a person
	// rather than to a scheduled or scripted run.
	Automation string `json:"automation"`
}

// SubmitManualJudgement records one human judgement of one prose assertion.
//
// The refusals happen in this order, and the order is the point:
//
//  1. the human-decision gate, BEFORE any transaction is opened, so a
//     machine principal cannot leave a trace of having tried;
//  2. the input's own shape, including the batch binding;
//  3. the operation pipeline: the authorization, the idempotency ledger and
//     the audit row of the attempt;
//  4. inside the write transaction: the Owner rule, the business key, the
//     audit completeness gate, and finally the two writes.
//
// The two writes are the judgement result and its audit row. They happen in
// one transaction, in that order, so a failed audit row takes the judgement
// result with it: a judgement nobody can verify is not a judgement.
func (s *Service) SubmitManualJudgement(ctx context.Context, actor Actor, projectID, key string, input ManualJudgementInput) (json.RawMessage, int, bool, error) {
	// The human-decision gate comes first and outside every transaction. The
	// pipeline's own gate list is the workspace's, and this operation is not
	// on it, so the check is made here, explicitly, where a reader can see
	// it: a machine identity may not judge, and a refused attempt leaves
	// nothing behind - not even a rolled-back row.
	if err := RequireHumanDecision(ctx); err != nil {
		return nil, 0, false, err
	}
	if err := validateManualJudgementInput(input); err != nil {
		return nil, 0, false, err
	}
	// The target is the project, not the business key: the ledger identity
	// has to stay the same for two calls that share an idempotency key, so
	// that "same key, different body" is still one conflict rather than two
	// unrelated operations. The business key is enforced inside the write,
	// where the judgement itself lives.
	descriptor := operationDescriptor[ManualJudgementInput]{
		Name:    operationManualJudgement,
		Target:  projectID + "/manual-judgement",
		Key:     key,
		Input:   input,
		Request: authorizationRequest(actor, projectID, AuthorizationManage, ""),
		Output:  operationOutputManualJudgement,
	}
	return runOperation(s, ctx, actor, descriptor, mutationPort[Transaction], func(tx Transaction, p Project) (operationResult[ManualJudgementRecord], error) {
		return s.writeManualJudgement(ctx, tx, actor, p, input)
	})
}

// writeManualJudgement performs the write of one manual judgement.
//
// It runs inside the operation's transaction, so the judgement result and
// its audit row commit together or not at all.
func (s *Service) writeManualJudgement(ctx context.Context, tx Transaction, actor Actor, p Project, input ManualJudgementInput) (operationResult[ManualJudgementRecord], error) {
	// Only the project's Owner may judge. The capability check that the
	// pipeline already ran says the caller may MANAGE the project; an
	// administrator may, and an administrator is not the Owner. A judgement
	// speaks for the project, so the Owner rule is separate and stated here.
	if !projectOwner(p, actor.UserID) {
		return operationResult[ManualJudgementRecord]{}, ErrNotFound
	}
	if p.Status != "active" || p.DiscardedAt != nil {
		return operationResult[ManualJudgementRecord]{}, ErrInvalidState
	}
	store, ok := tx.(projectRecordTransaction)
	if !ok {
		return operationResult[ManualJudgementRecord]{}, ErrInvalidState
	}
	// The business key is (project, run, assertion). One assertion of one
	// batch is judged once.
	existing, err := readManualJudgement(store, p.ID, input.RunID, input.AssertionID)
	if err == nil {
		// The judgement is not rewritten. A second statement about the same
		// assertion either says the same thing (it replays) or says
		// something else (it conflicts).
		if existing.AssertionText != input.AssertionText || existing.EvidenceRef != input.EvidenceRef {
			return operationResult[ManualJudgementRecord]{}, ErrInvalidRequest
		}
		if existing.ContentHash != manualJudgementContentHash(input) {
			return operationResult[ManualJudgementRecord]{}, ErrIdempotencyConflict
		}
		return operationResult[ManualJudgementRecord]{Value: existing.record(), Status: operationStatusOK}, nil
	}
	if err != ErrNotFound {
		return operationResult[ManualJudgementRecord]{}, err
	}

	now := time.Now().UTC()
	// The timestamp is a STRING from here on. It is not a time.Time that is
	// formatted on the way out: a timestamp that is re-formatted loses its
	// bytes on the way through the database, and the runner compares the
	// judgement and the audit record byte for byte.
	occurredAt := now.Format(time.RFC3339)
	auditID := uuid.NewString()
	channel := manualJudgementChannelFacts(ctx)
	authorization := manualJudgementAuthorization()

	details := map[string]any{
		// The seven facts the runner compares. Flat, string-keyed, at the
		// top level of the details.
		"actor":         actor.UserID,
		"authorization": authorization,
		"channel":       manualJudgementChannelUnverified,
		"occurred_at":   occurredAt,
		"assertion_id":  input.AssertionID,
		"run_id":        input.RunID,
		"evidence_ref":  input.EvidenceRef,
		// The two layers of the channel: what the server can prove, and
		// what it cannot.
		"channel_class":      channel.Class,
		"channel_ui_origin":  channel.UIOrigin,
		"channel_automation": channel.Automation,
		// The judgement's own content, so a reader of the audit row does
		// not have to look the judgement up somewhere else to know what was
		// concluded.
		"verdict":        input.Verdict,
		"verdict_source": manualJudgementVerdictSourceHuman,
		"assertion_text": input.AssertionText,
	}
	// The hard gate. An audit row that cannot say who acted, how they were
	// authorized, through which channel and when is not a weaker audit, it
	// is a missing one - and a judgement nobody can check is not a
	// judgement. Refuse rather than record one.
	if !manualJudgementAuditComplete(details) {
		return operationResult[ManualJudgementRecord]{}, ErrInvalidState
	}

	stored := manualJudgementStored{
		ID:            manualJudgementRecordID(input.RunID, input.AssertionID),
		ProjectID:     p.ID,
		RunID:         input.RunID,
		AssertionID:   input.AssertionID,
		AssertionText: input.AssertionText,
		Verdict:       input.Verdict,
		VerdictSource: manualJudgementVerdictSourceHuman,
		EvidenceRef:   input.EvidenceRef,
		Actor:         manualJudgementDetail(details, "actor"),
		Authorization: manualJudgementDetail(details, "authorization"),
		Channel:       manualJudgementDetail(details, "channel"),
		OccurredAt:    manualJudgementDetail(details, "occurred_at"),
		AuditID:       auditID,
		ContentHash:   manualJudgementContentHash(input),
		CreatedAt:     now,
	}
	// The judgement result first, the audit row second. Both are in this
	// transaction, so a failed audit row rolls the judgement result back
	// with it and nothing half-written survives.
	if err := store.PutProjectRecord(p.ID, manualJudgementRecordKind, stored.ID, 0, stored); err != nil {
		return operationResult[ManualJudgementRecord]{}, err
	}
	audit, ok := tx.(auditTransaction)
	if !ok {
		return operationResult[ManualJudgementRecord]{}, ErrInvalidState
	}
	if err := audit.RecordAudit(AuditEvent{
		ID:              auditID,
		TenantID:        actor.TenantID,
		ProjectID:       p.ID,
		UserID:          actor.UserID,
		ActorID:         actor.UserID,
		Role:            actor.Role,
		Action:          manualJudgementAuditAction,
		Target:          input.AssertionID,
		ProjectRevision: p.CurrentContextRevision,
		Capability:      string(AuthorizationManage),
		Decision:        input.Verdict,
		Details:         details,
		CreatedAt:       now,
	}); err != nil {
		return operationResult[ManualJudgementRecord]{}, err
	}
	return operationResult[ManualJudgementRecord]{Value: stored.record(), Status: operationStatusCreated}, nil
}

// ExportManualJudgements projects the project's manual judgement audit rows
// into the shape the runner compares.
//
// The projection is a copy, not a recomputation: every value is the string
// that was stored when the judgement was written. A row that does not carry
// every fact is refused rather than exported half-read - a partial record
// would compare unequal and leave the judgement "claimed" without saying
// why.
func (s *Service) ExportManualJudgements(ctx context.Context, actor Actor, projectID string) ([]ManualJudgementAudit, error) {
	result := []ManualJudgementAudit{}
	err := s.repository.Transaction(ctx, TransactionOptions{ReadOnly: true}, func(tx Transaction) error {
		if _, err := s.authorizeProject(ctx, tx, authorizationRequest(actor, projectID, AuthorizationRead, "")); err != nil {
			return err
		}
		audit, ok := tx.(auditTransaction)
		if !ok {
			return ErrInvalidState
		}
		events, err := audit.AuditEvents(projectID)
		if err != nil {
			return err
		}
		for _, event := range events {
			if event.Action != manualJudgementAuditAction {
				continue
			}
			projection, ok := manualJudgementAuditProjection(event)
			if !ok {
				return ErrInvalidState
			}
			result = append(result, projection)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// ManualJudgementStatus reports whether the project holds a human judgement
// for one assertion of one batch.
//
// It never guesses. An assertion nobody judged is "unjudged" - not
// "passed", not "held", and not "failed by default". A judgement is a fact
// somebody stated, and the absence of one is not a fact about the assertion.
func (s *Service) ManualJudgementStatus(ctx context.Context, actor Actor, projectID, runID, assertionID string) (string, error) {
	status := manualJudgementStatusUnjudged
	err := s.repository.Transaction(ctx, TransactionOptions{ReadOnly: true}, func(tx Transaction) error {
		if _, err := s.authorizeProject(ctx, tx, authorizationRequest(actor, projectID, AuthorizationRead, "")); err != nil {
			return err
		}
		store, ok := tx.(projectRecordTransaction)
		if !ok {
			return ErrInvalidState
		}
		existing, err := readManualJudgement(store, projectID, runID, assertionID)
		if err == ErrNotFound {
			return nil
		}
		if err != nil {
			return err
		}
		// Only a record that says a HUMAN judged may be reported as judged.
		// A record of another kind is not a judgement, and reporting it as
		// one would put a verdict on an assertion that nobody gave.
		if existing.VerdictSource != manualJudgementVerdictSourceHuman {
			return ErrInvalidState
		}
		status = manualJudgementStatusJudged
		return nil
	})
	if err != nil {
		return "", err
	}
	return status, nil
}

// validateManualJudgementInput refuses an input the server cannot record.
//
// The batch binding is the interesting one: the evidence a judgement rests
// on must be THIS batch's evidence. A judgement that names another batch's
// artefact says nothing about this batch, and the runner refuses it too -
// but the server refuses it first, so the audit log never holds a judgement
// that was about somewhere else.
func validateManualJudgementInput(input ManualJudgementInput) error {
	if strings.TrimSpace(input.AssertionID) == "" ||
		strings.TrimSpace(input.AssertionText) == "" ||
		strings.TrimSpace(input.RunID) == "" ||
		strings.TrimSpace(input.Verdict) == "" ||
		strings.TrimSpace(input.EvidenceRef) == "" {
		return ErrInvalidRequest
	}
	if !manualJudgementVerdicts[input.Verdict] {
		return ErrInvalidRequest
	}
	if !strings.Contains(input.EvidenceRef, input.RunID) {
		return ErrInvalidRequest
	}
	return nil
}

// manualJudgementContentHash digests the CALLER's content.
//
// The server's own facts - when it happened, which audit id it got - are
// deliberately not part of it. They differ on every attempt by
// construction, so including them would make every replay look like a
// conflict.
func manualJudgementContentHash(input ManualJudgementInput) string {
	return operationcore.ValueHash(input)
}

// manualJudgementRecordID is the project record's id for one judgement.
//
// It is a digest of the business key rather than the key itself: the key is
// two caller-supplied strings, and joining them with a separator would let
// two different pairs collide on one record.
func manualJudgementRecordID(runID, assertionID string) string {
	digest := sha256.Sum256([]byte(runID + "\x00" + assertionID))
	return hex.EncodeToString(digest[:])
}

// manualJudgementChannelFacts states what the server can prove about the
// channel the judgement came through.
//
// The credential class comes from the same helper the stabilization audit
// uses, and it reads the RAW principal value out of the context rather than
// types.PrincipalFromContext - that helper manufactures a browser session
// out of a bare user id, which would make a direct domain call look exactly
// like a click.
//
// The two layers below it are pinned HERE, in one place, because they are a
// policy and not a reading. The server cannot tell a browser click from a
// script replaying the same session credential, and it cannot tell an
// interactive call from a scheduled one. Until something stronger than a
// credential exists, both are "unprovable": a record that understates what
// it cannot prove is safe, and one that overstates it is not.
func manualJudgementChannelFacts(ctx context.Context) manualJudgementChannel {
	channel := stabilizationChannel(ctx)
	return manualJudgementChannel{
		Class:      channel.Class,
		UIOrigin:   stabilizationUnprovable,
		Automation: stabilizationUnprovable,
	}
}

// manualJudgementAuthorization states the checks that let the caller act.
//
// It is the server's statement about the call, in the same shape the
// stabilization audit uses - the method that was taken, the capability that
// was asked for, the Owner rule, and the outcome of the human-decision
// gate. It is derived, never read from the request.
func manualJudgementAuthorization() string {
	return "method=project_membership_capability_and_owner_check" +
		";capability=" + string(AuthorizationManage) +
		";project_owner=true" +
		";human_decision_gate=passed_not_a_machine_principal"
}

// manualJudgementAuditComplete reports whether the details carry every fact
// the audit must have.
//
// It reads the DETAILS, not a struct, because the details are what is
// stored: a field that is present in a struct and missing from the stored
// JSON is exactly the half-written audit this gate exists to refuse.
func manualJudgementAuditComplete(details map[string]any) bool {
	for _, field := range manualJudgementAuditFields {
		if !manualJudgementDetailPresent(details, field) {
			return false
		}
	}
	for _, field := range manualJudgementChannelFields {
		if !manualJudgementDetailPresent(details, field) {
			return false
		}
	}
	return true
}

// manualJudgementDetailPresent reports whether a details key holds a
// non-empty string.
func manualJudgementDetailPresent(details map[string]any, field string) bool {
	value, ok := details[field].(string)
	return ok && strings.TrimSpace(value) != ""
}

// manualJudgementDetail reads one flat string out of the details.
func manualJudgementDetail(details map[string]any, field string) string {
	value, _ := details[field].(string)
	return value
}

// manualJudgementAuditProjection projects one audit event into the shape the
// runner compares.
//
// It reports false for a row that does not carry every fact, so the caller
// refuses the export rather than handing out a half-record.
func manualJudgementAuditProjection(event AuditEvent) (ManualJudgementAudit, bool) {
	if !manualJudgementAuditComplete(event.Details) {
		return ManualJudgementAudit{}, false
	}
	return ManualJudgementAudit{
		AuditID:           event.ID,
		Actor:             manualJudgementDetail(event.Details, "actor"),
		Authorization:     manualJudgementDetail(event.Details, "authorization"),
		Channel:           manualJudgementDetail(event.Details, "channel"),
		OccurredAt:        manualJudgementDetail(event.Details, "occurred_at"),
		AssertionID:       manualJudgementDetail(event.Details, "assertion_id"),
		RunID:             manualJudgementDetail(event.Details, "run_id"),
		EvidenceRef:       manualJudgementDetail(event.Details, "evidence_ref"),
		AssertionText:     manualJudgementDetail(event.Details, "assertion_text"),
		ChannelClass:      manualJudgementDetail(event.Details, "channel_class"),
		ChannelUIOrigin:   manualJudgementDetail(event.Details, "channel_ui_origin"),
		ChannelAutomation: manualJudgementDetail(event.Details, "channel_automation"),
	}, true
}

// readManualJudgement reads the judgement the project holds for one
// assertion of one batch.
func readManualJudgement(store projectRecordTransaction, projectID, runID, assertionID string) (manualJudgementStored, error) {
	var stored manualJudgementStored
	_, err := store.GetProjectRecord(projectID, manualJudgementRecordKind, manualJudgementRecordID(runID, assertionID), &stored)
	if err != nil {
		return manualJudgementStored{}, err
	}
	return stored, nil
}

// record renders the stored judgement as the authoritative record.
func (s manualJudgementStored) record() ManualJudgementRecord {
	return ManualJudgementRecord{
		RunID:         s.RunID,
		AssertionID:   s.AssertionID,
		AssertionText: s.AssertionText,
		Verdict:       s.Verdict,
		Actor:         s.Actor,
		Authorization: s.Authorization,
		Channel:       s.Channel,
		OccurredAt:    s.OccurredAt,
		EvidenceRef:   s.EvidenceRef,
		AuditID:       s.AuditID,
	}
}
