package router

// The formal export under the owner_unavailable freeze, judged at the HTTP
// boundary.
//
// The formal export is the one Owner-only action that already asked whether
// the Owner may still act. What it could not say was *why* it refused: the
// refusal looked exactly like "this is not your project". The freeze is a
// domain fact, but the guarantee the product makes is a contract one -- the
// export endpoint answers with a reason, and the permission read outlet
// answers with the same reason -- so the assertions live on the HTTP seam:
// the production route, the production authentication, the production
// membership, and the production gate.
//
// This file carries the export's own cases. The shared fixture and the
// helpers it offers live next door, and are reused as they are.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/lingdoc/delivery"
	"github.com/Tencent/WeKnora/internal/lingdoc/workspace"
	"github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
	"github.com/Tencent/WeKnora/internal/types"
)

// ownerFreezeExportApplication is the formal-export entry point with the
// production gate in front of it.
//
// The gate is the production one -- the production workspacecore service over
// the production database -- because that is the only place where the freeze
// is decided. What this replaces is the rendering of the file, which cannot
// change whether the export is allowed: the refusal the caller sees is the
// gate's own.
type ownerFreezeExportApplication struct {
	projects *workspacecore.Service
}

func (e *ownerFreezeExportApplication) Start(ctx context.Context, user, pid, snapshot, key string) (delivery.ExportArtifact, bool, error) {
	tenant, _ := types.TenantIDFromContext(ctx)
	if err := e.projects.AuthorizeDeliveryExport(ctx, workspacecore.Actor{TenantID: tenant, UserID: user}, pid); err != nil {
		return delivery.ExportArtifact{}, false, err
	}
	return delivery.ExportArtifact{
		ID: "export-" + snapshot, ProjectID: pid, SnapshotID: snapshot,
		Status: delivery.ExportVerified, FileSHA256: "synthetic-digest",
	}, false, nil
}

func (e *ownerFreezeExportApplication) Get(context.Context, string, string, string) (delivery.ExportArtifact, error) {
	return delivery.ExportArtifact{}, workspace.ErrNotFound
}

func (e *ownerFreezeExportApplication) Download(context.Context, string, string, string) (delivery.ExportArtifact, []byte, error) {
	return delivery.ExportArtifact{}, nil, workspace.ErrNotFound
}

func (e *ownerFreezeExportApplication) List(context.Context, string, string) ([]delivery.ExportArtifact, bool, error) {
	return nil, false, workspace.ErrNotFound
}

// newOwnerFreezeExportFixture is the shared production composition with the
// formal-export route mounted on it.
//
// The composition root mounts that route on the write group of the LingDoc
// group, behind the tenant's contributor ceiling. Here it is mounted the same
// way -- same path, same handler, same ceiling -- so the export the test
// calls is the export the product serves.
func newOwnerFreezeExportFixture(t *testing.T) *ownerFreezeFixture {
	t.Helper()
	f := newOwnerFreezeFixture(t)
	enabled := true
	guards := &rbacGuards{cfg: &config.Config{Tenant: &config.TenantConfig{EnableRBAC: &enabled}}}
	group := f.engine.Group("/api/v1/lingdoc", guards.Contributor())
	workspace.RegisterDeliveryExportRoutes(group,
		workspace.NewDeliveryExportHandler(&ownerFreezeExportApplication{projects: f.projects}))
	return f
}

// formalExportPath is the production formal-export endpoint. The snapshot is
// synthetic on purpose: the gate answers before any snapshot is read, so the
// refusal under test is the gate's and not the snapshot lookup's.
func formalExportPath(projectID string) string {
	return "/projects/" + projectID + "/releases/snapshot-synthetic/exports"
}

// lapseOwnerTenantRole takes the Owner's tenant role away from the
// contributor rights the Owner-only actions need, while the tenant membership
// itself stays active. The Owner can still read the project; the Owner-only
// actions are frozen.
func lapseOwnerTenantRole(t *testing.T, f *ownerFreezeFixture) {
	t.Helper()
	if err := f.db.Exec("UPDATE tenant_members SET role = 'viewer' WHERE tenant_id = 1 AND user_id = ?",
		"owner-user").Error; err != nil {
		t.Fatal(err)
	}
}

