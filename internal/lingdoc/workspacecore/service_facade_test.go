package workspacecore

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"slices"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestDefaultProjectAuthorizerEnforcesTenantRoleCeiling(t *testing.T) {
	ctx := context.Background()
	viewer := Actor{TenantID: 1, UserID: "viewer", Role: types.TenantRoleViewer}
	if _, _, _, err := NewService(fakeWorkspace()).CreateProject(ctx, viewer, "viewer-create", CreateProjectInput{Name: "拒绝", TemplateID: "template-demo"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("viewer create error = %v, want ErrNotFound", err)
	}
	contributor := Actor{TenantID: 1, UserID: "owner", Role: types.TenantRoleContributor}
	repository := fakeWorkspace()
	service := NewService(repository)
	if _, _, replayed, err := service.CreateProject(ctx, contributor, "role-create", CreateProjectInput{Name: "允许", TemplateID: "template-demo"}); err != nil || replayed {
		t.Fatalf("contributor create: replayed=%v err=%v", replayed, err)
	}
	if _, _, _, err := service.SaveSpec(ctx, Actor{TenantID: 1, UserID: "owner", Role: types.TenantRoleViewer}, "p", "viewer-write", SaveSpecInput{ExpectedSpecRevision: 1, Fields: map[string]string{"research_subject": "subject"}}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("viewer write error = %v, want ErrNotFound", err)
	}
}

func TestTenantRoleMatrixCapsProjectMemberPermissions(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name       string
		role       types.TenantRole
		wantManage bool
	}{
		{name: "viewer", role: types.TenantRoleViewer},
		{name: "contributor", role: types.TenantRoleContributor},
		{name: "admin", role: types.TenantRoleAdmin, wantManage: true},
		{name: "owner", role: types.TenantRoleOwner, wantManage: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := NewService(fakeWorkspace())
			actor := Actor{TenantID: 1, UserID: "owner", Role: tt.role}
			_, _, _, err := service.SaveMembers(ctx, actor, "p", "matrix-"+tt.name, SaveMembersInput{ExpectedProjectVersion: 1, CollaboratorUserIDs: []string{}})
			if tt.wantManage && err != nil {
				t.Fatalf("manage: %v", err)
			}
			if !tt.wantManage && !errors.Is(err, ErrNotFound) {
				t.Fatalf("manage error = %v, want ErrNotFound", err)
			}
		})
	}
}

func TestTenantRoleDowngradeDeniesIdempotentReplay(t *testing.T) {
	repository := fakeWorkspace()
	service := NewService(repository)
	ctx := context.Background()
	actor := Actor{TenantID: 1, UserID: "owner", Role: types.TenantRoleContributor}
	input := CreateProjectInput{Name: "重放", TemplateID: "template-demo"}
	if _, _, _, err := service.CreateProject(ctx, actor, "downgrade-key", input); err != nil {
		t.Fatalf("initial create: %v", err)
	}
	downgraded := actor
	downgraded.Role = types.TenantRoleViewer
	if _, _, _, err := service.CreateProject(ctx, downgraded, "downgrade-key", input); !errors.Is(err, ErrNotFound) {
		t.Fatalf("downgraded replay error = %v, want ErrNotFound", err)
	}
}

