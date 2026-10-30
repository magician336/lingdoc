package generation

import (
	"context"
	"errors"
	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
	"github.com/Tencent/WeKnora/internal/types"
	"testing"
)

type testEvidenceValidator struct {
	denied bool
	calls  int
}

type legacyOnlyEvidenceSourceValidator struct{}

func (legacyOnlyEvidenceSourceValidator) ValidateGenerationSources(context.Context, Actor, string, []Source) error {
	return nil
}

type revokingEvidenceSourceValidator struct {
	denied bool
	calls  int
}

func (v *revokingEvidenceSourceValidator) ValidateGenerationSources(context.Context, Actor, string, []Source) error {
	return errors.New("evidence path must use evidence-aware source validation")
}

func (v *revokingEvidenceSourceValidator) ValidateGenerationSourcesWithEvidence(_ context.Context, _ Actor, _, chapter string, refs []EvidenceCardRef, _ []Source) error {
	v.calls++
	if v.denied || chapter != "chapter-1" || len(refs) != 1 || refs[0].ID != "card" || refs[0].Version != 2 {
		return ErrSourceAccessDenied
	}
	return nil
}

type revokeSourceDuringModel struct {
	validator *revokingEvidenceSourceValidator
}

func (*revokeSourceDuringModel) RunMode() string { return "mock" }
func (m *revokeSourceDuringModel) Generate(context.Context, Input) (Draft, error) {
	m.validator.denied = true
	return Draft{BodyMarkdown: "候选 [[source:source-1]]", Sources: []Source{{ID: "source-1"}}}, nil
}

func TestGenerationEvidenceAwareSourceRechecksAfterModel(t *testing.T) {
	service, repo := evidenceFixture()
	repo.run = Run{}
	service.Evidence = &testEvidenceValidator{}
	sources := &revokingEvidenceSourceValidator{}
	service.Sources = sources
	service.Model = &revokeSourceDuringModel{validator: sources}
	run, err := service.Start(context.Background(), Actor{TenantID: 1, UserID: "user-1"}, "project-1", "source-revoked-during-model", evidenceStartRequest())
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Execute(context.Background(), run.ID)
	if err != nil || result.Status != StatusFailed || repo.candidate.ID != "" || sources.calls < 2 {
		t.Fatalf("revoked evidence source was published: result=%+v calls=%d err=%v", result, sources.calls, err)
	}
}

func TestGenerationEvidenceSourceRecheckRequiresBoundPersistedRefs(t *testing.T) {
	refs := []EvidenceCardRef{{ID: "card", Version: 2}}
	input := Input{EvidenceCardRefs: refs, Request: Request{EvidenceCardRefs: refs}}
	service := &Service{Sources: legacyOnlyEvidenceSourceValidator{}}
	if err := service.validateSources(context.Background(), Actor{}, "project", "chapter", input, nil); !errors.Is(err, ErrDependencyUnavailable) {
		t.Fatalf("missing evidence-aware validator did not fail closed: %v", err)
	}
	service.Sources = testSources{}
	input.Request.EvidenceCardRefs = []EvidenceCardRef{{ID: "card", Version: 3}}
	if err := service.validateSources(context.Background(), Actor{}, "project", "chapter", input, nil); !errors.Is(err, ErrSourceAccessDenied) {
		t.Fatalf("different request and persisted refs were accepted: %v", err)
	}
	input.EvidenceCardRefs = nil
	input.Request.EvidenceCardRefs = nil
	service.Sources = legacyOnlyEvidenceSourceValidator{}
	if err := service.validateSources(context.Background(), Actor{}, "project", "chapter", input, nil); err != nil {
		t.Fatalf("ordinary source path changed: %v", err)
	}
}

