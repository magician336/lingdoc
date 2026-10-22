package workspacecore

import (
	"context"
	"path/filepath"
	"testing"
)

// 第二个求值器（模板治理求值）从**同一条规则判断**得出**同一条 finding**，但它不
// 发布交付链的 issue id——那个 id 是交付链的把手，不是 finding 本身的身份。因此读
// 处置时要按 finding 身份匹配：规则、规则集摘要、严重度、目标、目标版本、项目版本。
func TestValidationIssueDispositionsByFindingMatchesTheFindingNotTheIssueID(t *testing.T) {
	service := testStore(t, filepath.Join(t.TempDir(), "issue-finding.db"))
	owner := Actor{TenantID: 404, UserID: "owner"}
	project := seedActiveImpactProject(t, service, owner)
	binding := issueBinding(project.ProjectVersion, "blocking")
	if _, _, _, err := service.RecordValidationIssueDisposition(context.Background(), owner, project.ID, "finding-dismiss-001", ValidationIssueDispositionInput{
		ExpectedProjectVersion: project.ProjectVersion, ValidationIssueBinding: binding, Action: "dismiss", Reason: "经核对属于不适用项",
	}); err != nil {
		t.Fatalf("record disposition: %v", err)
	}

	// 另一个把手指向同一条 finding：处置必须照样生效。
	other := binding
	other.IssueID = "vi-other-handle"
	err := service.repository.Transaction(context.Background(), TransactionOptions{ReadOnly: true}, func(tx Transaction) error {
		found, err := service.ValidationIssueDispositionsInTransaction(context.Background(), tx, owner, project.ID, []ValidationIssueBinding{other})
		if err != nil {
			return err
		}
		if found[other.IssueID].Action != "dismiss" {
			t.Fatalf("the same finding under another handle = %+v, want the recorded dismissal", found)
		}

		// 目标版本一变：处置不再适用，问题重新出现。
		newTarget := other
		version := "chapter-v5"
		newTarget.TargetVersion = &version
		found, err = service.ValidationIssueDispositionsInTransaction(context.Background(), tx, owner, project.ID, []ValidationIssueBinding{newTarget})
		if err != nil {
			return err
		}
		if len(found) != 0 {
			t.Fatalf("a disposition bound to another target version leaked: %+v", found)
		}

		// 项目版本一变：同理。
		newProject := other
		newProject.ProjectVersion = binding.ProjectVersion + 1
		found, err = service.ValidationIssueDispositionsInTransaction(context.Background(), tx, owner, project.ID, []ValidationIssueBinding{newProject})
		if err != nil {
			return err
		}
		if len(found) != 0 {
			t.Fatalf("a disposition bound to another project version leaked: %+v", found)
		}

		// 规则集摘要一变：同理。
		newRuleset := other
		newRuleset.RulesetHash = "ruleset-8"
		found, err = service.ValidationIssueDispositionsInTransaction(context.Background(), tx, owner, project.ID, []ValidationIssueBinding{newRuleset})
		if err != nil {
			return err
		}
		if len(found) != 0 {
			t.Fatalf("a disposition bound to another ruleset leaked: %+v", found)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