func TestProjectCapabilityAndChapterScopeAreIndependentFromTenantRole(t *testing.T) {
	repository := fakeWorkspace()
	repository.project.Members = append(repository.project.Members, Member{
		UserID: "author", Role: "collaborator", GovernanceRole: "member",
		FunctionRoles: []string{"author"}, FunctionScopes: map[string][]string{"author": {"c"}},
	})
	service := NewService(repository)
	ctx := context.Background()
	author := Actor{TenantID: 1, UserID: "author", Role: types.TenantRoleContributor}
	input := SaveChapterInput{ExpectedSpecRevision: 1, BodyMarkdown: "scoped", SourceIDs: []string{}}
	if _, _, _, err := service.SaveChapter(ctx, author, "p", "c", "scoped-write", input); err != nil {
		t.Fatalf("scoped author write: %v", err)
	}
	if _, _, _, err := service.SaveChapter(ctx, author, "p", "other", "out-of-scope", input); !errors.Is(err, ErrNotFound) {
		t.Fatalf("out-of-scope write = %v, want ErrNotFound", err)
	}
	admin := Actor{TenantID: 1, UserID: "admin", Role: types.TenantRoleAdmin}
	repository.project.Members = append(repository.project.Members, Member{UserID: "admin", Role: "collaborator", GovernanceRole: "admin"})
	if err := service.Authorize(ctx, admin, "p", "manage"); err != nil {
		t.Fatalf("admin member management: %v", err)
	}
	for i := range repository.project.Members {
		if repository.project.Members[i].UserID == "admin" {
			repository.project.Members[i].Status = "suspended"
		}
	}
	if err := service.Authorize(ctx, admin, "p", "manage"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("suspended admin authorization = %v, want ErrNotFound", err)
	}
}

func TestMemberAssignmentsPersistCapabilityAndScopeInTransaction(t *testing.T) {
	repository := fakeWorkspace()
	audit := &auditSinkStub{}
	service := NewServiceWithAudit(repository, ContractDemoTemplate{}, nil, nil, audit)
	input := SaveMembersInput{ExpectedProjectVersion: 1, Members: []Member{{
		UserID: "author", GovernanceRole: "member", FunctionRoles: []string{"author"},
		FunctionScopes: map[string][]string{"author": {"c"}},
	}}}
	actor := Actor{TenantID: 1, UserID: "owner", Role: types.TenantRoleAdmin}
	if _, _, _, err := service.SaveMembers(context.Background(), actor, "p", "assignments-1", input); err != nil {
		t.Fatalf("save assignments: %v", err)
	}
	var found bool
	for _, got := range repository.project.Members {
		if got.UserID == "author" {
			found = true
			if len(got.FunctionRoles) != 1 || got.FunctionRoles[0] != "author" || got.FunctionScopes["author"][0] != "c" {
				t.Fatalf("assignment not persisted: %+v", got)
			}
		}
	}
	if !found {
		t.Fatal("assignment member missing")
	}
	if len(audit.events) < 2 || audit.events[len(audit.events)-1].Capability != "manage:members" || audit.events[len(audit.events)-1].Details["change"] != "member_permissions_replaced" {
		t.Fatalf("member change audit = %+v", audit.events)
	}
}

func TestTransferOwnerIsAtomicAndClearsFunctionAxis(t *testing.T) {
	repository := fakeWorkspace()
	repository.project.Members = append(repository.project.Members, Member{UserID: "recipient", Role: "collaborator", GovernanceRole: "member", FunctionRoles: []string{"author"}, FunctionScopes: map[string][]string{"author": {"c"}}})
	service := NewService(repository)
	actor := Actor{TenantID: 1, UserID: "owner", Role: types.TenantRoleAdmin}
	if _, _, _, err := service.TransferOwner(context.Background(), actor, "p", "transfer-1", TransferOwnerInput{ExpectedProjectVersion: 1, NewOwnerUserID: "recipient"}); err != nil {
		t.Fatalf("transfer: %v", err)
	}
	for _, member := range repository.project.Members {
		if member.UserID == "recipient" && (member.Role != "owner" || member.GovernanceRole != "owner" || len(member.FunctionRoles) != 0) {
			t.Fatalf("recipient not promoted: %+v", member)
		}
		if member.UserID == "owner" && member.Role != "collaborator" {
			t.Fatalf("old owner not demoted: %+v", member)
		}
	}
}

type sourcePolicyStub struct {
	err   error
	calls int
}

type auditSinkStub struct{ events []AuditEvent }

func (s *auditSinkStub) Record(_ context.Context, event AuditEvent) error {
	s.events = append(s.events, event)
	return nil
}

type failingAuditSink struct{}

