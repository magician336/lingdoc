package membership

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
	"github.com/Tencent/WeKnora/internal/types"
)

// These tests pin the resource scope of a project member: which chapter and
// which asset a member role may reach, and which global capability it must not
// reach.
//
// The member and owner context left the aggregate (Spec #47, ticket #268), so
// the member operations are asked of the member and owner service (w.member)
// and the workspace operations of the aggregate (w.svc). The assertions are
// unchanged: only the owner of each call moved.

func TestG5AuthReviewEmptyResearcherScopeDoesNotAuthorizeDraftSummaryOrAssets(t *testing.T) {
	w, owner, member, p := firstPassMemberFixture(t)
	ctx := context.Background()
	invitationRaw, _, _, err := w.member.InviteProjectMember(ctx, owner, p.ID, "empty-researcher-scope-invite", core.InviteMemberInput{
		ExpectedProjectVersion: p.ProjectVersion,
		Reason:                 "synthetic empty researcher scope denial",
		Assignment: core.Member{UserID: member.UserID, GovernanceRole: "member", FunctionRoles: []string{"researcher"},
			FunctionScopes: map[string][]string{"researcher": {}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	var invitation core.MemberInvitation
	if err := json.Unmarshal(invitationRaw, &invitation); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := w.member.AcceptProjectInvitation(ctx, member, p.ID, invitation.ID, "empty-researcher-scope-accept",
		core.AcceptMemberInput{ExpectedInvitationVersion: invitation.Version}); err != nil {
		t.Fatal(err)
	}

	permission, err := w.member.EffectivePermission(ctx, member, p.ID, "research")
	if err != nil || permission.Allowed || permission.ReasonCode != "capability_or_scope_denied" {
		t.Fatalf("empty researcher scope draft summary=%+v err=%v, want denied", permission, err)
	}
	if err := w.svc.Authorize(ctx, member, p.ID, "research"); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("empty researcher scope authorized global research: %v", err)
	}

	asset, err := core.NewAssetResourceRef("unscoped-asset")
	if err != nil {
		t.Fatal(err)
	}
	decisions, err := w.svc.AuthorizeResourceBatch(ctx, member, p.ID, core.AuthorizationResearch, []core.ResourceRef{asset})
	if err != nil || len(decisions) != 1 || decisions[0].Allowed || decisions[0].Reason != "not_authorized" {
		t.Fatalf("empty researcher scope draft asset decision=%+v err=%v, want denied", decisions, err)
	}
}

func TestG5ResourceBatchAlignsChapterAuthorAndResearcherAssetScopes(t *testing.T) {
	w, owner, member, project := firstPassMemberFixture(t)
	ctx := context.Background()
	updated, _, _, err := w.svc.SaveSpec(ctx, owner, project.ID, "resource-scope-spec", core.SaveSpecInput{
		ExpectedSpecRevision: project.SpecRevision,
		Fields:               map[string]string{"research_subject": "合成范围验证", "research_goal": "验证资源授权边界"},
	})
	if err != nil {
		t.Fatal(err)
	}
	project = asProject(t, updated)
	activated, _, _, err := w.svc.ActivateProject(ctx, owner, project.ID, "resource-scope-activate", core.ActivateProjectInput{
		ExpectedSpecRevision: project.SpecRevision, ExpectedProjectVersion: project.ProjectVersion, ReviewedProjectVersion: project.ProjectVersion,
	})
	if err != nil {
		t.Fatal(err)
	}
	project = asProject(t, activated)
	chapters, err := w.svc.ListChapters(ctx, owner, project.ID)
	if err != nil || len(chapters) == 0 {
		t.Fatalf("fixture chapters=%d err=%v", len(chapters), err)
	}
	chapterID := chapters[0].ID
	if err := w.db.AutoMigrate(&types.Chunk{}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"one", "two"} {
		if err := w.db.Exec("INSERT INTO lingdoc_project_assets(id,tenant_id,project_id,knowledge_id,asset_revision,processing_state,created_by) VALUES(?,?,?,?,?,?,?)",
			"asset-"+id, owner.TenantID, project.ID, "knowledge-"+id, 1, "ready", owner.UserID).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := w.db.Create(&types.Chunk{ID: "cited-source", TenantID: owner.TenantID, KnowledgeID: "knowledge-one"}).Error; err != nil {
		t.Fatal(err)
	}
	// The source-policy fixture is explicit; chapter persistence and subsequent
	// authorization use the real production service/repository.
	if _, _, _, err := w.svc.SaveChapter(ctx, owner, project.ID, chapterID, "resource-scope-citation", core.SaveChapterInput{
		ExpectedSpecRevision: project.SpecRevision, ExpectedChapterVersionID: chapters[0].CurrentVersionID,
		BodyMarkdown: "[[source:cited-source]]", SourceIDs: []string{"cited-source"},
	}); err != nil {
		t.Fatal(err)
	}
	project = authReviewProject(t, w, owner, project.ID)
	invitationRaw, _, _, err := w.member.InviteProjectMember(ctx, owner, project.ID, "resource-scope-invite", core.InviteMemberInput{
		ExpectedProjectVersion: project.ProjectVersion,
		Reason:                 "synthetic chapter resource authorization",
		Assignment: core.Member{UserID: member.UserID, GovernanceRole: "member", FunctionRoles: []string{"author"},
			FunctionScopes: map[string][]string{"author": {chapterID}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	var invitation core.MemberInvitation
	if err = json.Unmarshal(invitationRaw, &invitation); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err = w.member.AcceptProjectInvitation(ctx, member, project.ID, invitation.ID, "resource-scope-accept", core.AcceptMemberInput{ExpectedInvitationVersion: invitation.Version}); err != nil {
		t.Fatal(err)
	}
	chapterAsset, err := core.NewAssetResourceRefForChapter("asset-one", chapterID)
	if err != nil {
		t.Fatal(err)
	}
	decisions, err := w.svc.AuthorizeResourceBatch(ctx, member, project.ID, core.AuthorizationResearch, []core.ResourceRef{chapterAsset})
	if err != nil || len(decisions) != 1 || !decisions[0].Allowed {
		t.Fatalf("chapter author resource decision=%+v err=%v, want allowed", decisions, err)
	}
	for _, id := range []string{"asset-two", "missing-asset"} {
		unrelated, _ := core.NewAssetResourceRefForChapter(id, chapterID)
		decisions, err = w.svc.AuthorizeResourceBatch(ctx, member, project.ID, core.AuthorizationResearch, []core.ResourceRef{unrelated})
		if err != nil || len(decisions) != 1 || decisions[0].Allowed {
			t.Fatalf("chapter context granted unrelated asset %s: %+v %v", id, decisions, err)
		}
	}
	permission, err := w.member.EffectivePermission(ctx, member, project.ID, "research:"+chapterID)
	if err != nil || !permission.Allowed {
		t.Fatalf("chapter-targeted capability disagrees with chapter retrieval: %+v %v", permission, err)
	}
	permission, err = w.member.EffectivePermission(ctx, member, project.ID, "research")
	if err != nil || permission.Allowed {
		t.Fatalf("chapter scope leaked into global research: %+v %v", permission, err)
	}
	directAsset, _ := core.NewAssetResourceRef("asset-one")
	decisions, err = w.svc.AuthorizeResourceBatch(ctx, member, project.ID, core.AuthorizationResearch, []core.ResourceRef{directAsset})
	if err != nil || len(decisions) != 1 || decisions[0].Allowed || decisions[0].Reason != "not_authorized" {
		t.Fatalf("chapter author received project-wide asset scope: %+v err=%v", decisions, err)
	}
	project, err = w.svc.GetProject(ctx, owner, project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err = w.member.SetMemberAssignment(ctx, owner, project.ID, member.UserID, "resource-scope-researcher", core.MemberAssignmentInput{
		ExpectedProjectVersion: project.ProjectVersion,
		Reason:                 "synthetic exact researcher asset scope",
		Assignment: core.Member{UserID: member.UserID, GovernanceRole: "member", FunctionRoles: []string{"researcher"},
			FunctionScopes: map[string][]string{"researcher": {"asset:asset-one", "asset:asset-two"}}},
	}); err != nil {
		t.Fatal(err)
	}
	decisions, err = w.svc.AuthorizeResourceBatch(ctx, member, project.ID, core.AuthorizationResearch, []core.ResourceRef{directAsset})
	if err != nil || len(decisions) != 1 || !decisions[0].Allowed {
		t.Fatalf("researcher asset decision=%+v err=%v, want allowed", decisions, err)
	}
	previouslyAllowed, _ := core.NewAssetResourceRef("asset-two")
	decisions, err = w.svc.AuthorizeResourceBatch(ctx, member, project.ID, core.AuthorizationResearch, []core.ResourceRef{previouslyAllowed})
	if err != nil || len(decisions) != 1 || !decisions[0].Allowed {
		t.Fatalf("fixture never granted the subsequently revoked scope: %+v %v", decisions, err)
	}
	project = authReviewProject(t, w, owner, project.ID)
	if _, _, _, err := w.member.SetMemberAssignment(ctx, owner, project.ID, member.UserID, "resource-scope-revoke-two", core.MemberAssignmentInput{
		ExpectedProjectVersion: project.ProjectVersion, Reason: "synthetic revoke previously usable asset-two",
		Assignment: core.Member{UserID: member.UserID, GovernanceRole: "member", FunctionRoles: []string{"researcher"},
			FunctionScopes: map[string][]string{"researcher": {"asset:asset-one"}}},
	}); err != nil {
		t.Fatal(err)
	}
	// Suspend/restore must retain the current assignment, never the original
	// invitation's broader Author role or any historical resource assignment.
	for _, action := range []string{"suspend", "restore"} {
		project = authReviewProject(t, w, owner, project.ID)
		if _, _, _, err := w.member.ChangeMemberState(ctx, owner, project.ID, member.UserID, "resource-scope-"+action,
			core.MemberLifecycleInput{ExpectedProjectVersion: project.ProjectVersion, Action: action, Reason: "synthetic current-scope recovery"}); err != nil {
			t.Fatal(err)
		}
	}
	decisions, err = w.svc.AuthorizeResourceBatch(ctx, member, project.ID, core.AuthorizationResearch, []core.ResourceRef{directAsset})
	if err != nil || len(decisions) != 1 || !decisions[0].Allowed {
		t.Fatalf("restore lost current researcher scope: %+v %v", decisions, err)
	}
	unrelated, _ := core.NewAssetResourceRefForChapter("asset-two", chapterID)
	decisions, err = w.svc.AuthorizeResourceBatch(ctx, member, project.ID, core.AuthorizationResearch, []core.ResourceRef{unrelated})
	if err != nil || len(decisions) != 1 || decisions[0].Allowed {
		t.Fatalf("restore revived historical Author scope for unrelated asset: %+v %v", decisions, err)
	}
}
