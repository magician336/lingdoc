package membership

import (
	"context"
	"errors"
	"testing"
	"time"

	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
	"github.com/Tencent/WeKnora/internal/types"
)

type machineReadTestPolicy struct {
	check func(context.Context, core.MachineReadAuthorization) error
	calls int
}

func (p *machineReadTestPolicy) Validate(context.Context, string, string, []string) error {
	return core.ErrInvalidState // Generic human policy must never be used for this subject.
}

func (p *machineReadTestPolicy) ValidateMachineRead(ctx context.Context, a core.MachineReadAuthorization) error {
	p.calls++
	if !a.MatchesContext(ctx) {
		return core.ErrNotFound
	}
	principal, _ := types.PrincipalFromContext(ctx)
	if principal.Type != types.PrincipalAPITenant || principal != a.Principal() {
		return core.ErrNotFound
	}
	if p.check != nil {
		return p.check(ctx, a)
	}
	return nil
}

// machineReadCoreFixture builds a workspace whose project holds a machine API
// key grant, and returns the context a machine read arrives with.
func machineReadCoreFixture(
	t *testing.T,
) (*testWorkspace, core.Actor, core.Project, context.Context, *machineReadTestPolicy) {
	t.Helper()
	w, owner, _, p := firstPassMemberFixture(t)
	if err := w.db.AutoMigrate(&types.User{}, &types.TenantAPIKey{}); err != nil {
		t.Fatal(err)
	}
	if err := w.db.Create(&types.User{
		ID: owner.UserID, Username: "synthetic-owner",
		Email: "machine-core@example.invalid", TenantID: owner.TenantID, IsActive: true,
	}).Error; err != nil {
		t.Fatal(err)
	}
	tenant := owner.TenantID
	key := types.TenantAPIKey{
		ID: 17, TenantID: &tenant, ScopeType: types.APIKeyScopeTenant,
		Name: "synthetic machine envelope", KeyHash: "synthetic-machine-key-hash",
		Capabilities:     types.StringArray{string(types.APIKeyCapabilityRetrieve)},
		KnowledgeBaseIDs: types.StringArray{"permitted-kb"},
	}
	if err := w.db.Create(&key).Error; err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := w.member.SaveProjectAPIKeyGrant(context.Background(), owner, p.ID, "machine-core-grant",
		core.ProjectAPIKeyGrant{
			KeyID: key.ID, Capabilities: []string{"read"}, Active: true,
			ExpectedProjectVersion: p.ProjectVersion, Reason: "synthetic delegation",
		}); err != nil {
		t.Fatal(err)
	}
	policy := &machineReadTestPolicy{}
	w.setSources(policy)
	pool, err := w.db.DB()
	if err != nil {
		t.Fatal(err)
	}
	pool.SetMaxOpenConns(1)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	ctx = context.WithValue(ctx, types.TenantIDContextKey, owner.TenantID)
	ctx = types.WithPrincipal(ctx, types.Principal{Type: types.PrincipalAPITenant, ID: "8123"})
	ctx = types.WithTenantAPIKeyScope(ctx, types.TenantAPIKeyScope{
		KeyID: key.ID, ScopeType: types.APIKeyScopeTenant,
		Capabilities:     types.StringArray{string(types.APIKeyCapabilityRetrieve)},
		KnowledgeBaseIDs: types.StringArray{"permitted-kb"},
	})
	// Emulate auth's legacy tenant-user alias without ever claiming a web
	// principal. The machine seam must ignore this alias as an actor identity.
	ctx = types.WithCaller(ctx, types.Caller{
		TenantID: owner.TenantID, UserID: owner.UserID, Role: types.TenantRoleOwner,
	})
	return w, owner, p, ctx, policy
}

func TestMachineReadCurrentNativeGrantAndGrantor(t *testing.T) {
	w, _, p, ctx, policy := machineReadCoreFixture(t)
	summary, err := w.svc.ReadMachineProjectSummary(ctx, p.ID)
	if err != nil || summary.ID != p.ID || policy.calls != 1 {
		t.Fatalf("machine summary: %+v %v calls=%d", summary, err, policy.calls)
	}
	if err := core.RequireHumanDecision(ctx); !errors.Is(err, core.ErrHumanDecisionRequired) {
		t.Fatalf("read changed machine into human: %v", err)
	}
	var audit auditEventRow
	if err := w.db.Where("project_id=? AND action='machine.read'", p.ID).Take(&audit).Error; err != nil {
		t.Fatal(err)
	}
	if audit.ActorID != "api_tenant:8123" {
		t.Fatalf("audit invented human actor: %+v", audit)
	}
	if _, err := w.svc.ReadMachineProjectSummary(ctx, "another-project"); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("ungranted project: %v", err)
	}
}

