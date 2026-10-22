package router

// The activation and the template upgrade, judged at the HTTP boundary.
//
// The freeze is a domain fact, but the guarantee the product makes is a
// contract one: the Owner-only entries answer with a reason, and the
// permission read outlet answers with the same reason. So the assertions
// live on the HTTP seam -- the production composition, the production
// authentication, the production membership -- and never on the judgement
// inside it.
//
// The fixture and its helpers live in routes_lingdoc_owner_test.go; this file
// only owns the assertions of this ticket.

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
)

// newOwnerFreezeFixtureWithAuditShape builds the shared fixture and brings
// its audit table to the current production shape.
//
// The shared fixture applies the migration chain by hand, and the audit table
// gained two columns in #327 (target_version / project_revision). A fixture
// whose chain stops before that migration cannot hold the audit rows the
// write path now produces, and the write fails with an opaque 500 instead of
// a schema error. Applying the production migration here keeps this file's
// assertions on the current schema without touching the shared fixture.
func newOwnerFreezeFixtureWithAuditShape(t *testing.T) *ownerFreezeFixture {
	t.Helper()
	fixture := newOwnerFreezeFixture(t)
	fixture.ensureAuditEventShape(t)
	return fixture
}

// ensureAuditEventShape applies the audit version-fields migration when the
// table does not carry its columns yet.
func (f *ownerFreezeFixture) ensureAuditEventShape(t *testing.T) {
	t.Helper()
	var columns []struct {
		Name string
	}
	if err := f.db.Raw("PRAGMA table_info(lingdoc_project_audits)").Scan(&columns).Error; err != nil {
		t.Fatalf("read the audit table columns: %v", err)
	}
	for _, column := range columns {
		if column.Name == "target_version" {
			return
		}
	}
	migration, err := os.ReadFile(filepath.Join("..", "..", "migrations", "sqlite",
		"000052_lingdoc_audit_version_fields.up.sql"))
	if err != nil {
		t.Fatal(err)
	}
	pool, err := f.db.DB()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.ExecContext(context.Background(), string(migration)); err != nil {
		t.Fatalf("apply the audit version fields migration: %v", err)
	}
}

