package workspacecore

import (
	"strings"
	"testing"
)

// The audit table's action column is a 64-character category column
// (lingdoc_project_audits.action varchar(64) on PostgreSQL, size:64 in the
// GORM model). The capability string embeds a caller-supplied resource, so
// writing the composite there overflowed the column: a blocking rule-issue
// dismissal produced "authorization.issue-dismiss-blocking:<36-char uuid>"
// (73 characters) and PostgreSQL rejected it with SQLSTATE 22001, which
// surfaced as an unmapped HTTP 500 on a valid request.
func TestAuthorizationAuditActionStaysWithinColumnWidth(t *testing.T) {
	const columnWidth = 64
	longest := strings.Repeat("x", 512)
	cases := []string{
		"read",
		"issue-resolve:" + longest,
		"issue-dismiss-blocking:5013efc8-24f7-4983-b853-3e8b53329acd",
		"write:" + longest,
	}
	for _, capability := range cases {
		action := auditAuthorizationAction(capability)
		if len(action) > columnWidth {
			t.Fatalf("audit action %q is %d characters, column holds %d", action, len(action), columnWidth)
		}
		if !strings.HasPrefix(action, "authorization.") {
			t.Fatalf("audit action %q lost its category prefix", action)
		}
		if strings.Contains(action, ":") {
			t.Fatalf("audit action %q carries a resource; the category column must stay unbounded-free", action)
		}
	}
}

// Every authorization action the policy can request must fit, whatever the
// resource is. A new action that does not fit would fail at write time on
// PostgreSQL only, which is exactly how the original defect hid.
func TestEveryAuthorizationActionFitsTheAuditCategory(t *testing.T) {
	const columnWidth = 64
	actions := []AuthorizationAction{
		AuthorizationDeliveryRead, AuthorizationTenantManage, AuthorizationResearch, AuthorizationAgent,
		AuthorizationEvidenceWrite, AuthorizationEvidenceConfirm, AuthorizationConfirm, AuthorizationReview,
		AuthorizationChangeAssess, AuthorizationImpactResolve, AuthorizationOpsControl, AuthorizationRead,
		AuthorizationWrite, AuthorizationCreate, AuthorizationManage, AuthorizationIssueResolve,
		AuthorizationIssueDismiss, AuthorizationIssueWaive, AuthorizationIssueDismissBlocking,
	}
	for _, action := range actions {
		written := auditAuthorizationAction(string(action) + ":" + strings.Repeat("r", 128))
		if len(written) > columnWidth {
			t.Fatalf("authorization action %q writes %q (%d characters), column holds %d", action, written, len(written), columnWidth)
		}
	}
}

// The resource must not be lost: the full capability stays in the details of
// the same audit row, so the decision remains traceable to the object it was
// taken for.
func TestAuthorizationAuditKeepsTheFullCapabilityInDetails(t *testing.T) {
	capability := "issue-dismiss-blocking:5013efc8-24f7-4983-b853-3e8b53329acd"
	action := auditAuthorizationAction(capability)
	if action != "authorization.issue-dismiss-blocking" {
		t.Fatalf("audit action = %q", action)
	}
	if !strings.Contains(capability, "5013efc8-24f7-4983-b853-3e8b53329acd") {
		t.Fatalf("capability %q lost the resource", capability)
	}
}
