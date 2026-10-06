package workspacecore

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

func issueBinding(projectVersion int64, severity string) ValidationIssueBinding {
	version := "chapter-v4"
	return ValidationIssueBinding{IssueID: "issue-1", RuleID: "required_fields", RulesetHash: "ruleset-7",
		Severity: severity, TargetID: "chapter-1", TargetVersion: &version, ProjectVersion: projectVersion}
}

func TestValidationIssueDispositionPersistsBindingAndRejectsStaleOrDuplicateActions(t *testing.T) {
	service := testStore(t, filepath.Join(t.TempDir(), "issue-disposition.db"))
	owner := Actor{TenantID: 401, UserID: "owner"}
	project := seedActiveG3Project(t, service, owner)
	binding := issueBinding(project.ProjectVersion, "warning")
	input := ValidationIssueDispositionInput{ExpectedProjectVersion: project.ProjectVersion,
		ValidationIssueBinding: binding, Action: "dismiss", Reason: "该章节不适用于本项目"}

	raw, status, replayed, err := service.RecordValidationIssueDisposition(context.Background(), owner, project.ID, "issue-dismiss-001", input)
	if err != nil || status != 201 || replayed {
		t.Fatalf("record disposition: status=%d replayed=%v err=%v", status, replayed, err)
	}
	var got ValidationIssueDisposition
	if err := json.Unmarshal(raw, &got); err != nil || got.Action != "dismiss" || got.ActorID != owner.UserID || got.Reason != input.Reason || got.ProjectVersion != project.ProjectVersion || got.CreatedAt.IsZero() {
		t.Fatalf("persisted disposition = %+v, unmarshal error=%v", got, err)
	}

	current, err := service.ValidationIssueDispositions(context.Background(), owner, project.ID, []ValidationIssueBinding{binding})
	if err != nil || current[binding.IssueID].Action != "dismiss" {
		t.Fatalf("read current disposition = %+v err=%v", current, err)
	}
	stale := binding
	stale.ProjectVersion++
	if current, err := service.ValidationIssueDispositions(context.Background(), owner, project.ID, []ValidationIssueBinding{stale}); err != nil || len(current) != 0 {
		t.Fatalf("stale disposition leaked to new project version: %+v err=%v", current, err)
	}

	duplicate := input
	duplicate.Action = "resolve"
	if _, _, _, err := service.RecordValidationIssueDisposition(context.Background(), owner, project.ID, "issue-dismiss-002", duplicate); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("duplicate disposition error = %v, want ErrInvalidState", err)
	}
	if _, _, _, err := service.RecordValidationIssueDisposition(context.Background(), owner, project.ID, "issue-dismiss-003", ValidationIssueDispositionInput{
		ExpectedProjectVersion: project.ProjectVersion, ValidationIssueBinding: issueBinding(project.ProjectVersion, "blocking"), Action: "waive", Reason: "接受风险",
	}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("blocking waive error = %v, want ErrInvalidRequest", err)
	}
	emptyReason := input
	emptyReason.Reason = "  "
	if _, _, _, err := service.RecordValidationIssueDisposition(context.Background(), owner, project.ID, "issue-dismiss-004", emptyReason); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("empty reason error = %v, want ErrInvalidRequest", err)
	}
	staleInput := input
	staleInput.ExpectedProjectVersion++
	if _, _, _, err := service.RecordValidationIssueDisposition(context.Background(), owner, project.ID, "issue-dismiss-005", staleInput); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("stale project error = %v, want ErrVersionConflict", err)
	}

	events, err := service.ListAuditEvents(context.Background(), owner, project.ID)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, event := range events {
		if event.Action == "validation_issue.disposition" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("disposition audit count = %d, want exactly one", count)
	}
}