func (v *testEvidenceValidator) ValidateGenerationEvidence(_ context.Context, _ Actor, _, _ string, refs []EvidenceCardRef, sources []Source) error {
	v.calls++
	if v.denied || len(refs) != 1 || refs[0].ID != "card" || refs[0].Version != 2 || len(sources) != 1 {
		return ErrSourceAccessDenied
	}
	return nil
}

type testScopedAuthorizer struct {
	testAuthorizer
	denied  bool
	chapter string
}

func (a *testScopedAuthorizer) AuthorizeGenerationChapter(_ context.Context, _ Actor, _, chapter string) error {
	a.chapter = chapter
	if a.denied {
		return ErrForbidden
	}
	return nil
}

type evidenceRevokingModel struct {
	calls     int
	validator *testEvidenceValidator
}

func (m *evidenceRevokingModel) RunMode() string { return "mock" }

func (m *evidenceRevokingModel) Generate(_ context.Context, in Input) (Draft, error) {
	m.calls++
	m.validator.denied = true
	return Draft{BodyMarkdown: "候选 [[source:source-1]]", Sources: []Source{{ID: "source-1"}}}, nil
}
func evidenceStartRequest() Request {
	return Request{ChapterID: "chapter-1", AssetIDs: []string{}, EvidenceCardRefs: []EvidenceCardRef{{ID: "card", Version: 2}}, Instruction: "Draft", ExpectedSpecRevision: 3, ExpectedChapterVersionID: stringPtr("chapter-v1")}
}
func TestGenerationRefsOnlyQueuesImmutableRefsAndReplayRechecksBeforeEnqueue(t *testing.T) {
	service, repo := evidenceFixture()
	repo.run = Run{}
	validator := &testEvidenceValidator{}
	service.Evidence = validator
	request := evidenceStartRequest()
	actor := Actor{TenantID: 1, UserID: "user-1"}
	run, err := service.Start(context.Background(), actor, "project-1", "g2-card-queue", request)
	if err != nil || run.ID == "" || len(repo.input.Basis.AssetVersions) != 1 || len(repo.input.EvidenceCardRefs) != 1 {
		t.Fatalf("refs-only rejected: %+v %v", run, err)
	}
	request.EvidenceCardRefs[0].Version = 99
	if repo.input.EvidenceCardRefs[0].Version != 2 || repo.input.Request.EvidenceCardRefs[0].Version != 2 {
		t.Fatal("caller mutated queued immutable references")
	}
	validator.denied = true
	_, err = service.Start(context.Background(), actor, "project-1", "g2-card-queue", evidenceStartRequest())
	if !errors.Is(err, ErrSourceAccessDenied) || repo.createRunCalls != 1 {
		t.Fatalf("revoked replay accepted: %v creates=%d", err, repo.createRunCalls)
	}
}
func TestGenerationCardChangedBeforeWorkerNeverCallsModel(t *testing.T) {
	service, repo := evidenceFixture()
	repo.run = Run{}
	validator := &testEvidenceValidator{}
	service.Evidence = validator
	model := &countingModel{}
	service.Model = model
	run, err := service.Start(context.Background(), Actor{TenantID: 1, UserID: "user-1"}, "project-1", "g2-card-before-worker", evidenceStartRequest())
	if err != nil {
		t.Fatal(err)
	}
	validator.denied = true
	result, err := service.Execute(context.Background(), run.ID)
	if err != nil || result.Status != StatusFailed || model.calls != 0 || repo.candidate.ID != "" {
		t.Fatalf("stale evidence called model: %+v %v calls=%d", result, err, model.calls)
	}
}
func TestGenerationCardChangedDuringModelNeverPublishesCandidate(t *testing.T) {
	service, repo := evidenceFixture()
	repo.run = Run{}
	validator := &testEvidenceValidator{}
	service.Evidence = validator
	model := &evidenceRevokingModel{validator: validator}
	service.Model = model
	run, err := service.Start(context.Background(), Actor{TenantID: 1, UserID: "user-1"}, "project-1", "g2-card-during-model", evidenceStartRequest())
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Execute(context.Background(), run.ID)
	if err != nil || result.Status != StatusFailed || model.calls != 1 || repo.candidate.ID != "" {
		t.Fatalf("changed evidence published candidate: %+v %v calls=%d", result, err, model.calls)
	}
}
func TestGenerationObserverCannotStartScopedChapterOrExecuteQueuedWrite(t *testing.T) {
	service, repo := evidenceFixture()
	repo.run = Run{}
	scoped := &testScopedAuthorizer{denied: true}
	service.Authorizer = scoped
	service.Evidence = &testEvidenceValidator{}
	_, err := service.Start(context.Background(), Actor{TenantID: 1, UserID: "user-1"}, "project-1", "g2-observer-start", evidenceStartRequest())
	if !errors.Is(err, ErrForbidden) || scoped.chapter != "chapter-1" || repo.createRunCalls != 0 {
		t.Fatalf("observer read escalated: %v %+v", err, scoped)
	}
}
func TestGenerationRefsFailClosedWithoutEvidenceValidator(t *testing.T) {
	service, repo := evidenceFixture()
	repo.run = Run{}
	_, err := service.Start(context.Background(), Actor{TenantID: 1, UserID: "user-1"}, "project-1", "g2-missing-evidence-port", evidenceStartRequest())
	if !errors.Is(err, ErrSourceAccessDenied) || repo.createRunCalls != 0 {
		t.Fatalf("unverified refs queued: %v", err)
	}
}