// TestOwnerUnavailableFreezesFormalExport is the formal export's half of the
// freeze: the Owner-only action is refused, the refusal names the freeze, and
// it stays distinguishable from "you are not the Owner".
func TestOwnerUnavailableFreezesFormalExport(t *testing.T) {
	f := newOwnerFreezeExportFixture(t)
	project := f.activeProject(t)
	chapters := f.chapters(t, project.ID)
	if len(chapters) == 0 {
		t.Fatalf("activated project has no chapter: %+v", project)
	}
	f.addMember(t, project.ID, chapters[0].ID)

	// The Owner's project membership lapses: the Owner may no longer act as a
	// tenant member of this project, while the tenant identity is untouched.
	f.lapseOwner(t, project.ID)

	frozen := f.request(t, http.MethodPost, formalExportPath(project.ID),
		`{"format":"docx"}`, "owner-freeze-export", f.tokens["owner-user"], 404)
	if reason := errorReason(t, frozen); reason != "owner_unavailable" {
		t.Fatalf("export refusal reason = %q, want owner_unavailable (%s)", reason, frozen.Body.String())
	}
	// The status code and the error code are the ones this refusal has always
	// answered with; only the reason rides along.
	if !strings.Contains(frozen.Body.String(), `"code":"not_found"`) {
		t.Fatalf("export refusal lost its error code: %s", frozen.Body.String())
	}

	// The member is refused because the member is not the Owner, and that
	// refusal names no freeze.
	notOwner := f.request(t, http.MethodPost, formalExportPath(project.ID),
		`{"format":"docx"}`, "owner-freeze-member-export", f.tokens["member-user"], 404)
	if reason := errorReason(t, notOwner); reason != "" {
		t.Fatalf("non-Owner export refusal reason = %q, want none (%s)", reason, notOwner.Body.String())
	}
	if !strings.Contains(notOwner.Body.String(), `"code":"not_found"`) {
		t.Fatalf("non-Owner export refusal lost its error code: %s", notOwner.Body.String())
	}

	// The permission read outlet answers with the same reason for the export
	// capability, so the interface can grey the entry out and say why.
	permission := f.request(t, http.MethodGet, "/projects/"+project.ID+"/permissions?capability=ops-control",
		"", "", f.tokens["member-user"], 200)
	allowed, reason := permissionReason(t, permission)
	if allowed || reason != "owner_unavailable" {
		t.Fatalf("export permission = allowed %v reason %q, want false / owner_unavailable (%s)",
			allowed, reason, permission.Body.String())
	}
}

// TestOwnerUnavailableFormalExportUnfreezesWhenTheIdentityComesBack is the
// derived part: the freeze is not a state anybody repairs, it is a fact that
// changes by itself.
func TestOwnerUnavailableFormalExportUnfreezesWhenTheIdentityComesBack(t *testing.T) {
	f := newOwnerFreezeExportFixture(t)
	project := f.activeProject(t)

	f.lapseOwner(t, project.ID)
	f.request(t, http.MethodPost, formalExportPath(project.ID),
		`{"format":"docx"}`, "owner-unfreeze-export-blocked", f.tokens["owner-user"], 404)

	// The Owner's project membership comes back. There is no repair action to
	// run: the next export simply runs.
	f.restoreOwner(t, project.ID)
	f.request(t, http.MethodPost, formalExportPath(project.ID),
		`{"format":"docx"}`, "owner-unfreeze-export-restored", f.tokens["owner-user"], 202)
}

