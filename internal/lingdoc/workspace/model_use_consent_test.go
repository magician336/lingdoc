package workspace

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/evidence"
	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"gorm.io/gorm"
)

// The model-use consent.
//
// A generation may only use a material whose owner explicitly allowed model
// use, and that consent is a *record*: the production chain writes it (policy
// proposal -> assess -> apply).  Three facts have to stay true, and each of
// them is pinned here:
//
//  1. nothing is allowed by default.  With no consent record the policy
//     carries ModelUseAllowed=false and the model-use gateway refuses the
//     material.  If somebody ever changes that default to true, these cases
//     turn red instead of silently letting every material into a generation.
//  2. the chain really writes that record, so the acceptance contract can
//     drive it instead of inserting a row by hand.
//  3. the consent is pinned to the material revision it was granted for: a
//     material that moved on does not keep the old consent.
//
// The HTTP route is a thin adapter over these service calls
// (internal/lingdoc/workspace/agent_routes.go:205-221 and
// internal/lingdoc/workspace/http.go:144), so the service is where the
// behaviour lives; the contract's own nodes are checked against the OpenAPI
// by the static contract validator.

type modelUseConsentFixture struct {
	handler *Handler
	service *core.Service
	db      *gorm.DB
	assetID string
	actor   core.Actor
}

// modelUseConsentShares stands in for the external sharing service.  The
// same-tenant read path does not consult it; the KB read decision in this
// fixture is made by the allow-all test authorizer.
type modelUseConsentShares struct{ interfaces.KBShareService }

// applyProductionG2ObjectMigration creates the object store tables the way
// production does: the statements come from the production migration
// (migrations/sqlite/000037_lingdoc_full_workflow.up.sql), not from a copy of
// its DDL written into this file.  A fixture that carries its own copy of a
// production schema is exactly the shape that breaks when the schema moves.
func applyProductionG2ObjectMigration(t *testing.T, db *gorm.DB) {
	t.Helper()
	path := filepath.Join("..", "..", "..", "migrations", "sqlite", "000037_lingdoc_full_workflow.up.sql")
	script, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read the production migration: %v", err)
	}
	wanted := map[string]bool{
		"CREATE TABLE lingdoc_g2_objects":         true,
		"CREATE TABLE lingdoc_g2_object_versions": true,
	}
	applied := 0
	for _, statement := range strings.Split(string(script), ";") {
		trimmed := strings.TrimSpace(statement)
		for prefix := range wanted {
			if strings.HasPrefix(trimmed, prefix) {
				if err := db.Exec(trimmed).Error; err != nil {
					t.Fatalf("apply the production migration statement: %v", err)
				}
				applied++
			}
		}
	}
	if applied != len(wanted) {
		t.Fatalf("生产迁移里的 g2 对象表建表语句 = %d 条, want %d 条（迁移形状变了？）", applied, len(wanted))
	}
}

func newModelUseConsentFixture(t *testing.T) modelUseConsentFixture {
	t.Helper()
	handler, db, assetID := seedBoundSource(t)
	seedDeliveryWorkspace(t, db,
		deliveryQuestionChapter(assetID, currentAssetRevision(t, handler, assetID), deliveryBody, []string{deliverySourceID}),
		deliveryMethodChapter(),
	)
	ensureImpactRouteTables(t, db)
	applyProductionG2ObjectMigration(t, db)
	if err := db.Exec("UPDATE lingdoc_projects SET current_context_revision = ? WHERE id = ?", 1, "project-1").Error; err != nil {
		t.Fatalf("seed context revision: %v", err)
	}
	seedDeliveryBoundTemplateCopy(t, db, "consent-copy")
	service, ok := testRuntime(handler).projects.(*core.Service)
	if !ok {
		t.Fatalf("workspace handler service = %T, want *core.Service", testRuntime(handler).projects)
	}
	// The consent record is read through the agent runtime, so the runtime has
	// to be wired the way production wires it (candidate_oneconn_test.go does
	// the same for the candidate path).
	adapter := NewResearchAdapter(db, service, testRuntime(handler), modelUseConsentShares{}, nil, nil)
	service.ConfigureAgentRuntime(core.AgentRuntime{Sources: adapter})
	return modelUseConsentFixture{
		handler: handler,
		service: service,
		db:      db,
		assetID: assetID,
		actor:   core.Actor{TenantID: 7, UserID: deliveryActorID},
	}
}

// readPolicy reads the consent record the way the service reads it: with no
// record at all this is the *default* policy, and its ModelUseAllowed is the
// zero value.
func (f modelUseConsentFixture) readPolicy(t *testing.T) core.ProjectAssetPolicy {
	t.Helper()
	policy, err := f.service.GetProjectAssetPolicy(policyContext(), f.actor, "project-1", f.assetID)
	if err != nil {
		t.Fatalf("read the project asset policy: %v", err)
	}
	return policy
}

// modelUseDecision is what a generation path sees: the model-use gateway
// decides on top of the ordinary asset gateway, so the ordinary answer
// (the material is bound and readable) is not enough.
func (f modelUseConsentFixture) modelUseDecision(t *testing.T) *evidence.ResolveResult {
	t.Helper()
	gateway := NewModelAssetPolicyGateway(f.db, testRuntime(f.handler).gateway)
	result, err := gateway.ResolveAllowed(policyContext(), "project-1",
		evidence.Actor{TenantID: "7", UserID: deliveryActorID}, []string{f.assetID})
	if err != nil {
		t.Fatalf("resolve the model-use decision: %v", err)
	}
	return result
}