func (r *testRepository) LoadGenerationInput(context.Context, Actor, string, string) (Input, error) {
	return r.input, nil
}

type testHumanAuthorizer struct{ testScopedAuthorizer }

func (*testHumanAuthorizer) AuthorizeHumanGeneration(ctx context.Context) error {
	return core.RequireHumanDecision(ctx)
}
func TestGenerationMachineInitiationAndReplayCannotUpgradeQueuedHumanIdentity(t *testing.T) {
	service, repo := evidenceFixture()
	repo.run = Run{}
	service.RequireHumanInitiation = true
	auth := &testHumanAuthorizer{}
	service.Authorizer = auth
	service.Evidence = &testEvidenceValidator{}
	machine := types.WithTenantAPIKeyScope(context.Background(), types.TenantAPIKeyScope{KeyID: 5, Capabilities: types.StringArray{"write"}})
	actor := Actor{TenantID: 1, UserID: "user-1"}
	_, err := service.Start(machine, actor, "project-1", "g2-machine-queue", evidenceStartRequest())
	if !errors.Is(err, core.ErrHumanDecisionRequired) || repo.createRunCalls != 0 {
		t.Fatalf("machine initiation accepted: %v", err)
	}
	human := types.WithCaller(context.Background(), types.Caller{TenantID: 1, UserID: "user-1", Role: types.TenantRoleContributor})
	run, err := service.Start(human, actor, "project-1", "g2-human-queue", evidenceStartRequest())
	if err != nil {
		t.Fatal(err)
	}
	if !repo.input.HumanInitiated || repo.input.Actor.Role != types.TenantRoleContributor || len(repo.input.Basis.EvidenceCardRefs) != 1 {
		t.Fatal("human authority/evidence snapshot missing")
	}
	_, err = service.Start(machine, actor, "project-1", "g2-human-queue", evidenceStartRequest())
	if !errors.Is(err, core.ErrHumanDecisionRequired) || repo.createRunCalls != 1 {
		t.Fatalf("machine replay upgraded human identity: %v", err)
	}
	// Legacy queue input without server-owned human admission must fail closed.
	repo.input.HumanInitiated = false
	model := &countingModel{}
	service.Model = model
	result, err := service.Execute(context.Background(), run.ID)
	if err != nil || result.Status != StatusFailed || model.calls != 0 {
		t.Fatalf("unknown legacy principal invoked model: %+v %v", result, err)
	}
}
func TestGenerationQueuedRoleRevocationAndPinnedReplayBasisFailBeforeModel(t *testing.T) {
	service, repo := evidenceFixture()
	repo.run = Run{}
	auth := &testHumanAuthorizer{}
	service.Authorizer = auth
	service.RequireHumanInitiation = true
	service.Evidence = &testEvidenceValidator{}
	actor := Actor{TenantID: 1, UserID: "user-1"}
	run, err := service.Start(context.Background(), actor, "project-1", "g2-pinned-replay", evidenceStartRequest())
	if err != nil {
		t.Fatal(err)
	}
	service.Currentness = testCurrentness{current: false}
	enqueuer := service.Enqueuer.(*testEnqueuer)
	before := enqueuer.calls
	_, err = service.Start(context.Background(), actor, "project-1", "g2-pinned-replay", evidenceStartRequest())
	if !errors.Is(err, ErrStaleInput) || enqueuer.calls != before {
		t.Fatalf("stale immutable replay queued: %v", err)
	}
	service.Currentness = testCurrentness{current: true}
	auth.denied = true
	model := &countingModel{}
	service.Model = model
	result, err := service.Execute(context.Background(), run.ID)
	if err != nil || result.Status != StatusFailed || model.calls != 0 {
		t.Fatalf("observer worker invoked model: %+v %v", result, err)
	}
}

