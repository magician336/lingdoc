package delivery

import "testing"

// 阻断集判定是「误报删除进入求值」这件事的**唯一**落点：判定规则住在这里，
// 两个求值器都走它。这里钉住判定本身，不涉及任何读侧。
func TestBlockingIssuesKeepsOnlyFindingsThatStillBlock(t *testing.T) {
	disposed := func(action string) *IssueDisposition {
		return &IssueDisposition{Action: action, Reason: "经核对", ActorID: "u-owner"}
	}
	findings := []CheckIssue{
		{ID: "vi-blocked", Severity: SeverityBlocking},
		{ID: "vi-dismissed", Severity: SeverityBlocking, Disposition: disposed(DispositionDismiss)},
		{ID: "vi-resolved", Severity: SeverityBlocking, Disposition: disposed(DispositionResolve)},
		{ID: "vi-waived", Severity: SeverityBlocking, Disposition: disposed(DispositionWaive)},
		{ID: "vi-warning", Severity: SeverityWarning, Disposition: disposed(DispositionDismiss)},
	}
	blocking := BlockingIssues(findings)
	if len(blocking) != 3 {
		t.Fatalf("blocking findings = %+v, want the blocked, resolved and waived ones", blocking)
	}
	for _, issue := range blocking {
		if issue.ID == "vi-dismissed" || issue.ID == "vi-warning" {
			t.Fatalf("issue %q must not block after a dismissal", issue.ID)
		}
	}
}

// 处置不改变问题本身的身份与严重度：判定只决定「还阻不阻断」。
func TestBlockingIssuesKeepsTheFindingItself(t *testing.T) {
	finding := CheckIssue{ID: "vi-dismissed", RuleID: "chapter-nonempty", Severity: SeverityBlocking,
		Disposition: &IssueDisposition{Action: DispositionDismiss, Reason: "经核对属于不适用项"}}
	blocking := BlockingIssues([]CheckIssue{finding})
	if len(blocking) != 0 {
		t.Fatalf("blocking = %+v, want none", blocking)
	}
	if finding.Severity != SeverityBlocking || finding.RuleID != "chapter-nonempty" || finding.Disposition == nil {
		t.Fatalf("the dismissed finding was rewritten: %+v", finding)
	}
}

func TestApplyDispositionsUnblocksTheEvaluationWithoutDroppingTheFinding(t *testing.T) {
	result := CheckResult{Status: CheckBlocked, Issues: []CheckIssue{
		{ID: "vi-dismissed", Severity: SeverityBlocking, Disposition: &IssueDisposition{Action: DispositionDismiss}},
	}}
	ApplyDispositions(&result)
	if result.Status != CheckPassed {
		t.Fatalf("status = %q, want passed once the only blocking finding is dismissed", result.Status)
	}
	if len(result.Issues) != 1 || result.Issues[0].ID != "vi-dismissed" || result.Issues[0].Disposition == nil {
		t.Fatalf("issues = %+v, want the dismissed finding to stay visible with its disposition", result.Issues)
	}
}

func TestApplyDispositionsKeepsBlockingWhenAnyFindingStillBlocks(t *testing.T) {
	result := CheckResult{Status: CheckBlocked, Issues: []CheckIssue{
		{ID: "vi-dismissed", Severity: SeverityBlocking, Disposition: &IssueDisposition{Action: DispositionDismiss}},
		{ID: "vi-open", Severity: SeverityBlocking},
	}}
	ApplyDispositions(&result)
	if result.Status != CheckBlocked {
		t.Fatalf("status = %q, want blocked while one blocking finding stands", result.Status)
	}
}

// resolve 与 waive 不解除阻断：判断依然成立，该由谁处置仍由谁处置。
func TestApplyDispositionsDoesNotUnblockOnResolveOrWaive(t *testing.T) {
	for _, action := range []string{DispositionResolve, DispositionWaive} {
		result := CheckResult{Status: CheckBlocked, Issues: []CheckIssue{
			{ID: "vi-1", Severity: SeverityBlocking, Disposition: &IssueDisposition{Action: action}},
		}}
		ApplyDispositions(&result)
		if result.Status != CheckBlocked {
			t.Fatalf("status after %q = %q, want blocked", action, result.Status)
		}
	}
}

// not_evaluated 是求值器的事实（没有可求值的规则集），不受处置影响。
func TestApplyDispositionsKeepsNotEvaluated(t *testing.T) {
	result := CheckResult{Status: CheckNotEvaluated, Issues: []CheckIssue{}}
	ApplyDispositions(&result)
	if result.Status != CheckNotEvaluated {
		t.Fatalf("status = %q, want not_evaluated", result.Status)
	}
}