func TestMachineReadRevocationAndMissingDependencyFailClosed(t *testing.T) {
	for _, change := range []string{
		"native-revoked", "native-capability-removed", "grant-revoked", "grantor-tenant-suspended",
		"grantor-role-downgraded", "grantor-project-removed",
		"grantor-user-disabled", "wrong-principal", "missing-bridge",
	} {
		t.Run(change, func(t *testing.T) {
			w, owner, p, ctx, _ := machineReadCoreFixture(t)
			sql := ""
			switch change {
			case "native-revoked":
				sql = "UPDATE tenant_api_keys SET revoked_at=CURRENT_TIMESTAMP WHERE id=17"
			case "native-capability-removed":
				if err := w.db.Table("tenant_api_keys").Where("id=17").Update("capabilities",
					types.StringArray{string(types.APIKeyCapabilityChat)}).Error; err != nil {
					t.Fatal(err)
				}
			case "grant-revoked":
				if _, _, _, err := w.member.SaveProjectAPIKeyGrant(
					context.Background(), owner, p.ID, "machine-core-revoke",
					core.ProjectAPIKeyGrant{
						KeyID: 17, Capabilities: []string{"read"}, Active: false,
						ExpectedProjectVersion: p.ProjectVersion, Reason: "synthetic revoke",
					},
				); err != nil {
					t.Fatal(err)
				}
			case "grantor-tenant-suspended":
				sql = "UPDATE tenant_members SET status='suspended' WHERE user_id='owner'"
			case "grantor-role-downgraded":
				sql = "UPDATE tenant_members SET role='viewer' WHERE user_id='owner'"
			case "grantor-project-removed":
				sql = "UPDATE lingdoc_member_permissions SET status='removed' WHERE user_id='owner'"
			case "grantor-user-disabled":
				sql = "UPDATE users SET is_active=0 WHERE id='owner'"
			case "wrong-principal":
				ctx = types.WithPrincipal(ctx, types.Principal{Type: types.PrincipalWebUser, ID: owner.UserID})
			case "missing-bridge":
				w.setSources(nil)
			}
			if sql != "" {
				if err := w.db.Exec(sql).Error; err != nil {
					t.Fatal(err)
				}
			}
			summary, err := w.svc.ReadMachineProjectSummary(ctx, p.ID)
			want := error(core.ErrNotFound)
			if change == "missing-bridge" {
				want = core.ErrMachineReadDependency
			}
			if !errors.Is(err, want) || summary.ID != "" {
				t.Fatalf("revocation leaked summary: %+v err=%v want=%v", summary, err, want)
			}
		})
	}
}

func TestMachineReadPolicyIOReleasesConnectionAndRechecksCurrentness(t *testing.T) {
	for _, change := range []string{"project-changed", "key-revoked-during-policy"} {
		t.Run(change, func(t *testing.T) {
			w, _, p, ctx, policy := machineReadCoreFixture(t)
			policy.check = func(context.Context, core.MachineReadAuthorization) error {
				// A single connection is enforced. This DB write could not run if
				// the parent read transaction were still holding that connection.
				sql := "UPDATE lingdoc_projects SET name='changed while checking sources' WHERE id=?"
				if change == "key-revoked-during-policy" {
					return w.db.Exec("UPDATE tenant_api_keys SET revoked_at=CURRENT_TIMESTAMP WHERE id=17").Error
				}
				return w.db.Exec(sql, p.ID).Error
			}
			summary, err := w.svc.ReadMachineProjectSummary(ctx, p.ID)
			want := core.ErrVersionConflict
			if change == "key-revoked-during-policy" {
				want = core.ErrNotFound
			}
			if !errors.Is(err, want) || summary.ID != "" || policy.calls != 1 {
				t.Fatalf("stale read survived policy IO: %+v err=%v calls=%d", summary, err, policy.calls)
			}
		})
	}
}

func TestMachineReadOnlyGrantCannotEnableGenericWrites(t *testing.T) {
	w, owner, p, ctx, _ := machineReadCoreFixture(t)
	if _, _, _, err := w.member.SaveProjectAPIKeyGrant(context.Background(), owner, p.ID, "machine-core-write-grant",
		core.ProjectAPIKeyGrant{
			KeyID: 17, Capabilities: []string{"write"}, Active: true,
			ExpectedProjectVersion: p.ProjectVersion, Reason: "must remain read-only",
		}); !errors.Is(err, core.ErrInvalidRequest) {
		t.Fatalf("write grant accepted: %v", err)
	}
	if err := w.svc.Authorize(ctx, owner, p.ID, "read"); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("generic Actor alias authorized machine: %v", err)
	}
	if _, _, _, err := w.svc.SaveSpec(ctx, owner, p.ID, "machine-core-forbidden-spec",
		core.SaveSpecInput{
			ExpectedSpecRevision: p.SpecRevision,
			Fields:               map[string]string{"research_subject": "forbidden"},
		},
	); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("machine generic write: %v", err)
	}
}

func TestMachineReadAuthenticatedCeilingCannotBeExpandedByCurrentRow(t *testing.T) {
	w, _, p, ctx, policy := machineReadCoreFixture(t)
	ctx = types.WithTenantAPIKeyScope(ctx, types.TenantAPIKeyScope{
		KeyID: 17, ScopeType: types.APIKeyScopeTenant,
		Capabilities:     types.StringArray{string(types.APIKeyCapabilityChat)},
		KnowledgeBaseIDs: types.StringArray{"permitted-kb"},
	})
	summary, err := w.svc.ReadMachineProjectSummary(ctx, p.ID)
	if !errors.Is(err, core.ErrNotFound) || summary.ID != "" || policy.calls != 0 {
		t.Fatalf("current row enlarged authenticated capability: %+v err=%v policy calls=%d",
			summary, err, policy.calls)
	}
}
