package membership

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
	"github.com/Tencent/WeKnora/internal/types"
)

// issueBinding names the validation issue a disposition answers.
//
// It is a copy, not a move: the aggregate's own disposition tests still use
// theirs.
func issueBinding(projectVersion int64, severity string) core.ValidationIssueBinding {
	version := "chapter-v4"
	return core.ValidationIssueBinding{
		IssueID: "issue-1", RuleID: "required_fields", RulesetHash: "ruleset-7",
		Severity: severity, TargetID: "chapter-1", TargetVersion: &version, ProjectVersion: projectVersion,
	}
}

func TestReviewerMayOnlyDismissBlockingIssueInAssignedScope(t *testing.T) {
	w := newTestWorkspace(t, filepath.Join(t.TempDir(), "issue-reviewer.db"))
	owner := core.Actor{TenantID: 402, UserID: "owner", Role: types.TenantRoleOwner}
	project := seedActiveImpactProject(t, w.svc, w.db, owner)
	chapters, err := w.svc.ListChapters(context.Background(), owner, project.ID)
	if err != nil || len(chapters) == 0 {
		t.Fatalf("chapters: %+v err=%v", chapters, err)
	}
	reviewer := core.Actor{TenantID: owner.TenantID, UserID: "reviewer", Role: types.TenantRoleContributor}
	seedTenantMember(t, w.db, reviewer)
	if _, _, _, err := w.member.SaveMembers(
		context.Background(),
		owner,
		project.ID,
		"issue-reviewer-assign",
		core.SaveMembersInput{
			ExpectedProjectVersion: project.ProjectVersion,
			Members: []core.Member{{
				UserID:         reviewer.UserID,
				GovernanceRole: "member",
				FunctionRoles:  []string{"reviewer"},
				FunctionScopes: map[string][]string{"reviewer": {chapters[0].ID}},
			}},
		},
	); err != nil {
		t.Fatalf("assign reviewer scope: %v", err)
	}
	project, err = w.svc.GetProject(context.Background(), owner, project.ID)
	if err != nil {
		t.Fatal(err)
	}
	blocking := issueBinding(project.ProjectVersion, "blocking")
	blocking.TargetID = chapters[0].ID
	if _, _, _, err := w.svc.RecordValidationIssueDisposition(
		context.Background(),
		reviewer,
		project.ID,
		"reviewer-dismiss-001",
		core.ValidationIssueDispositionInput{
			ExpectedProjectVersion: project.ProjectVersion,
			ValidationIssueBinding: blocking,
			Action:                 "dismiss",
			Reason:                 "经核对，该规则对此章节不适用",
		},
	); err != nil {
		t.Fatalf("in-scope blocking dismiss: %v", err)
	}

	outOfScope := blocking
	outOfScope.IssueID = "issue-out-of-scope"
	outOfScope.TargetID = chapters[len(chapters)-1].ID
	if _, _, _, err := w.svc.RecordValidationIssueDisposition(
		context.Background(),
		reviewer,
		project.ID,
		"reviewer-dismiss-002",
		core.ValidationIssueDispositionInput{
			ExpectedProjectVersion: project.ProjectVersion,
			ValidationIssueBinding: outOfScope,
			Action:                 "dismiss",
			Reason:                 "not in my scope",
		},
	); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("out-of-scope dismiss error = %v, want ErrNotFound", err)
	}
	warning := blocking
	warning.IssueID = "issue-warning"
	warning.Severity = "warning"
	if _, _, _, err := w.svc.RecordValidationIssueDisposition(
		context.Background(),
		reviewer,
		project.ID,
		"reviewer-waive-001",
		core.ValidationIssueDispositionInput{
			ExpectedProjectVersion: project.ProjectVersion,
			ValidationIssueBinding: warning,
			Action:                 "waive",
			Reason:                 "not permitted for reviewer",
		},
	); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("reviewer waive error = %v, want ErrNotFound", err)
	}
}
