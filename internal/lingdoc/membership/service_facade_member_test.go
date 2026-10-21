package membership

import (
	"context"
	"errors"
	"maps"
	"slices"
	"testing"
	"time"

	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
	"github.com/Tencent/WeKnora/internal/types"
)

func TestTenantRoleMatrixCapsProjectMemberPermissions(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name       string
		role       types.TenantRole
		wantManage bool
	}{
		{name: "viewer", role: types.TenantRoleViewer},
		{name: "contributor", role: types.TenantRoleContributor, wantManage: true},
		{name: "admin", role: types.TenantRoleAdmin, wantManage: true},
		{name: "owner", role: types.TenantRoleOwner, wantManage: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repository := fakeWorkspace()
			service := core.NewService(repository)
			member := newServiceForTest(t, service, repository)
			actor := core.Actor{TenantID: 1, UserID: "owner", Role: tt.role}
			_, _, _, err := member.SaveMembers(
				ctx, actor, "p", "matrix-"+tt.name,
				core.SaveMembersInput{ExpectedProjectVersion: 1, CollaboratorUserIDs: []string{}},
			)
			if tt.wantManage && err != nil {
				t.Fatalf("manage: %v", err)
			}
			if !tt.wantManage && !errors.Is(err, core.ErrNotFound) {
				t.Fatalf("manage error = %v, want ErrNotFound", err)
			}
		})
	}
}

func TestMemberAssignmentsPersistCapabilityAndScopeInTransaction(t *testing.T) {
	repository := fakeWorkspace()
	audit := &auditSinkStub{}
	service := core.NewServiceWithAudit(repository, core.ContractDemoTemplate{}, nil, nil, audit)
	member := newServiceForTest(t, service, repository)
	input := core.SaveMembersInput{ExpectedProjectVersion: 1, Members: []core.Member{{
		UserID: "author", GovernanceRole: "member", FunctionRoles: []string{"author"},
		FunctionScopes: map[string][]string{"author": {"c"}},
	}}}
	actor := core.Actor{TenantID: 1, UserID: "owner", Role: types.TenantRoleAdmin}
	if _, _, _, err := member.SaveMembers(context.Background(), actor, "p", "assignments-1", input); err != nil {
		t.Fatalf("save assignments: %v", err)
	}
	var found bool
	for _, got := range repository.project.Members {
		if got.UserID == "author" {
			found = true
			if len(got.FunctionRoles) != 1 || got.FunctionRoles[0] != "author" ||
				got.FunctionScopes["author"][0] != "c" {
				t.Fatalf("assignment not persisted: %+v", got)
			}
		}
	}
	if !found {
		t.Fatal("assignment member missing")
	}
	if len(audit.events) < 2 || audit.events[len(audit.events)-1].Capability != "manage:members" ||
		audit.events[len(audit.events)-1].Details["change"] != "member_permissions_replaced" {
		t.Fatalf("member change audit = %+v", audit.events)
	}
}

func TestTransferOwnerIsAtomicAndClearsFunctionAxis(t *testing.T) {
	repository := fakeWorkspace()
	repository.project.Members = append(repository.project.Members, core.Member{
		UserID:         "recipient",
		Role:           "collaborator",
		GovernanceRole: "member",
		FunctionRoles:  []string{"author"},
		FunctionScopes: map[string][]string{"author": {"c"}},
	})
	service := core.NewService(repository)
	member := newServiceForTest(t, service, repository)
	actor := core.Actor{TenantID: 1, UserID: "owner", Role: types.TenantRoleAdmin}
	if _, _, _, err := member.TransferOwner(
		context.Background(), actor, "p", "transfer-1",
		core.TransferOwnerInput{ExpectedProjectVersion: 1, NewOwnerUserID: "recipient"},
	); err != nil {
		t.Fatalf("transfer: %v", err)
	}
	for _, memberRow := range repository.project.Members {
		if memberRow.UserID == "recipient" && (memberRow.Role != "owner" || memberRow.GovernanceRole != "owner" ||
			len(memberRow.FunctionRoles) != 0) {
			t.Fatalf("recipient not promoted: %+v", memberRow)
		}
		if memberRow.UserID == "owner" && memberRow.Role != "collaborator" {
			t.Fatalf("old owner not demoted: %+v", memberRow)
		}
	}
}

func TestAuthorizationRollbackModeStopsNewMemberAssignments(t *testing.T) {
	repository := fakeWorkspace()
	service := core.NewServiceWithAuditMode(
		repository, core.ContractDemoTemplate{}, nil, nil, nil, core.AuthorizationModeRollback,
	)
	member := newServiceForTest(t, service, repository)
	actor := core.Actor{TenantID: 1, UserID: "owner", Role: types.TenantRoleAdmin}
	if _, _, _, err := member.SaveMembers(
		context.Background(), actor, "p", "rollback-members",
		core.SaveMembersInput{
			ExpectedProjectVersion: 1,
			Members: []core.Member{{
				UserID:         "author",
				GovernanceRole: "member",
				FunctionRoles:  []string{"author"},
			}},
		},
	); !errors.Is(err, core.ErrInvalidState) {
		t.Fatalf("rollback mode assignment error = %v, want ErrInvalidState", err)
	}
}