func (failingAuditSink) Record(context.Context, AuditEvent) error {
	return errors.New("audit unavailable")
}

func TestAuthorizationAuditRecordsAllowAndDenyDecisions(t *testing.T) {
	audit := &auditSinkStub{}
	service := NewServiceWithAudit(fakeWorkspace(), ContractDemoTemplate{}, nil, nil, audit)
	actor := Actor{TenantID: 1, UserID: "owner", Role: types.TenantRoleAdmin}
	if err := service.Authorize(context.Background(), actor, "p", "read"); err != nil {
		t.Fatal(err)
	}
	if err := service.Authorize(context.Background(), Actor{TenantID: 1, UserID: "missing", Role: types.TenantRoleViewer}, "p", "read"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deny = %v", err)
	}
	if len(audit.events) != 2 || audit.events[0].Decision != "allow" || audit.events[1].Decision != "deny" {
		t.Fatalf("audit events = %+v", audit.events)
	}
	if audit.events[0].Role != types.TenantRoleAdmin || audit.events[1].Role != types.TenantRoleViewer {
		t.Fatalf("audit roles = %q/%q", audit.events[0].Role, audit.events[1].Role)
	}
}

func TestAuthorizationAuditFailureDoesNotBlockDecision(t *testing.T) {
	service := NewServiceWithAudit(fakeWorkspace(), ContractDemoTemplate{}, nil, nil, failingAuditSink{})
	if err := service.Authorize(context.Background(), Actor{TenantID: 1, UserID: "owner"}, "p", "read"); err != nil {
		t.Fatalf("audit failure changed authorization result: %v", err)
	}
}

func TestAuthorizationLogModeFallsBackToLegacyDecision(t *testing.T) {
	audit := &auditSinkStub{}
	service := NewServiceWithAuditMode(fakeWorkspace(), ContractDemoTemplate{}, nil, nil, audit, AuthorizationModeLog)
	actor := Actor{TenantID: 1, UserID: "owner", Role: types.TenantRoleViewer}
	if _, _, _, err := service.CreateProject(context.Background(), actor, "legacy-log", CreateProjectInput{Name: "兼容", TemplateID: "template-demo"}); err != nil {
		t.Fatalf("log mode should preserve the legacy decision while observing the new policy: %v", err)
	}
	if len(audit.events) < 2 || audit.events[0].Capability != "shadow:create" || audit.events[0].Decision != "deny" {
		t.Fatalf("shadow audit = %+v", audit.events)
	}
}

func TestAuthorizationLogModeDoesNotAdoptNewAllow(t *testing.T) {
	repository := fakeWorkspace()
	repository.project.Members = append(repository.project.Members, Member{UserID: "admin", Role: "collaborator", GovernanceRole: "admin", Status: "active"})
	audit := &auditSinkStub{}
	service := NewServiceWithAuditMode(repository, ContractDemoTemplate{}, nil, nil, audit, AuthorizationModeLog)
	actor := Actor{TenantID: 1, UserID: "admin", Role: types.TenantRoleAdmin}
	if err := service.Authorize(context.Background(), actor, "p", "manage"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("log mode adopted new allow instead of legacy deny: %v", err)
	}
	if len(audit.events) == 0 || audit.events[0].Capability != "shadow:manage" || audit.events[0].Decision != "allow" {
		t.Fatalf("shadow allow audit = %+v", audit.events)
	}
}

func TestAuthorizationRollbackModeStopsNewMemberAssignments(t *testing.T) {
	service := NewServiceWithAuditMode(fakeWorkspace(), ContractDemoTemplate{}, nil, nil, nil, AuthorizationModeRollback)
	actor := Actor{TenantID: 1, UserID: "owner", Role: types.TenantRoleAdmin}
	if _, _, _, err := service.SaveMembers(context.Background(), actor, "p", "rollback-members", SaveMembersInput{ExpectedProjectVersion: 1, Members: []Member{{UserID: "author", GovernanceRole: "member", FunctionRoles: []string{"author"}}}}); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("rollback mode assignment error = %v, want ErrInvalidState", err)
	}
}

