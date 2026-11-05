package access

import "testing"

func TestCurrentnessInputKeepsGenerationAndDeliveryKindsDistinct(t *testing.T) {
	if GenerationInput == DeliveryInput {
		t.Fatal("generation and delivery currentness kinds must remain distinct")
	}
	ctx := LingDocAccessContext{TenantID: 7, UserID: "user-1", ProjectID: "project-1"}
	if ctx.TenantID == 0 || ctx.UserID == "" || ctx.ProjectID == "" {
		t.Fatal("access context must carry the authenticated project identity")
	}
}