// draftProjectForActivation creates a project and leaves it in the draft
// period, so the activation gate is the one under test.
//
// The draft period and the active period are two different gates: an
// activation may only run on a draft project, a template upgrade only on an
// active one. Keeping them apart here is what lets the assertions say which
// period they are talking about.
func (f *ownerFreezeFixture) draftProjectForActivation(t *testing.T) workspacecore.Project {
	t.Helper()
	created := f.request(t, http.MethodPost, "/projects",
		`{"name":"owner freeze synthetic draft","template_id":"template-demo"}`,
		"owner-freeze-create-draft", f.tokens["owner-user"], 201)
	var envelope struct {
		Data workspacecore.Project `json:"data"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	path := "/projects/" + envelope.Data.ID
	f.request(t, http.MethodPut, path+"/spec",
		`{"expected_spec_revision":0,"fields":{"research_subject":"合成研究对象","research_goal":"合成研究目标"}}`,
		"owner-freeze-spec-draft", f.tokens["owner-user"], 200)
	read := f.request(t, http.MethodGet, path, "", "", f.tokens["owner-user"], 200)
	if err := json.Unmarshal(read.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Data.Status != "draft" {
		t.Fatalf("fixture project is not a draft: %+v", envelope.Data)
	}
	return envelope.Data
}

// addDraftPeriodMember records the member as a collaborator without a chapter
// scope. A draft project has no chapters yet, so the member's ordinary
// collaboration in that period is reading.
func (f *ownerFreezeFixture) addDraftPeriodMember(t *testing.T, projectID string) {
	t.Helper()
	if err := f.db.Exec("INSERT INTO lingdoc_members (project_id, user_id, role) VALUES (?, ?, ?)",
		projectID, "member-user", "collaborator").Error; err != nil {
		t.Fatal(err)
	}
	if err := f.db.Exec(`INSERT INTO lingdoc_member_permissions
		(project_id, user_id, governance_role, function_roles_json, function_scopes_json, status)
		VALUES (?, ?, ?, ?, ?, ?)`,
		projectID, "member-user", "member", `["author","reviewer"]`, `{}`, "active").Error; err != nil {
		t.Fatal(err)
	}
}

// readProjectByActor reads the project as somebody else than the Owner.
func (f *ownerFreezeFixture) readProjectByActor(t *testing.T, projectID, identity string) workspacecore.Project {
	t.Helper()
	read := f.request(t, http.MethodGet, "/projects/"+projectID, "", "", f.tokens[identity], 200)
	var envelope struct {
		Data workspacecore.Project `json:"data"`
	}
	if err := json.Unmarshal(read.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	return envelope.Data
}

// proposeTemplateUpgrade proposes the template upgrade the Owner still has to
// decide on. A template upgrade is a controlled change set, not a direct copy
// edit: the Owner decides it by applying that change set.
func (f *ownerFreezeFixture) proposeTemplateUpgrade(t *testing.T, project workspacecore.Project) workspacecore.ChangeSet {
	t.Helper()
	read := f.request(t, http.MethodGet,
		"/projects/"+project.ID+"/template-copies/"+strconv.FormatInt(project.TemplateCopyVersion, 10),
		"", "", f.tokens["owner-user"], 200)
	var envelope struct {
		Data struct {
			Definition struct {
				Fields         []map[string]any `json:"fields"`
				Sections       []map[string]any `json:"sections"`
				Terms          []map[string]any `json:"terms"`
				RequiredFields []string         `json:"required_fields"`
				Rules          []map[string]any `json:"rules"`
			} `json:"definition"`
		} `json:"data"`
	}
	if err := json.Unmarshal(read.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	definition := envelope.Data.Definition
	if len(definition.Fields) == 0 {
		t.Fatalf("template copy carries no field to upgrade: %s", read.Body.String())
	}
	definition.Fields[0]["label"] = "合成升级后的字段名"
	// The proposal carries every part of the definition, and an absent part is
	// an empty list rather than a missing one: a definition without terms is
	// still a definition.
	upgrade := map[string]any{
		"expected_project_version":       project.ProjectVersion,
		"expected_template_copy_version": project.TemplateCopyVersion,
		"fields":                         definition.Fields,
		"required_fields":                definition.RequiredFields,
	}
	for name, part := range map[string][]map[string]any{
		"sections": definition.Sections,
		"terms":    definition.Terms,
		"rules":    definition.Rules,
	} {
		if part == nil {
			part = []map[string]any{}
		}
		upgrade[name] = part
	}
	body, err := json.Marshal(map[string]any{
		"expected_context_revision": project.CurrentContextRevision,
		"reason":                    "合成模板升级评估",
		"template_upgrade":          upgrade,
	})
	if err != nil {
		t.Fatal(err)
	}
	created := f.request(t, http.MethodPost, "/projects/"+project.ID+"/change-sets", string(body),
		"owner-freeze-upgrade-create", f.tokens["owner-user"], 201)
	var change struct {
		Data workspacecore.ChangeSet `json:"data"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &change); err != nil {
		t.Fatal(err)
	}
	if change.Data.TemplateUpgrade == nil {
		t.Fatalf("created change set is not a template upgrade: %+v", change.Data)
	}
	return change.Data
}

// TestActivationFreezesWhileTheOwnerIsUnavailable is the draft-period half
// of the freeze: the activation is the Owner's decision, so it stops while
// the Owner's tenant identity has lapsed, and the refusal says why.
func TestActivationFreezesWhileTheOwnerIsUnavailable(t *testing.T) {
	f := newOwnerFreezeFixtureWithAuditShape(t)
	project := f.draftProjectForActivation(t)
	f.addAdmin(t, project.ID)

	f.lapseOwner(t, project.ID)

	activation := f.request(t, http.MethodPost, "/projects/"+project.ID+"/activate",
		`{"expected_spec_revision":1}`, "owner-freeze-activate-lapsed", f.tokens["owner-user"], 404)
	if reason := errorReason(t, activation); reason != "owner_unavailable" {
		t.Fatalf("activation refusal reason = %q, want owner_unavailable (%s)", reason, activation.Body.String())
	}

	// The project did not go live: the freeze stops the decision instead of
	// leaving a half-activated project behind.
	latest := f.readProjectByActor(t, project.ID, "admin-user")
	if latest.Status != "draft" {
		t.Fatalf("frozen activation left the project status = %q, want draft", latest.Status)
	}
}