func TestReviewerMayOnlyDismissBlockingIssueInAssignedScope(t *testing.T) {
	service := testStore(t, filepath.Join(t.TempDir(), "issue-reviewer.db"))
	owner := Actor{TenantID: 402, UserID: "owner", Role: types.TenantRoleOwner}
	project := seedActiveG3Project(t, service, owner)
	chapters, err := service.ListChapters(context.Background(), owner, project.ID)
	if err != nil || len(chapters) == 0 {
		t.Fatalf("chapters: %+v err=%v", chapters, err)
	}
	reviewer := Actor{TenantID: owner.TenantID, UserID: "reviewer", Role: types.TenantRoleContributor}
	seedTenantMember(t, service, reviewer)
	if _, _, _, err := service.SaveMembers(context.Background(), owner, project.ID, "issue-reviewer-assign", SaveMembersInput{
		ExpectedProjectVersion: project.ProjectVersion,
		Members:                []Member{{UserID: reviewer.UserID, GovernanceRole: "member", FunctionRoles: []string{"reviewer"}, FunctionScopes: map[string][]string{"reviewer": {chapters[0].ID}}}},
	}); err != nil {
		t.Fatalf("assign reviewer scope: %v", err)
	}
	project, err = service.GetProject(context.Background(), owner, project.ID)
	if err != nil {
		t.Fatal(err)
	}
	blocking := issueBinding(project.ProjectVersion, "blocking")
	blocking.TargetID = chapters[0].ID
	if _, _, _, err := service.RecordValidationIssueDisposition(context.Background(), reviewer, project.ID, "reviewer-dismiss-001", ValidationIssueDispositionInput{
		ExpectedProjectVersion: project.ProjectVersion, ValidationIssueBinding: blocking, Action: "dismiss", Reason: "经核对，该规则对此章节不适用",
	}); err != nil {
		t.Fatalf("in-scope blocking dismiss: %v", err)
	}

	outOfScope := blocking
	outOfScope.IssueID = "issue-out-of-scope"
	outOfScope.TargetID = chapters[len(chapters)-1].ID
	if _, _, _, err := service.RecordValidationIssueDisposition(context.Background(), reviewer, project.ID, "reviewer-dismiss-002", ValidationIssueDispositionInput{
		ExpectedProjectVersion: project.ProjectVersion, ValidationIssueBinding: outOfScope, Action: "dismiss", Reason: "not in my scope",
	}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("out-of-scope dismiss error = %v, want ErrNotFound", err)
	}
	warning := blocking
	warning.IssueID = "issue-warning"
	warning.Severity = "warning"
	if _, _, _, err := service.RecordValidationIssueDisposition(context.Background(), reviewer, project.ID, "reviewer-waive-001", ValidationIssueDispositionInput{
		ExpectedProjectVersion: project.ProjectVersion, ValidationIssueBinding: warning, Action: "waive", Reason: "not permitted for reviewer",
	}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("reviewer waive error = %v, want ErrNotFound", err)
	}
}

func TestDispositionRejectsRevokedMembershipWithoutAuditSideEffect(t *testing.T) {
	service := testStore(t, filepath.Join(t.TempDir(), "issue-disposition-revoked.db"))
	owner := Actor{TenantID: 403, UserID: "owner"}
	project := seedActiveG3Project(t, service, owner)
	if err := service.testDB().Exec("UPDATE lingdoc_member_permissions SET status = 'suspended' WHERE project_id = ? AND user_id = ?", project.ID, owner.UserID).Error; err != nil {
		t.Fatal(err)
	}
	_, _, _, err := service.RecordValidationIssueDisposition(context.Background(), owner, project.ID, "issue-revoked-001", ValidationIssueDispositionInput{
		ExpectedProjectVersion: project.ProjectVersion, ValidationIssueBinding: issueBinding(project.ProjectVersion, "warning"), Action: "dismiss", Reason: "撤权后不得写入",
	})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("revoked disposition error = %v, want ErrNotFound", err)
	}
	var count int64
	if err := service.testDB().Table("lingdoc_project_audits").Where("project_id = ? AND action = ?", project.ID, "validation_issue.disposition").Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("revoked action left %d audit events (err=%v), want none", count, err)
	}
}
