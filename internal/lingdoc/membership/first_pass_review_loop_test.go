package membership

import (
	"context"
	"errors"
	"testing"

	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
	"github.com/Tencent/WeKnora/internal/types"
)

func TestFirstPassActivationRequiresCurrentHumanReadingAndBlocksBulkJoin(t *testing.T) {
	w, a, b, p := firstPassMemberFixture(t)
	ctx := context.Background()
	w.svc.RequireActivationRead(true)
	raw, _, _, err := w.svc.SaveSpec(ctx, a, p.ID, "conditions-original", core.SaveSpecInput{
		ExpectedSpecRevision: 0,
		Fields: map[string]string{
			"research_subject": "主题",
			"research_goal":    "目标",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	p = asProject(t, raw)
	input := core.ActivateProjectInput{
		ExpectedSpecRevision:   p.SpecRevision,
		ExpectedProjectVersion: p.ProjectVersion,
		ReviewedProjectVersion: p.ProjectVersion,
	}
	if _, _, _, err := w.svc.ActivateProject(ctx, a, p.ID, "without-read", input); !errors.Is(err,
		core.ErrInvalidRequest) {
		t.Fatalf("no reading receipt accepted: %v", err)
	}
	receipt, err := w.svc.RecordActivationRead(ctx, a, p.ID, p.ProjectVersion)
	if err != nil {
		t.Fatal(err)
	}
	raw, _, _, err = w.svc.SaveSpec(ctx, a, p.ID, "conditions-next", core.SaveSpecInput{
		ExpectedSpecRevision: p.SpecRevision,
		Fields: map[string]string{
			"research_subject": "主题",
			"research_goal":    "目标修改",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	p = asProject(t, raw)
	input.ExpectedProjectVersion = p.ProjectVersion
	input.ExpectedSpecRevision = p.SpecRevision
	input.ReviewedProjectVersion = p.ProjectVersion
	input.ActivationReadID = receipt.ID
	if _, _, _, err := w.svc.ActivateProject(ctx, a, p.ID, "stale-read", input); !errors.Is(err,
		core.ErrVersionConflict) {
		t.Fatalf("stale reading accepted: %v", err)
	}
	machine := types.WithTenantAPIKeyScope(ctx, types.TenantAPIKeyScope{KeyID: 1, FullAccess: true})
	if _, err := w.svc.RecordActivationRead(machine, a, p.ID, p.ProjectVersion); !errors.Is(err,
		core.ErrHumanDecisionRequired) {
		t.Fatalf("machine reading forged: %v", err)
	}
	if _, _, _, err := w.member.SaveMembers(ctx, a, p.ID, "bulk-add", core.SaveMembersInput{
		ExpectedProjectVersion: p.ProjectVersion,
		CollaboratorUserIDs:    []string{b.UserID},
	}); !errors.Is(err, core.ErrProjectInvitationRequired) {
		t.Fatalf("invitation bypass: %v", err)
	}
}