// TestActivationRefusalIsNotTheOwnersOwnRefusal is the distinction the
// activation refusal has to carry: the freeze and the caller's own lack of
// authority are two different answers.
func TestActivationRefusalIsNotTheOwnersOwnRefusal(t *testing.T) {
	f := newOwnerFreezeFixtureWithAuditShape(t)
	project := f.draftProjectForActivation(t)
	f.addAdmin(t, project.ID)
	f.lapseOwner(t, project.ID)

	// The Owner is refused because the Owner's identity lapsed.
	frozen := f.request(t, http.MethodPost, "/projects/"+project.ID+"/activate",
		`{"expected_spec_revision":1}`, "owner-freeze-owner-activate", f.tokens["owner-user"], 404)
	if reason := errorReason(t, frozen); reason != "owner_unavailable" {
		t.Fatalf("frozen Owner refusal reason = %q, want owner_unavailable (%s)", reason, frozen.Body.String())
	}

	// The admin is refused because the admin is not the Owner, and that
	// refusal names no freeze.
	notOwner := f.request(t, http.MethodPost, "/projects/"+project.ID+"/activate",
		`{"expected_spec_revision":1}`, "owner-freeze-admin-activate", f.tokens["admin-user"], 404)
	if reason := errorReason(t, notOwner); reason != "" {
		t.Fatalf("non-Owner refusal reason = %q, want none (%s)", reason, notOwner.Body.String())
	}
	if !strings.Contains(notOwner.Body.String(), `"code":"not_found"`) {
		t.Fatalf("non-Owner refusal lost its error code: %s", notOwner.Body.String())
	}
}

// TestActivationUnfreezesWhenTheIdentityComesBack is the derived part: the
// activation is not a state anybody repairs, it is a fact that changes by
// itself.
func TestActivationUnfreezesWhenTheIdentityComesBack(t *testing.T) {
	f := newOwnerFreezeFixtureWithAuditShape(t)
	project := f.draftProjectForActivation(t)

	f.lapseOwner(t, project.ID)
	f.request(t, http.MethodPost, "/projects/"+project.ID+"/activate",
		`{"expected_spec_revision":1}`, "owner-unfreeze-blocked-activate", f.tokens["owner-user"], 404)

	// The Owner's project membership comes back. There is no repair action to
	// run: the next activation simply runs.
	f.restoreOwner(t, project.ID)
	f.request(t, http.MethodPost, "/projects/"+project.ID+"/activate",
		`{"expected_spec_revision":1}`, "owner-unfreeze-restored-activate", f.tokens["owner-user"], 200)
}

// TestActivationAndTemplateUpgradeShareTheFreezeReason is the consistency
// the two entries owe the caller: the activation is a draft-period decision
// and the template upgrade is an active-period one, and both refusals name
// the same freeze.
func TestActivationAndTemplateUpgradeShareTheFreezeReason(t *testing.T) {
	f := newOwnerFreezeFixtureWithAuditShape(t)

	draft := f.draftProjectForActivation(t)
	f.lapseOwner(t, draft.ID)
	activation := f.request(t, http.MethodPost, "/projects/"+draft.ID+"/activate",
		`{"expected_spec_revision":1}`, "owner-freeze-activation-reason", f.tokens["owner-user"], 404)
	activationReason := errorReason(t, activation)
	if activationReason != "owner_unavailable" {
		t.Fatalf("activation refusal reason = %q, want owner_unavailable (%s)", activationReason, activation.Body.String())
	}

	active := f.activeProject(t)
	upgrade := f.proposeTemplateUpgrade(t, active)
	f.lapseOwner(t, active.ID)
	apply := f.request(t, http.MethodPost, "/projects/"+active.ID+"/change-sets/"+upgrade.ID+"/apply",
		"{}", "owner-freeze-upgrade-reason", f.tokens["owner-user"], 404)
	upgradeReason := errorReason(t, apply)
	if upgradeReason != "owner_unavailable" {
		t.Fatalf("template upgrade refusal reason = %q, want owner_unavailable (%s)", upgradeReason, apply.Body.String())
	}
	if upgradeReason != activationReason {
		t.Fatalf("template upgrade reason = %q, activation reason = %q, want the same freeze reason", upgradeReason, activationReason)
	}
}