// grantModelUseConsent runs the production chain: a policy proposal becomes a
// draft, the draft is assessed, and the assessed change set is applied.  That
// last step is the one that writes the record.
func (f modelUseConsentFixture) grantModelUseConsent(t *testing.T) {
	t.Helper()
	ctx := policyContext()
	current := f.readPolicy(t)
	input := core.ExtendedChangeSetInput{
		ExpectedContextRevision: 1,
		Reason:                  "Owner 明确同意把这份合成资料用于模型生成",
		SemanticDeltas: []core.SemanticDelta{{
			Kind:            "ProjectAssetScope",
			TargetID:        f.assetID,
			ExpectedVersion: current.Version,
			ProposedAssetPolicy: &core.ProjectAssetPolicy{
				Scope:           "personal",
				Purpose:         "合成材料上的模型生成许可",
				AllowedUserIDs:  []string{},
				ModelUseAllowed: true,
			},
		}},
	}
	raw, status, _, err := f.service.CreateChangeDraft(ctx, f.actor, "project-1", "consent-draft-1", input)
	if err != nil {
		t.Fatalf("create the consent draft: %v", err)
	}
	if status != http.StatusCreated {
		t.Fatalf("create the consent draft status = %d, want 201", status)
	}
	var draft struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}
	if err := json.Unmarshal(raw, &draft); err != nil {
		t.Fatalf("decode the consent draft: %v", err)
	}
	if draft.ID == "" {
		t.Fatalf("the consent draft has no id: %s", raw)
	}
	if _, status, _, err := f.service.AssessChangeSet(ctx, f.actor, "project-1", draft.ID, "consent-assess-1"); err != nil {
		t.Fatalf("assess the consent change set: %v", err)
	} else if status != http.StatusOK {
		t.Fatalf("assess the consent change set status = %d, want 200", status)
	}
	if _, status, _, err := f.service.ApplyChangeSet(ctx, f.actor, "project-1", draft.ID, "consent-apply-1"); err != nil {
		t.Fatalf("apply the consent change set: %v", err)
	} else if status != http.StatusOK {
		t.Fatalf("apply the consent change set status = %d, want 200", status)
	}
}

func TestModelUseConsentIsNotGrantedByDefault(t *testing.T) {
	f := newModelUseConsentFixture(t)

	policy := f.readPolicy(t)
	if policy.ModelUseAllowed {
		t.Fatal("没有许可记录却放行模型使用：默认 policy 的 ModelUseAllowed 必须是零值 false")
	}

	result := f.modelUseDecision(t)
	if len(result.Allowed) != 0 {
		t.Fatalf("没有许可记录时模型用途仍放行：%+v", result.Allowed)
	}
	if len(result.Denied) != 1 || result.Denied[0].AssetID != f.assetID || result.Denied[0].Reason != evidence.DenyNotAuthorized {
		t.Fatalf("没有许可记录时的判定 = %+v，必须是拒绝（not_authorized）", result.Denied)
	}
}

func TestModelUseConsentChainWritesTheRecordTheModelUseDecisionFollows(t *testing.T) {
	f := newModelUseConsentFixture(t)

	f.grantModelUseConsent(t)

	policy := f.readPolicy(t)
	if !policy.ModelUseAllowed {
		t.Fatal("许可链跑完了，读回来的 policy 却没有模型使用许可")
	}
	if policy.AssetRevision != 2 {
		t.Fatalf("许可钉住的资料修订 = %d, want 2（应用这一步把资料推进到新一版）", policy.AssetRevision)
	}

	// The record is a stored object, not a computed answer: the acceptance
	// contract has to be able to produce it, not read a promise.
	var payload string
	row := f.db.Raw("SELECT payload_json FROM lingdoc_g2_objects WHERE project_id = ? AND kind = ? AND id = ?",
		"project-1", core.AssetPolicyKind, f.assetID).Take(&payload)
	if row.Error != nil {
		t.Fatalf("read the stored consent object: %v", row.Error)
	}
	if row.RowsAffected != 1 {
		t.Fatalf("许可记录行数 = %d, want 1（它必须真的落在对象存储里）", row.RowsAffected)
	}
	var stored core.ProjectAssetPolicy
	if err := json.Unmarshal([]byte(payload), &stored); err != nil {
		t.Fatalf("decode the stored consent: %v", err)
	}
	if !stored.ModelUseAllowed {
		t.Fatal("存下来的许可对象没有模型使用许可")
	}

	result := f.modelUseDecision(t)
	if len(result.Allowed) != 1 || result.Allowed[0].ID != f.assetID {
		t.Fatalf("许可建立后模型用途仍被拒：%+v / %+v", result.Allowed, result.Denied)
	}
}

func TestModelUseConsentDoesNotFollowAChangedMaterialRevision(t *testing.T) {
	f := newModelUseConsentFixture(t)

	f.grantModelUseConsent(t)
	if result := f.modelUseDecision(t); len(result.Allowed) != 1 {
		t.Fatalf("许可刚建立时模型用途就未被放行：%+v / %+v", result.Allowed, result.Denied)
	}

	// The material moved on: a new revision.  The consent was granted for the
	// old one, so it must not follow.
	if err := f.db.Exec("UPDATE lingdoc_project_assets SET asset_revision = ? WHERE id = ?", 3, f.assetID).Error; err != nil {
		t.Fatalf("advance the material revision: %v", err)
	}

	policy := f.readPolicy(t)
	if policy.ModelUseAllowed {
		t.Fatal("资料修订前进后旧许可仍在放行：同意必须钉在它签发时的那一版修订上")
	}
	result := f.modelUseDecision(t)
	if len(result.Allowed) != 0 {
		t.Fatalf("资料修订前进后模型用途仍放行：%+v", result.Allowed)
	}
	if len(result.Denied) != 1 || result.Denied[0].Reason != evidence.DenyNotAuthorized {
		t.Fatalf("资料修订前进后的判定 = %+v，必须是拒绝（not_authorized）", result.Denied)
	}
}