// ownerAvailabilityAnswer reads the project level outlet's answer.
func ownerAvailabilityAnswer(t *testing.T, response *httptest.ResponseRecorder) (bool, []string) {
	t.Helper()
	var envelope struct {
		Data struct {
			Available     bool     `json:"available"`
			RecoveryPaths []string `json:"recovery_paths"`
		} `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode owner availability envelope: %v (%s)", err, response.Body.String())
	}
	return envelope.Data.Available, envelope.Data.RecoveryPaths
}

// TestOwnerUnavailableRefusalShapePerLapseForm is the four shapes the
// Owner's identity can take, judged at the HTTP boundary.
//
// The freeze is a domain fact, but the shape of the refusal is a contract
// one -- and the shape is not the same for every form:
//
//   - the project membership lapses: the request reaches the freeze gate,
//     and the refusal names the freeze;
//   - the tenant role is downgraded: the transport's own ceiling refuses
//     the write before the workspace is reached, and the read side is where
//     the freeze is named;
//   - the tenant membership is suspended or removed: the transport refuses
//     the request before it reaches the workspace at all, so neither the
//     write nor the read outlets answer.
//
// The last shape is a boundary of the transport, not a choice of the
// workspace: the project level outlet is refused by the same layer, so the
// recovery guidance reaches the remaining members and the tenant admin rather
// than the Owner whose identity was taken away. What never changes is that
// the member working on the project reads the freeze in every form.
func TestOwnerUnavailableRefusalShapePerLapseForm(t *testing.T) {
	t.Run("the project membership lapses", func(t *testing.T) {
		f := newOwnerFreezeExportFixture(t)
		project := f.activeProject(t)
		f.addMember(t, project.ID, f.chapters(t, project.ID)[0].ID)

		f.lapseOwner(t, project.ID)

		// The write reaches the freeze gate, and the refusal names it.
		frozen := f.request(t, http.MethodPost, formalExportPath(project.ID),
			`{"format":"docx"}`, "pf9-shape-project-membership", f.tokens["owner-user"], 404)
		if reason := errorReason(t, frozen); reason != "owner_unavailable" {
			t.Fatalf("export refusal reason = %q, want owner_unavailable (%s)", reason, frozen.Body.String())
		}

		// The member reads the same reason, so the interface can say why.
		permission := f.request(t, http.MethodGet, "/projects/"+project.ID+"/permissions?capability=manage",
			"", "", f.tokens["member-user"], 200)
		allowed, reason := permissionReason(t, permission)
		if allowed || reason != "owner_unavailable" {
			t.Fatalf("member permission = allowed %v reason %q, want false / owner_unavailable (%s)",
				allowed, reason, permission.Body.String())
		}
	})

	t.Run("the tenant role is downgraded", func(t *testing.T) {
		f := newOwnerFreezeExportFixture(t)
		project := f.activeProject(t)
		f.addMember(t, project.ID, f.chapters(t, project.ID)[0].ID)

		lapseOwnerTenantRole(t, f)

		// The write is refused by the transport's ceiling. That refusal is
		// the transport's own and it names no freeze.
		refused := f.request(t, http.MethodPost, formalExportPath(project.ID),
			`{"format":"docx"}`, "pf9-shape-tenant-role", f.tokens["owner-user"], 403)
		if strings.Contains(refused.Body.String(), "owner_unavailable") {
			t.Fatalf("the transport refusal names the freeze: %s", refused.Body.String())
		}

		// The read side is where the freeze is named: the Owner may still
		// read the project, and it reads its own export permission.
		permission := f.request(t, http.MethodGet, "/projects/"+project.ID+"/permissions?capability=ops-control",
			"", "", f.tokens["owner-user"], 200)
		allowed, reason := permissionReason(t, permission)
		if allowed || reason != "owner_unavailable" {
			t.Fatalf("downgraded export permission = allowed %v reason %q, want false / owner_unavailable (%s)",
				allowed, reason, permission.Body.String())
		}
	})

	for _, form := range []struct {
		name  string
		weave func(t *testing.T, f *ownerFreezeFixture)
	}{
		{
			name: "the tenant membership is suspended",
			weave: func(t *testing.T, f *ownerFreezeFixture) {
				if err := f.db.Exec("UPDATE tenant_members SET status = 'inactive' WHERE tenant_id = 1 AND user_id = ?",
					"owner-user").Error; err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "the tenant membership is removed",
			weave: func(t *testing.T, f *ownerFreezeFixture) {
				if err := f.db.Exec("DELETE FROM tenant_members WHERE tenant_id = 1 AND user_id = ?",
					"owner-user").Error; err != nil {
					t.Fatal(err)
				}
			},
		},
	} {
		t.Run(form.name, func(t *testing.T) {
			f := newOwnerFreezeExportFixture(t)
			project := f.activeProject(t)
			f.addMember(t, project.ID, f.chapters(t, project.ID)[0].ID)

			form.weave(t, f)

			// The transport refuses the request before the workspace is
			// reached, and it refuses the read outlets the same way.
			refused := f.request(t, http.MethodPost, formalExportPath(project.ID),
				`{"format":"docx"}`, "pf9-shape-tenant-membership", f.tokens["owner-user"], 403)
			if strings.Contains(refused.Body.String(), "owner_unavailable") {
				t.Fatalf("the transport refusal names the freeze: %s", refused.Body.String())
			}
			f.request(t, http.MethodGet, "/projects/"+project.ID+"/permissions?capability=manage",
				"", "", f.tokens["owner-user"], 403)
			f.request(t, http.MethodGet, "/projects/"+project.ID+"/owner-availability",
				"", "", f.tokens["owner-user"], 403)

			// The member still reads the freeze, and with it the ways back:
			// the recovery guidance reaches the people who can act on it.
			permission := f.request(t, http.MethodGet, "/projects/"+project.ID+"/permissions?capability=manage",
				"", "", f.tokens["member-user"], 200)
			allowed, reason := permissionReason(t, permission)
			if allowed || reason != "owner_unavailable" {
				t.Fatalf("member permission = allowed %v reason %q, want false / owner_unavailable (%s)",
					allowed, reason, permission.Body.String())
			}
			availability := f.request(t, http.MethodGet, "/projects/"+project.ID+"/owner-availability",
				"", "", f.tokens["member-user"], 200)
			available, paths := ownerAvailabilityAnswer(t, availability)
			if available || len(paths) == 0 {
				t.Fatalf("owner availability = available %v paths %v, want unavailable with the ways back (%s)",
					available, paths, availability.Body.String())
			}
		})
	}
}

// TestOwnerUnavailableTransferIsAttributable is the write side attribution at
// the HTTP boundary.
//
// The transfer is an Owner-only write like the five decisions, so it carries
// the same freeze. Before the gate was added to it, the refusal was the
// ordinary "not found" -- the same answer a missing project gives -- and the
// caller could not act on it.
//
// The shape asserted here is the whole contract: the status code and the
// error code stay what this refusal has always answered with, and only the
// reason rides along.
func TestOwnerUnavailableTransferIsAttributable(t *testing.T) {
	f := newOwnerFreezeFixture(t)
	project := f.activeProject(t)
	chapters := f.chapters(t, project.ID)
	if len(chapters) == 0 {
		t.Fatalf("activated project has no chapter: %+v", project)
	}
	f.addMember(t, project.ID, chapters[0].ID)

	f.lapseOwner(t, project.ID)

	refused := f.request(t, http.MethodPost, "/projects/"+project.ID+"/owner-transfer",
		`{"expected_project_version":`+strconv.FormatInt(project.ProjectVersion, 10)+`,"to_user_id":"member-user"}`,
		"pf9-transfer-owner", f.tokens["owner-user"], 404)
	if reason := errorReason(t, refused); reason != "owner_unavailable" {
		t.Fatalf("transfer refusal reason = %q, want owner_unavailable (%s)", reason, refused.Body.String())
	}
	if !strings.Contains(refused.Body.String(), `"code":"not_found"`) {
		t.Fatalf("the transfer refusal lost its error code: %s", refused.Body.String())
	}
}

// TestOwnerUnavailableFormalExportIsAttributableWhenTheTenantRoleIsDowngraded
// is the other way the Owner's tenant identity lapses: the membership stays,
// the contributor rights go away.
//
// The write is refused by the tenant ceiling long before the project gate is
// reached -- that refusal is the transport's own and it does not change here.
// The read side is where the freeze is named: the Owner may still read the
// project, and it reads its own export permission and is told the reason.
func TestOwnerUnavailableFormalExportIsAttributableWhenTheTenantRoleIsDowngraded(t *testing.T) {
	f := newOwnerFreezeExportFixture(t)
	project := f.activeProject(t)

	lapseOwnerTenantRole(t, f)

	permission := f.request(t, http.MethodGet, "/projects/"+project.ID+"/permissions?capability=ops-control",
		"", "", f.tokens["owner-user"], 200)
	allowed, reason := permissionReason(t, permission)
	if allowed || reason != "owner_unavailable" {
		t.Fatalf("downgraded export permission = allowed %v reason %q, want false / owner_unavailable (%s)",
			allowed, reason, permission.Body.String())
	}
}