// TestPermissionOutletAgreesWithTheActivationRefusal is the read side of
// the same fact: the permission read outlet reaches the same conclusion the
// write refusals do, so the interface can say "the Owner is unavailable"
// instead of "you lack permission".
//
// The audience of the read outlet is the member working on the project: the
// lapsed Owner cannot even read the project any more, so the outlet is not
// where that side of the story is told.
func TestPermissionOutletAgreesWithTheActivationRefusal(t *testing.T) {
	f := newOwnerFreezeFixtureWithAuditShape(t)
	project := f.draftProjectForActivation(t)
	f.addDraftPeriodMember(t, project.ID)
	f.lapseOwner(t, project.ID)

	permission := f.request(t, http.MethodGet, "/projects/"+project.ID+"/permissions?capability=manage",
		"", "", f.tokens["member-user"], 200)
	allowed, reason := permissionReason(t, permission)
	if allowed || reason != "owner_unavailable" {
		t.Fatalf("member permission read outlet = allowed %v reason %q, want false / owner_unavailable (%s)",
			allowed, reason, permission.Body.String())
	}
}

// TestDraftCollaborationSurvivesTheOwnerFreeze is the positive evidence for
// the draft period: the freeze stops the Owner's decisions and nothing else.
// The research conditions stay editable by the project admin, the draft
// candidates stay readable, and the Owner's own candidate save is refused by
// the ordinary membership rule rather than by the freeze.
func TestDraftCollaborationSurvivesTheOwnerFreeze(t *testing.T) {
	f := newOwnerFreezeFixtureWithAuditShape(t)
	project := f.draftProjectForActivation(t)
	f.addAdmin(t, project.ID)
	f.addDraftPeriodMember(t, project.ID)

	// The baseline: what the project allows before the freeze.
	before := f.readProjectByActor(t, project.ID, "admin-user")
	if before.Status != "draft" {
		t.Fatalf("fixture project is not a draft: %+v", before)
	}

	f.lapseOwner(t, project.ID)

	// The admin still edits the research conditions.
	f.request(t, http.MethodPut, "/projects/"+project.ID+"/spec",
		`{"expected_spec_revision":1,"fields":{"research_subject":"合成研究对象","research_goal":"冻结期间的研究目标"}}`,
		"owner-freeze-draft-spec", f.tokens["admin-user"], 200)

	// The member still reads the project and the draft candidate pool.
	f.request(t, http.MethodGet, "/projects/"+project.ID, "", "", f.tokens["member-user"], 200)
	f.request(t, http.MethodGet, "/projects/"+project.ID+"/draft-candidates", "", "", f.tokens["member-user"], 200)

	// The Owner's own candidate save is not a frozen action: it is refused by
	// the ordinary membership rule, and its refusal names no freeze.
	candidate := f.request(t, http.MethodPost, "/projects/"+project.ID+"/draft-candidates",
		`{"kind":"manual_note","title":"合成候选","content":"合成候选内容"}`,
		"owner-freeze-draft-candidate", f.tokens["owner-user"], 404)
	if reason := errorReason(t, candidate); reason != "" {
		t.Fatalf("candidate save refusal reason = %q, want none (%s)", reason, candidate.Body.String())
	}
}

// TestActiveCollaborationSurvivesTheOwnerFreeze is the same positive
// evidence for the active period, where the template upgrade lives: the
// member still comments and still reviews while the Owner-only decisions are
// frozen.
func TestActiveCollaborationSurvivesTheOwnerFreeze(t *testing.T) {
	f := newOwnerFreezeFixtureWithAuditShape(t)
	project := f.activeProject(t)
	chapters := f.chapters(t, project.ID)
	if len(chapters) == 0 {
		t.Fatalf("activated project has no chapter: %+v", project)
	}
	chapter := chapters[0]
	f.addMember(t, project.ID, chapter.ID)
	versionID := f.commitChapter(t, project, chapter.ID)
	current := f.readProject(t, project.ID)

	f.lapseOwner(t, project.ID)

	comment := `{"expected_project_version":` + strconv.FormatInt(current.ProjectVersion, 10) +
		`,"expected_context_revision":` + strconv.FormatInt(current.CurrentContextRevision, 10) +
		`,"chapter_version_id":"` + versionID + `","body":"冻结期间的成员评论"}`
	f.request(t, http.MethodPost, "/projects/"+project.ID+"/chapters/"+chapter.ID+"/comments",
		comment, "owner-freeze-active-comment", f.tokens["member-user"], 201)

	review := `{"expected_project_version":` + strconv.FormatInt(current.ProjectVersion, 10) +
		`,"expected_context_revision":` + strconv.FormatInt(current.CurrentContextRevision, 10) +
		`,"chapter_version_id":"` + versionID + `","action":"submit","reason":"冻结期间的成员复核"}`
	f.request(t, http.MethodPost, "/projects/"+project.ID+"/chapters/"+chapter.ID+"/review",
		review, "owner-freeze-active-review", f.tokens["member-user"], 200)
}
