package templategovernance

// Replay fixture registration (Spec #768, ticket #769): one registration
// operation, one merged store, one shared coverage judgment. The template-path
// HTTP endpoint is a thin spelling of RegisterFixtures; shape validation,
// trial replay, the atomic batch insert and the audit receipt live only here.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/lingdoc/template"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// MaxReplayFixturesPerBatch bounds one registration request. The request body
// size is additionally bounded by the transport's decode limit; both bounds
// answer 400 invalid_request with zero writes.
const MaxReplayFixturesPerBatch = 200

// maxReplayTargetRefLength bounds the declared target reference
// ("<kind>/<identifier>" with the stable identifier lexicon).
const maxReplayTargetRefLength = 180

// maxReplayReviewReferenceLength bounds the human review evidence citation.
const maxReplayReviewReferenceLength = 255

// ReplayFixtureRecord is one server-registered reviewed fixture with the pin
// the server computed at registration time. Builtin demo fixtures and
// tenant-registered rows share this shape: the builtin set is answered by code
// only, the tenant set only by this tenant's stored rows.
type ReplayFixtureRecord struct {
	Fixture             ReplayFixture
	TemplateID          string
	TemplateVersion     string
	TemplateContentHash string
	PolicyHash          string
	ReviewedBy          string
	ReviewReference     string
	ReviewedAt          time.Time
}

// ErrFixtureNotStored marks a fixture id that is neither builtin nor
// registered by the requesting tenant. It is a store-internal answer: the
// replay surfaces translate it into ErrReplayRequired so a missing fixture is
// indistinguishable from a stale pin and never enumerates other tenants.
var ErrFixtureNotStored = errors.New("replay fixture not registered for this tenant")

// FixturePin is the immutable version identity a fixture is registered
// against. Readers only trust rows whose pin equals the current version pin.
type FixturePin struct {
	TemplateID      string
	TemplateVersion string
	ContentHash     string
	PolicyHash      string
}

// ReplayFixtureStore is the single merged read point over the two fixture
// sources: the code-defined builtin set (an in-memory backend of this store)
// and the tenant-registered rows. Get (the replay reader), the catalog listing
// and Verify's pin queries all go through this one implementation; there is no
// second merge.
//
// Two facts the code cannot show, and every implementation must keep:
//
//  1. The two sources are disjoint because the registration side rejects
//     fixture ids that collide with the builtin set (exact equality); the read
//     side therefore never checks merge order, and the order carries no
//     semantics.
//  2. When fixture storage is unavailable (table missing, migrations not run,
//     query failure) every read of the stored set fails closed with
//     ErrUnavailable. It must never silently degrade to the builtin-only
//     view: a publication decided on half the evidence is not a publication
//     the replay gate approved.
type ReplayFixtureStore interface {
	GetFixture(ctx context.Context, tenantID uint64, id string) (ReplayFixtureRecord, error)
	ListFixtures(ctx context.Context, tenantID uint64) ([]ReplayFixtureRecord, error)
	FixturesForPin(ctx context.Context, tenantID uint64, pin FixturePin) ([]ReplayFixtureRecord, error)
	// FixturesForPinInTransaction is the transaction-scoped twin of
	// FixturesForPin for the registration operation: the batch it just
	// inserted is only visible inside its own transaction, and the
	// single-connection SQLite pool cannot serve a second query while that
	// transaction is open.
	FixturesForPinInTransaction(tx *gorm.DB, tenantID uint64, pin FixturePin) ([]ReplayFixtureRecord, error)
	ConflictsBuiltin(id string) bool
	// GetReplayFixture adapts the merged store to the replay reader the
	// publication verifier consumes. A missing fixture is a replay
	// requirement; a storage failure keeps its own (fail-closed) error.
	GetReplayFixture(ctx context.Context, actor Actor, id string) (ReplayFixture, error)
}

// NewReplayFixtureStore builds the merged store over one database and the
// code-defined builtin records.
func NewReplayFixtureStore(db *gorm.DB, builtin []ReplayFixtureRecord) ReplayFixtureStore {
	seeded := make(map[string]ReplayFixtureRecord, len(builtin))
	for _, record := range builtin {
		if record.Fixture.ID == "" {
			continue
		}
		seeded[record.Fixture.ID] = record
	}
	return &mergedFixtureStore{db: db, builtin: seeded}
}