func TestProjectMutationResultsPreserveMemberAndOwnerSemantics(t *testing.T) {
	repository := fakeWorkspace()
	repository.project.Members = []core.Member{
		{UserID: "owner", Role: "owner", GovernanceRole: "owner", Status: "active"},
		{UserID: "member", Role: "collaborator", GovernanceRole: "member", Status: "active"},
	}
	service := core.NewService(repository)
	member := newServiceForTest(t, service, repository)
	actor := core.Actor{TenantID: 1, UserID: "owner"}

	members, err := member.SaveMembersResult(context.Background(), actor, "p", "typed-members", core.SaveMembersInput{
		ExpectedProjectVersion: 1,
		Members:                []core.Member{{UserID: "member", GovernanceRole: "member", Status: "active"}},
	})
	if err != nil {
		t.Fatalf("typed members: %v", err)
	}
	if members.Replayed || members.Project.ProjectVersion != 2 || len(members.Project.Members) != 2 {
		t.Fatalf("typed members result = %+v, want updated project", members)
	}

	transfer, err := member.TransferOwnerResult(
		context.Background(), actor, "p", "typed-owner-transfer",
		core.TransferOwnerInput{
			ExpectedProjectVersion: 2,
			NewOwnerUserID:         "member",
		},
	)
	if err != nil {
		t.Fatalf("typed owner transfer: %v", err)
	}
	hasNewOwner := false
	for _, memberRow := range transfer.Project.Members {
		if memberRow.UserID == "member" && memberRow.Role == "owner" {
			hasNewOwner = true
		}
	}
	if transfer.Replayed || transfer.Project.ProjectVersion != 3 || !hasNewOwner {
		t.Fatalf("typed owner transfer result = %+v, want owner change", transfer)
	}

	if _, err := member.TransferOwnerResult(
		context.Background(), actor, "p", "typed-owner-transfer",
		core.TransferOwnerInput{
			ExpectedProjectVersion: 2,
			NewOwnerUserID:         "member",
		},
	); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("owner transfer replay after ownership change = %v, want authorization denial", err)
	}
}

// A stateful non-SQL adapter. The SAME Service owns every rule, and rollback
// discards both domain writes and replay records on any callback error.
//
// It is a copy, not a move: the aggregate's own facade tests still use theirs.
type stateRepository struct {
	project     core.Project
	chapter     core.Chapter
	workingCopy core.WorkingCopy
	versions    []core.ChapterVersion
	audits      []core.AuditEvent
	active      bool
	operations  map[core.OperationIdentity]core.OperationResult
	writes      int
	failCommit  bool
}

func (r *stateRepository) Transaction(
	ctx context.Context,
	_ core.TransactionOptions,
	fn func(core.Transaction) error,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	tx := *r
	tx.project.Spec = maps.Clone(r.project.Spec)
	tx.operations = maps.Clone(r.operations)
	tx.versions = slices.Clone(r.versions)
	tx.audits = slices.Clone(r.audits)
	if err := fn(&stateTransaction{state: &tx}); err != nil {
		return err
	}
	if r.failCommit {
		return core.ErrRequestInProgress
	}
	*r = tx
	return nil
}

type stateTransaction struct {
	core.Transaction
	state *stateRepository
}

func (t *stateTransaction) ActiveMember(core.Actor) (bool, error) { return t.state.active, nil }
func (t *stateTransaction) Chapters(string) ([]core.Chapter, error) {
	return []core.Chapter{t.state.chapter}, nil
}

func (t *stateTransaction) ReplaceMembers(_ string, members []core.Member) error {
	t.state.project.Members = slices.Clone(members)
	return nil
}

func (t *stateTransaction) Projects(actor core.Actor, _ int) ([]core.Project, error) {
	if actor.TenantID != 1 || !t.state.active {
		return nil, core.ErrNotFound
	}
	return []core.Project{t.state.project}, nil
}

func (t *stateTransaction) Project(tenant uint64, id string) (core.Project, error) {
	if tenant != 1 || t.state.project.ID != id {
		return core.Project{}, core.ErrNotFound
	}
	return t.state.project, nil
}

func (t *stateTransaction) InsertProject(_ uint64, project core.Project) error {
	t.state.project = project
	return nil
}

func (t *stateTransaction) Chapter(project, id string) (core.Chapter, error) {
	if project != t.state.chapter.ProjectID || id != t.state.chapter.ID {
		return core.Chapter{}, core.ErrNotFound
	}
	return t.state.chapter, nil
}