type recordingProjectAuthorizer struct {
	tenantErr    error
	projectErr   error
	tenantCalls  int
	projectCalls []string
}

func (a *recordingProjectAuthorizer) AuthorizeTenant(_ Transaction, _ Actor, _ string) error {
	a.tenantCalls++
	return a.tenantErr
}

func (a *recordingProjectAuthorizer) AuthorizeProject(tx Transaction, actor Actor, projectID, capability string) (Project, error) {
	a.projectCalls = append(a.projectCalls, projectID+":"+capability)
	if a.projectErr != nil {
		return Project{}, a.projectErr
	}
	return tx.Project(actor.TenantID, projectID)
}

func TestProjectAuthorizerSeamCoversCreateListAndRead(t *testing.T) {
	repository := fakeWorkspace()
	authorizer := &recordingProjectAuthorizer{}
	service := NewServiceWithAuthorizer(repository, ContractDemoTemplate{}, nil, authorizer)
	actor := Actor{TenantID: 1, UserID: "owner"}
	ctx := context.Background()
	raw, _, _, err := service.CreateProject(ctx, actor, "create-auth", CreateProjectInput{Name: "授权", TemplateID: "template-demo"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	created := asProject(t, raw)
	if authorizer.tenantCalls != 1 {
		t.Fatalf("create tenant calls = %d, want 1", authorizer.tenantCalls)
	}
	if _, _, err := service.ListProjects(ctx, actor); err != nil {
		t.Fatalf("list: %v", err)
	}
	if authorizer.tenantCalls != 2 {
		t.Fatalf("list tenant calls = %d, want 2", authorizer.tenantCalls)
	}
	if _, err := service.GetProject(ctx, actor, created.ID); err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(authorizer.projectCalls) != 1 || authorizer.projectCalls[0] != created.ID+":read" {
		t.Fatalf("project calls = %v, want [%s:read]", authorizer.projectCalls, created.ID)
	}
}

func TestProjectAuthorizerDeniesBeforeReplay(t *testing.T) {
	repository := fakeWorkspace()
	repository.project.Status = "draft"
	authorizer := &recordingProjectAuthorizer{}
	service := NewServiceWithAuthorizer(repository, ContractDemoTemplate{}, nil, authorizer)
	actor := Actor{TenantID: 1, UserID: "owner"}
	ctx := context.Background()
	input := SaveSpecInput{ExpectedSpecRevision: 1, Fields: map[string]string{"research_subject": "subject"}}
	if _, _, _, err := service.SaveSpec(ctx, actor, "p", "save-auth", input); err != nil {
		t.Fatalf("initial save: %v", err)
	}
	authorizer.projectErr = ErrNotFound
	if _, _, _, err := service.SaveSpec(ctx, actor, "p", "save-auth", input); !errors.Is(err, ErrNotFound) {
		t.Fatalf("revoked replay error = %v, want ErrNotFound", err)
	}
}

func (s *sourcePolicyStub) Validate(context.Context, string, string, []string) error {
	s.calls++
	return s.err
}
func TestServiceSourceRecheckBeforeReplayWithNonSQLStorage(t *testing.T) {
	r := fakeWorkspace()
	policy := &sourcePolicyStub{}
	s := NewServiceWithSources(r, ContractDemoTemplate{}, policy)
	actor := Actor{TenantID: 1, UserID: "owner"}
	in := SaveChapterInput{ExpectedSpecRevision: 1, BodyMarkdown: "draft [[source:s1]]", SourceIDs: []string{"s1"}}
	body, _, replayed, err := s.SaveChapter(context.Background(), actor, "p", "c", "source-save", in)
	if err != nil || replayed || r.writes != 1 || policy.calls != 1 {
		t.Fatalf("initial save: %v %v", replayed, err)
	}
	policy.err = errors.New("source changed or revoked")
	if _, _, _, err := s.SaveChapter(context.Background(), actor, "p", "c", "source-save", in); !errors.Is(err, policy.err) {
		t.Fatalf("source-invalid replay returned saved content: %v", err)
	}
	if r.writes != 1 || policy.calls != 2 {
		t.Fatal("failed recheck mutated state or skipped policy")
	}
	policy.err = nil
	again, _, replayed, err := s.SaveChapter(context.Background(), actor, "p", "c", "source-save", in)
	if err != nil || !replayed || string(again) != string(body) || r.writes != 1 {
		t.Fatalf("restored replay: %v %v", replayed, err)
	}
	r.active = false
	previousCalls := policy.calls
	if _, _, _, err := s.SaveChapter(context.Background(), actor, "p", "c", "source-save", in); !errors.Is(err, ErrNotFound) || policy.calls != previousCalls {
		t.Fatalf("project authorization did not precede source access: %v", err)
	}
}

// A stateful non-SQL adapter. The SAME Service owns every rule, and rollback
// discards both domain writes and replay records on any callback error.
type stateRepository struct {
	project    Project
	chapter    Chapter
	active     bool
	operations map[OperationIdentity]OperationResult
	writes     int
	failCommit bool
}

func (r *stateRepository) Transaction(ctx context.Context, _ TransactionOptions, fn func(Transaction) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	tx := *r
	tx.project.Spec = maps.Clone(r.project.Spec)
	tx.operations = maps.Clone(r.operations)
	if err := fn(&stateTransaction{state: &tx}); err != nil {
		return err
	}
	if r.failCommit {
		return ErrRequestInProgress
	}
	*r = tx
	return nil
}

type stateTransaction struct {
	Transaction
	state *stateRepository
}

func (t *stateTransaction) ActiveMember(Actor) (bool, error) { return t.state.active, nil }
func (t *stateTransaction) Chapters(string) ([]Chapter, error) {
	return []Chapter{t.state.chapter}, nil
}
func (t *stateTransaction) ReplaceMembers(_ string, members []Member) error {
	t.state.project.Members = slices.Clone(members)
	return nil
}
func (t *stateTransaction) Projects(actor Actor, _ int) ([]Project, error) {
	if actor.TenantID != 1 || !t.state.active {
		return nil, ErrNotFound
	}
	return []Project{t.state.project}, nil
}
func (t *stateTransaction) Project(tenant uint64, id string) (Project, error) {
	if tenant != 1 || t.state.project.ID != id {
		return Project{}, ErrNotFound
	}
	return t.state.project, nil
}
func (t *stateTransaction) InsertProject(_ uint64, project Project) error {
	t.state.project = project
	return nil
}
func (t *stateTransaction) Chapter(project, id string) (Chapter, error) {
	if project != t.state.chapter.ProjectID || id != t.state.chapter.ID {
		return Chapter{}, ErrNotFound
	}
	return t.state.chapter, nil
}
func (t *stateTransaction) UpdateProject(_ uint64, previous, next Project) error {
	if t.state.project.ProjectVersion != previous.ProjectVersion {
		return ErrVersionConflict
	}
	t.state.project = next
	return nil
}
func (t *stateTransaction) AppendChapter(previous, next Chapter, _ int64) error {
	if !sameVersion(previous.CurrentVersionID, t.state.chapter.CurrentVersionID) {
		return ErrVersionConflict
	}
	t.state.chapter = next
	t.state.writes++
	return nil
}
func (t *stateTransaction) Operation(id OperationIdentity) (OperationResult, bool, error) {
	result, found := t.state.operations[id]
	return result, found, nil
}
func (t *stateTransaction) SaveOperation(id OperationIdentity, result OperationResult) error {
	t.state.operations[id] = result
	return nil
}
func fakeWorkspace() *stateRepository {
	return &stateRepository{active: true, operations: map[OperationIdentity]OperationResult{}, project: Project{ID: "p", Status: "active", ProjectVersion: 1, SpecRevision: 1, Spec: map[string]string{}, Members: []Member{{UserID: "owner", Role: "owner"}}}, chapter: Chapter{ID: "c", ProjectID: "p", SourceIDs: []string{}, ReviewItems: []ReviewItem{{ID: "review", Statement: "check me", OriginCandidateID: "candidate"}}}}
}
func TestServiceRulesWithNonSQLStorage(t *testing.T) {
	r := fakeWorkspace()
	s := NewService(r)
	actor := Actor{TenantID: 1, UserID: "owner"}
	ctx := context.Background()
	input := SaveChapterInput{ExpectedSpecRevision: 1, BodyMarkdown: "first draft", SourceIDs: []string{}}
	raw, code, replay, err := s.SaveChapter(ctx, actor, "p", "c", "chapter-save", input)
	if err != nil || code != 201 || replay || r.writes != 1 {
		t.Fatalf("save: %s %d %v %v", raw, code, replay, err)
	}
	var chapter Chapter
	if err := json.Unmarshal(raw, &chapter); err != nil {
		t.Fatal(err)
	}
	if chapter.CurrentVersionID == nil || len(chapter.ReviewItems) != 1 || chapter.ConfirmationValid {
		t.Fatalf("immutable version/review lost: %+v", chapter)
	}
	second, _, replay, err := s.SaveChapter(ctx, actor, "p", "c", "chapter-save", input)
	if err != nil || !replay || string(raw) != string(second) || r.writes != 1 {
		t.Fatalf("lost-response retry duplicated: %v %v", replay, err)
	}
	if _, _, _, err := s.SaveChapter(ctx, actor, "p", "c", "different-key", input); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("stale version: %v", err)
	}
	changed := input
	changed.BodyMarkdown = "different body"
	if _, _, _, err := s.SaveChapter(ctx, actor, "p", "c", "chapter-save", changed); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("key conflict: %v", err)
	}
	r.active = false
	if _, _, _, err := s.SaveChapter(ctx, actor, "p", "c", "chapter-save", input); !errors.Is(err, ErrNotFound) {
		t.Fatalf("revoked replay leaked: %v", err)
	}
	r.active = true
	r.project.Members = nil
	if _, err := s.GetProject(ctx, actor, "p"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("nonmember read: %v", err)
	}
}
func TestServiceRollbackAndSourceBoundaryWithNonSQLStorage(t *testing.T) {
	r := fakeWorkspace()
	s := NewService(r)
	actor := Actor{TenantID: 1, UserID: "owner"}
	ctx := context.Background()
	input := SaveChapterInput{ExpectedSpecRevision: 1, BodyMarkdown: "draft", SourceIDs: []string{}}
	r.failCommit = true
	if _, _, _, err := s.SaveChapter(ctx, actor, "p", "c", "chapter-save", input); !errors.Is(err, ErrRequestInProgress) {
		t.Fatalf("commit failure: %v", err)
	}
	if r.writes != 0 || r.project.ProjectVersion != 1 || len(r.operations) != 0 {
		t.Fatal("failed transaction leaked mutations")
	}
	r.failCommit = false
	if _, _, replay, err := s.SaveChapter(ctx, actor, "p", "c", "chapter-save", input); err != nil || replay {
		t.Fatalf("retry: %v %v", replay, err)
	}
	r.chapter.SourceIDs = []string{"changed-source"}
	input.ExpectedChapterVersionID = r.chapter.CurrentVersionID
	input.SourceIDs = []string{"changed-source"}
	input.BodyMarkdown = "[[source:changed-source]]"
	if _, _, _, err := s.SaveChapter(ctx, actor, "p", "c", "source-check", input); !errors.Is(err, ErrSourceUnavailable) {
		t.Fatalf("changed existing source escaped policy: %v", err)
	}
	input.BodyMarkdown = "[[source:malformed/id]]"
	if _, _, _, err := s.SaveChapter(ctx, actor, "p", "c", "source-check", input); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("malformed marker: %v", err)
	}
}