type mergedFixtureStore struct {
	db      *gorm.DB
	builtin map[string]ReplayFixtureRecord
}

func (s *mergedFixtureStore) GetFixture(ctx context.Context, tenantID uint64, id string) (ReplayFixtureRecord, error) {
	if record, ok := s.builtin[id]; ok {
		return record, nil
	}
	var row fixtureRow
	err := s.db.WithContext(ctx).Where("tenant_id=? AND fixture_id=?", tenantID, id).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ReplayFixtureRecord{}, ErrFixtureNotStored
	}
	if err != nil {
		return ReplayFixtureRecord{}, errors.Join(ErrUnavailable, err)
	}
	return fixtureRecordFromRow(row)
}

func (s *mergedFixtureStore) ListFixtures(ctx context.Context, tenantID uint64) ([]ReplayFixtureRecord, error) {
	var rows []fixtureRow
	if err := s.db.WithContext(ctx).Where("tenant_id=?", tenantID).Find(&rows).Error; err != nil {
		// Fail closed: a broken stored set must not silently shrink to the
		// builtin-only catalog.
		return nil, errors.Join(ErrUnavailable, err)
	}
	records := make([]ReplayFixtureRecord, 0, len(rows)+len(s.builtin))
	for _, row := range rows {
		record, err := fixtureRecordFromRow(row)
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	for _, record := range s.builtin {
		records = append(records, record)
	}
	sortReplayFixtureRecords(records)
	return records, nil
}

func (s *mergedFixtureStore) FixturesForPin(ctx context.Context, tenantID uint64, pin FixturePin) ([]ReplayFixtureRecord, error) {
	return s.fixturesForPin(ctx, s.db, tenantID, pin)
}

func (s *mergedFixtureStore) FixturesForPinInTransaction(tx *gorm.DB, tenantID uint64, pin FixturePin) ([]ReplayFixtureRecord, error) {
	return s.fixturesForPin(tx.Statement.Context, tx, tenantID, pin)
}

func (s *mergedFixtureStore) fixturesForPin(ctx context.Context, exec *gorm.DB, tenantID uint64, pin FixturePin) ([]ReplayFixtureRecord, error) {
	var rows []fixtureRow
	query := exec.WithContext(ctx).Where("tenant_id=? AND template_id=? AND version=? AND content_hash=? AND policy_hash=?", tenantID, pin.TemplateID, pin.TemplateVersion, pin.ContentHash, pin.PolicyHash)
	if err := query.Find(&rows).Error; err != nil {
		return nil, errors.Join(ErrUnavailable, err)
	}
	records := make([]ReplayFixtureRecord, 0, len(rows))
	for _, row := range rows {
		record, err := fixtureRecordFromRow(row)
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	for _, record := range s.builtin {
		if record.TemplateID == pin.TemplateID && record.TemplateVersion == pin.TemplateVersion && record.TemplateContentHash == pin.ContentHash && record.PolicyHash == pin.PolicyHash {
			records = append(records, record)
		}
	}
	sortReplayFixtureRecords(records)
	return records, nil
}

func (s *mergedFixtureStore) ConflictsBuiltin(id string) bool {
	_, ok := s.builtin[id]
	return ok
}

func (s *mergedFixtureStore) GetReplayFixture(ctx context.Context, actor Actor, id string) (ReplayFixture, error) {
	record, err := s.GetFixture(ctx, actor.TenantID, id)
	if errors.Is(err, ErrFixtureNotStored) {
		return ReplayFixture{}, ErrReplayRequired
	}
	if err != nil {
		return ReplayFixture{}, err
	}
	return record.Fixture, nil
}

// sortReplayFixtureRecords is the stable catalog order: fixture id ascending.
func sortReplayFixtureRecords(records []ReplayFixtureRecord) {
	sort.Slice(records, func(i, j int) bool { return records[i].Fixture.ID < records[j].Fixture.ID })
}

func fixtureRecordFromRow(row fixtureRow) (ReplayFixtureRecord, error) {
	var input template.EvaluationInput
	if err := json.Unmarshal([]byte(row.InputJSON), &input); err != nil {
		return ReplayFixtureRecord{}, fmt.Errorf("decode replay fixture input: %w", err)
	}
	return ReplayFixtureRecord{
		Fixture: ReplayFixture{
			ID:             row.FixtureID,
			RuleSet:        RuleSetRef{ID: row.RulesetID, Version: row.RulesetVersion, Hash: row.RulesetHash},
			RuleID:         row.RuleID,
			Scene:          row.Scene,
			Polarity:       row.Polarity,
			TargetRef:      row.TargetRef,
			Input:          input,
			ExpectedStatus: template.EvaluationStatus(row.ExpectedStatus),
		},
		TemplateID:          row.TemplateID,
		TemplateVersion:     row.Version,
		TemplateContentHash: row.ContentHash,
		PolicyHash:          row.PolicyHash,
		ReviewedBy:          row.ReviewedBy,
		ReviewReference:     row.ReviewReference,
		ReviewedAt:          row.ReviewedAt,
	}, nil
}

// ReplayFixtureInput is one fixture of a registration batch. It carries only
// semantic fields: the ruleset reference, the content hash and the policy hash
// are computed by the server from the registered version (D4), and any
// client-declared hash field is a shape error because the input struct has no
// such field at all.
type ReplayFixtureInput struct {
	FixtureID       string                    `json:"fixture_id"`
	RuleID          string                    `json:"rule_id"`
	Scene           string                    `json:"scene"`
	Polarity        string                    `json:"polarity"`
	TargetRef       string                    `json:"target_ref"`
	Input           template.EvaluationInput  `json:"input"`
	ExpectedStatus  template.EvaluationStatus `json:"expected_status"`
	ReviewReference string                    `json:"review_reference"`
}

// RegisterFixturesInput is one batch against one draft template version. The
// batch is atomic: every fixture is validated and trialed before any row is
// written, and a single failure leaves zero rows.
type RegisterFixturesInput struct {
	TemplateID string               `json:"template_id"`
	Version    string               `json:"version"`
	Fixtures   []ReplayFixtureInput `json:"fixtures"`
	Reason     string               `json:"reason"`
}

// RegisteredFixtureSummary is one registered fixture's metadata receipt.
type RegisteredFixtureSummary struct {
	FixtureID string     `json:"fixture_id"`
	RuleID    string     `json:"rule_id"`
	Scene     string     `json:"scene"`
	Polarity  string     `json:"polarity"`
	TargetRef string     `json:"target_ref"`
	RuleSet   RuleSetRef `json:"ruleset"`
}

// RegisterFixturesReceipt states the server-fixed pin, what was registered,
// and the shared coverage matrix of the version after this batch — the same
// computation the coverage readback and publication Verify consume.
type RegisterFixturesReceipt struct {
	TemplateID  string                     `json:"template_id"`
	Version     string                     `json:"version"`
	ContentHash string                     `json:"content_hash"`
	PolicyHash  string                     `json:"policy_hash"`
	Registered  []RegisteredFixtureSummary `json:"registered"`
	Coverage    ReplayCoverageReport       `json:"coverage"`
}

// RegisterFixtures is the single registration operation (Spec #768 D3/D10).
// Within one transaction it rechecks current authorization and membership,
// loads the stored draft version, computes the server-fixed pin, validates
// every fixture's shape and semantics, trials every fixture with the same
// judge publication Verify uses, inserts the whole batch append-only and
// records the success audit. Any failure rolls the transaction back (zero
// writes); the failure receipt is written afterwards in its own bounded
// transaction. Idempotency follows the governance operation table: the same
// key with the same request replays the same receipt, the same key with a
// different request is ErrIdempotencyConflict.
func (s *Service) RegisterFixtures(ctx context.Context, actor Actor, key string, input RegisterFixturesInput) (receipt RegisterFixturesReceipt, err error) {
	defer func() {
		if auditErr := s.auditCatalogFailure(ctx, actor, ActionRegisterFixture, input.TemplateID, input.Version, input.Reason, err); auditErr != nil {
			err = errors.Join(err, fmt.Errorf("record catalog failure receipt: %w", auditErr))
		}
	}()
	if err := s.ready(actor); err != nil {
		return RegisterFixturesReceipt{}, err
	}
	if !validKey(key) {
		return RegisterFixturesReceipt{}, ErrInvalidRequest
	}
	if s.ports.Fixtures == nil {
		// Fail closed: without the fixture store there is no registration
		// channel and no silent builtin-only substitute.
		return RegisterFixturesReceipt{}, ErrUnavailable
	}
	if err := validateRegisterFixturesInput(input); err != nil {
		return RegisterFixturesReceipt{}, err
	}
	for _, fixture := range input.Fixtures {
		if s.ports.Fixtures.ConflictsBuiltin(fixture.FixtureID) {
			// The builtin set is code-defined; a tenant row can never claim
			// one of its identities (D4).
			return RegisterFixturesReceipt{}, ErrInvalidRequest
		}
	}
	err = s.withTx(ctx, func(tx *gorm.DB) error {
		if err := s.ports.Authorizer.AuthorizeTemplate(ctx, tx, actor, ActionRegisterFixture, input.TemplateID); err != nil {
			return err
		}
		current, err := loadVersion(tx, actor, input.TemplateID, input.Version)
		if err != nil {
			return err
		}
		if current.State != template.RuleSetDraft {
			return ErrInvalidState
		}
		op := operationIdentity(actor, "register_fixture", input.TemplateID+"/"+input.Version, key)
		found, err := operationGet(tx, op, input, &receipt)
		if err != nil {
			return err
		}
		if found {
			if current.ContentHash != receipt.ContentHash || current.PolicyHash != receipt.PolicyHash {
				// The draft moved since this receipt was minted; its pin no
				// longer answers the current version.
				return ErrVersionConflict
			}
			return nil
		}
		newReceipt, trialErr := s.trialAndInsert(ctx, tx, actor, op, current, input)
		if trialErr != nil {
			return trialErr
		}
		receipt = newReceipt
		return nil
	})
	return receipt, err
}

// trialAndInsert runs the batch: server-fixed pin, per-fixture 400 boundary
// then trial replay (422 family), then the append-only insert. It runs inside
// the caller's transaction and returns the receipt to store as the
// operation's replayable result.
func (s *Service) trialAndInsert(_ context.Context, tx *gorm.DB, actor Actor, op operationRow, current TemplateVersion, input RegisterFixturesInput) (RegisterFixturesReceipt, error) {
	receipt := RegisterFixturesReceipt{
		TemplateID:  current.TemplateID,
		Version:     current.Version,
		ContentHash: current.ContentHash,
		PolicyHash:  current.PolicyHash,
		Registered:  make([]RegisteredFixtureSummary, 0, len(input.Fixtures)),
		Coverage:    ReplayCoverageReport{Entries: []ReplayCoverageEntry{}},
	}
	rows := make([]fixtureRow, 0, len(input.Fixtures))
	seen := map[string]bool{}
	now := time.Now().UTC()
	for _, in := range input.Fixtures {
		if seen[in.FixtureID] {
			return RegisterFixturesReceipt{}, ErrInvalidRequest
		}
		seen[in.FixtureID] = true
		if err := validateFixtureSemantics(current, in); err != nil {
			return RegisterFixturesReceipt{}, err
		}
		fixture := ReplayFixture{
			ID:             in.FixtureID,
			RuleSet:        current.RuleSet,
			RuleID:         in.RuleID,
			Scene:          in.Scene,
			Polarity:       in.Polarity,
			TargetRef:      in.TargetRef,
			Input:          in.Input,
			ExpectedStatus: in.ExpectedStatus,
		}
		// Registration-time trial replay (D5): the same judge publication
		// Verify runs. Everything it can still reject required the real
		// evaluator, so it is the 422 replay evidence family (D6).
		if err := JudgeReplayFixture(current, fixture); err != nil {
			return RegisterFixturesReceipt{}, err
		}
		inputJSON, err := jsonText(in.Input)
		if err != nil {
			return RegisterFixturesReceipt{}, err
		}
		rows = append(rows, fixtureRow{
			TenantID: actor.TenantID, TemplateID: current.TemplateID, Version: current.Version,
			ContentHash: current.ContentHash, FixtureID: in.FixtureID,
			RulesetID: current.RuleSet.ID, RulesetVersion: current.RuleSet.Version, RulesetHash: current.RuleSet.Hash,
			PolicyHash: current.PolicyHash, RuleID: in.RuleID, Scene: in.Scene, Polarity: in.Polarity,
			TargetRef: in.TargetRef, InputJSON: inputJSON, ExpectedStatus: string(in.ExpectedStatus),
			ReviewedBy: actor.UserID, ReviewReference: in.ReviewReference, ReviewedAt: now,
			CreatedBy: actor.UserID, CreatedAt: now,
		})
		receipt.Registered = append(receipt.Registered, RegisteredFixtureSummary{
			FixtureID: in.FixtureID, RuleID: in.RuleID, Scene: in.Scene, Polarity: in.Polarity, TargetRef: in.TargetRef, RuleSet: current.RuleSet,
		})
	}
	for i := range rows {
		if err := insertFixtureRow(tx, rows[i]); err != nil {
			return RegisterFixturesReceipt{}, err
		}
	}
	// The receipt's coverage is the shared matrix over the full merged set
	// bound to this pin — the exact set publication Verify will consume.
	records, err := s.ports.Fixtures.FixturesForPinInTransaction(tx, actor.TenantID, FixturePin{
		TemplateID: current.TemplateID, TemplateVersion: current.Version, ContentHash: current.ContentHash, PolicyHash: current.PolicyHash,
	})
	if err != nil {
		return RegisterFixturesReceipt{}, err
	}
	fixtures := make([]ReplayFixture, 0, len(records))
	for _, record := range records {
		fixtures = append(fixtures, record.Fixture)
	}
	receipt.Coverage = JudgeReplayCoverage(current, fixtures)
	ids := make([]string, 0, len(receipt.Registered))
	for _, summary := range receipt.Registered {
		ids = append(ids, summary.FixtureID)
	}
	if err := recordAudit(tx, actor, ActionRegisterFixture, op.Target, input.Reason, map[string]any{
		"fixture_ids": ids, "content_hash": current.ContentHash, "policy_hash": current.PolicyHash, "ruleset": current.RuleSet,
	}); err != nil {
		return RegisterFixturesReceipt{}, err
	}
	return receipt, operationSave(tx, op, input, receipt, map[string]any{"content_hash": current.ContentHash, "policy_hash": current.PolicyHash, "count": len(rows)})
}

// insertFixtureRow appends one row. A primary-key collision is an append-only
// refusal: identical content means the fixture is already registered (the
// batch no-ops), different content under the same identity is never rewritten.
func insertFixtureRow(tx *gorm.DB, row fixtureRow) error {
	created := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&row)
	if created.Error != nil {
		return created.Error
	}
	if created.RowsAffected == 1 {
		return nil
	}
	var existing fixtureRow
	if err := tx.Where("tenant_id=? AND template_id=? AND version=? AND content_hash=? AND fixture_id=?", row.TenantID, row.TemplateID, row.Version, row.ContentHash, row.FixtureID).Take(&existing).Error; err != nil {
		return err
	}
	if sameFixtureContent(existing, row) {
		return nil
	}
	return ErrVersionConflict
}

func sameFixtureContent(existing, next fixtureRow) bool {
	return existing.RulesetID == next.RulesetID && existing.RulesetVersion == next.RulesetVersion &&
		existing.RulesetHash == next.RulesetHash && existing.PolicyHash == next.PolicyHash &&
		existing.RuleID == next.RuleID && existing.Scene == next.Scene && existing.Polarity == next.Polarity &&
		existing.TargetRef == next.TargetRef && existing.InputJSON == next.InputJSON &&
		existing.ExpectedStatus == next.ExpectedStatus && existing.ReviewReference == next.ReviewReference
}

// validateRegisterFixturesInput is the request-level 400 boundary: identifiers,
// batch bounds and per-fixture field shapes that need no rule set knowledge.
func validateRegisterFixturesInput(input RegisterFixturesInput) error {
	if !identifier.MatchString(input.TemplateID) || !identifier.MatchString(input.Version) || !validReason(input.Reason) {
		return ErrInvalidRequest
	}
	if len(input.Fixtures) == 0 || len(input.Fixtures) > MaxReplayFixturesPerBatch {
		return ErrInvalidRequest
	}
	for _, fixture := range input.Fixtures {
		if !identifier.MatchString(fixture.FixtureID) || !identifier.MatchString(fixture.RuleID) {
			return ErrInvalidRequest
		}
		if len(fixture.TargetRef) == 0 || len(fixture.TargetRef) > maxReplayTargetRefLength {
			return ErrInvalidRequest
		}
		if strings.TrimSpace(fixture.ReviewReference) == "" || len(fixture.ReviewReference) > maxReplayReviewReferenceLength {
			return ErrInvalidRequest
		}
	}
	return nil
}

// validateFixtureSemantics is the rule-set-aware 400 boundary (D6): everything
// decidable without running the evaluator — the rule must be an enabled
// binding of the version's snapshot, the scene one of its trigger scenes, and
// the expected outcome must match the binding's definition class.
func validateFixtureSemantics(current TemplateVersion, in ReplayFixtureInput) error {
	binding, ok := bindingsByID(current)[in.RuleID]
	if !ok || !binding.Enabled {
		return ErrInvalidRequest
	}
	if in.Polarity != "positive" && in.Polarity != "negative" {
		return ErrInvalidRequest
	}
	if !contains(binding.TriggerScenes, in.Scene) {
		return ErrInvalidRequest
	}
	switch in.ExpectedStatus {
	case template.EvaluationPass, template.EvaluationIssue, template.EvaluationUnknown:
	default:
		return ErrInvalidRequest
	}
	if binding.DefinitionID != "model_assisted" {
		if in.Polarity == "positive" && in.ExpectedStatus != template.EvaluationPass {
			return ErrInvalidRequest
		}
		if in.Polarity == "negative" && in.ExpectedStatus != template.EvaluationIssue {
			return ErrInvalidRequest
		}
	} else if in.ExpectedStatus != template.EvaluationUnknown {
		return ErrInvalidRequest
	}
	return nil
}

// ReplayCoverageView is the coverage readback for one stored version: the
// version identity it describes plus the shared matrix.
type ReplayCoverageView struct {
	TemplateID  string `json:"template_id"`
	Version     string `json:"version"`
	ContentHash string `json:"content_hash"`
	PolicyHash  string `json:"policy_hash"`
	ReplayCoverageReport
}

// ReplayCoverage is the manager-only coverage readback (D9). The draft's
// coverage matrix is draft metadata: any current tenant manager may read it,
// nobody else — non-managers get the anti-enumerating ErrNotFound. The
// fixture read happens outside the authorization transaction so the store's
// query never competes for the single-connection SQLite pool; the view names
// the content hash it describes.
func (s *Service) ReplayCoverage(ctx context.Context, actor Actor, templateID, version string) (ReplayCoverageView, error) {
	if err := s.ready(actor); err != nil {
		return ReplayCoverageView{}, err
	}
	if !identifier.MatchString(templateID) || !identifier.MatchString(version) {
		return ReplayCoverageView{}, ErrInvalidRequest
	}
	if s.ports.Fixtures == nil {
		return ReplayCoverageView{}, ErrUnavailable
	}
	var current TemplateVersion
	if err := s.withTx(ctx, func(tx *gorm.DB) error {
		if err := s.ports.Authorizer.AuthorizeTemplate(ctx, tx, actor, ActionRead, templateID); err != nil {
			return err
		}
		canManage, err := s.ports.Authorizer.CanManageTemplateCatalog(ctx, tx, actor)
		if err != nil {
			return err
		}
		if !canManage {
			return ErrNotFound
		}
		current, err = loadVersion(tx, actor, templateID, version)
		return err
	}); err != nil {
		return ReplayCoverageView{}, err
	}
	records, err := s.ports.Fixtures.FixturesForPin(ctx, actor.TenantID, FixturePin{
		TemplateID: templateID, TemplateVersion: version, ContentHash: current.ContentHash, PolicyHash: current.PolicyHash,
	})
	if err != nil {
		return ReplayCoverageView{}, err
	}
	fixtures := make([]ReplayFixture, 0, len(records))
	for _, record := range records {
		fixtures = append(fixtures, record.Fixture)
	}
	return ReplayCoverageView{
		TemplateID:           templateID,
		Version:              version,
		ContentHash:          current.ContentHash,
		PolicyHash:           current.PolicyHash,
		ReplayCoverageReport: JudgeReplayCoverage(current, fixtures),
	}, nil
}