func (t *stateTransaction) WorkingCopy(project, id string) (core.WorkingCopy, error) {
	if project != t.state.workingCopy.ProjectID || id != t.state.workingCopy.ChapterID {
		return core.WorkingCopy{}, core.ErrNotFound
	}
	// The local is not named "copy": that name shadows Go's built-in.
	snapshot := t.state.workingCopy
	snapshot.SourceIDs = slices.Clone(snapshot.SourceIDs)
	snapshot.ReviewItems = slices.Clone(snapshot.ReviewItems)
	return snapshot, nil
}

func (t *stateTransaction) SaveWorkingCopy(previous, next core.WorkingCopy) error {
	if previous.WorkingCopyRevision != t.state.workingCopy.WorkingCopyRevision ||
		!sameVersion(previous.BaseChapterVersionID, t.state.workingCopy.BaseChapterVersionID) {
		return core.ErrVersionConflict
	}
	next.SourceIDs = slices.Clone(next.SourceIDs)
	next.ReviewItems = slices.Clone(next.ReviewItems)
	t.state.workingCopy = next
	return nil
}

func (t *stateTransaction) ChapterVersion(project, chapter, id string) (core.ChapterVersion, error) {
	for _, version := range t.state.versions {
		if version.ProjectID == project && version.ChapterID == chapter && version.ID == id {
			return version, nil
		}
	}
	return core.ChapterVersion{}, core.ErrNotFound
}

func (t *stateTransaction) ChapterVersions(project, chapter string) ([]core.ChapterVersion, error) {
	versions := make([]core.ChapterVersion, 0, len(t.state.versions))
	for _, version := range t.state.versions {
		if version.ProjectID == project && version.ChapterID == chapter {
			versions = append(versions, version)
		}
	}
	return versions, nil
}

func (t *stateTransaction) UpdateProject(_ uint64, previous, next core.Project) error {
	if t.state.project.ProjectVersion != previous.ProjectVersion {
		return core.ErrVersionConflict
	}
	t.state.project = next
	return nil
}

func (t *stateTransaction) AppendChapter(previous, next core.Chapter, _ int64) error {
	if !sameVersion(previous.CurrentVersionID, t.state.chapter.CurrentVersionID) {
		return core.ErrVersionConflict
	}
	t.state.chapter = next
	t.state.versions = append(t.state.versions, core.ChapterVersion{
		ID: *next.CurrentVersionID, ProjectID: next.ProjectID,
		ChapterID: next.ID, ParentVersionID: previous.CurrentVersionID, BodyMarkdown: next.BodyMarkdown,
		SourceIDs:   slices.Clone(next.SourceIDs),
		ReviewItems: slices.Clone(next.ReviewItems),
		CreatedAt:   time.Now().UTC(),
	})
	t.state.writes++
	return nil
}

func (t *stateTransaction) Operation(id core.OperationIdentity) (core.OperationResult, bool, error) {
	result, found := t.state.operations[id]
	return result, found, nil
}

func (t *stateTransaction) SaveOperation(id core.OperationIdentity, result core.OperationResult) error {
	t.state.operations[id] = result
	return nil
}

func (t *stateTransaction) RecordAudit(event core.AuditEvent) error {
	t.state.audits = append(t.state.audits, event)
	return nil
}

func (t *stateTransaction) AuditEvents(projectID string) ([]core.AuditEvent, error) {
	events := make([]core.AuditEvent, 0, len(t.state.audits))
	for _, event := range t.state.audits {
		if event.ProjectID == projectID {
			events = append(events, event)
		}
	}
	return events, nil
}

// sameVersion compares two optional version identifiers.
func sameVersion(a, b *string) bool {
	return (a == nil && b == nil) || (a != nil && b != nil && *a == *b)
}

func fakeWorkspace() *stateRepository {
	chapter := core.Chapter{
		ID:        "c",
		ProjectID: "p",
		SourceIDs: []string{},
		ReviewItems: []core.ReviewItem{{
			ID:                "review",
			Statement:         "check me",
			OriginCandidateID: "candidate",
		}},
	}
	now := time.Now().UTC()
	return &stateRepository{
		active: true, operations: map[core.OperationIdentity]core.OperationResult{},
		project: core.Project{
			ID:             "p",
			Status:         "active",
			ProjectVersion: 1,
			SpecRevision:   1,
			Spec:           map[string]string{},
			Members:        []core.Member{{UserID: "owner", Role: "owner"}},
		},
		chapter: chapter, workingCopy: core.WorkingCopy{
			ProjectID: "p", ChapterID: "c", SpecRevision: 1,
			WorkingCopyRevision: 1, BodyMarkdown: chapter.BodyMarkdown, SourceIDs: []string{},
			ReviewItems: slices.Clone(chapter.ReviewItems), UpdatedAt: now,
		},
	}
}