type testEvidenceInputs struct{ InputResolver }

func (r testEvidenceInputs) ResolveGenerationInput(ctx context.Context, actor Actor, project string, request Request) (Input, error) {
	input, err := r.InputResolver.ResolveGenerationInput(ctx, actor, project, request)
	if err != nil {
		return input, err
	}
	input.EvidenceConstraints = nil
	for _, ref := range request.EvidenceCardRefs {
		input.EvidenceConstraints = append(input.EvidenceConstraints, EvidenceConstraint{CardRef: ref, SupportType: "qualifies", Scope: "仅合成样本", Limitations: "不证明因果", SourceIDs: []string{"source-1"}})
	}
	return input, nil
}
func evidenceFixture() (*Service, *testRepository) {
	service, repo := generationFixture()
	service.Inputs = testEvidenceInputs{InputResolver: service.Inputs}
	return service, repo
}
func (v *testEvidenceValidator) ValidateGenerationEvidenceConstraints(_ context.Context, _ Actor, _, _ string, refs []EvidenceCardRef, values []EvidenceConstraint) error {
	if v.denied || len(values) != len(refs) {
		return ErrSourceAccessDenied
	}
	for i, value := range values {
		if value.CardRef != refs[i] || value.SupportType != "qualifies" || value.Scope != "仅合成样本" || value.Limitations != "不证明因果" || len(value.SourceIDs) != 1 || value.SourceIDs[0] != "source-1" {
			return ErrSourceAccessDenied
		}
	}
	return nil
}
func TestGenerationConstraintSnapshotTamperStopsWorkerBeforeModel(t *testing.T) {
	service, repo := evidenceFixture()
	repo.run = Run{}
	service.Evidence = &testEvidenceValidator{}
	model := &countingModel{}
	service.Model = model
	run, err := service.Start(context.Background(), Actor{TenantID: 1, UserID: "user-1"}, "project-1", "g2-constraint-tamper", evidenceStartRequest())
	if err != nil {
		t.Fatal(err)
	}
	if len(repo.input.EvidenceConstraints) != 1 || repo.input.EvidenceConstraints[0].Limitations != "不证明因果" {
		t.Fatal("internal evidence metadata snapshot missing")
	}
	repo.input.EvidenceConstraints[0].Limitations = "可证明总体因果效果"
	result, err := service.Execute(context.Background(), run.ID)
	if err != nil || result.Status != StatusFailed || model.calls != 0 {
		t.Fatalf("changed evidence restriction called model: %+v %v", result, err)
	}
}
