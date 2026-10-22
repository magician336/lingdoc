package router

// Archiving, judged at the HTTP boundary.
//
// Archiving is an Owner-only action on the project lifecycle, so the
// guarantees the product makes about it are contract guarantees: the Owner may
// archive, nobody else may, the decision carries the project version, a retry
// is one action, an archived project accepts no further change, and the end of
// a project is traceable in the audit trail.
//
// The assertions therefore live on the HTTP seam -- the production
// composition, the production authentication, the production membership --
// and never on the judgement inside it. The fixture is the one the
// owner_unavailable freeze already uses, so both live in the same world.
//
// The helpers here carry the archive prefix so that the sibling tickets of
// this work surface can hold their own helpers in their own files without
// either of them having to give way at merge time.

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
)

// archiveRequestBody is the archive request body: the project version the
// caller read, so that a stale decision cannot land on a project that has
// changed since.
func archiveRequestBody(projectVersion int64) string {
	return `{"expected_project_version":` + strconv.FormatInt(projectVersion, 10) + `}`
}

// archiveReadProject reads the project as the Owner.
func (f *ownerFreezeFixture) archiveReadProject(t *testing.T, projectID string) map[string]any {
	t.Helper()
	return f.archiveReadProjectAs(t, projectID, f.tokens["owner-user"])
}

// archiveReadProjectAs reads the project as one identity. A project whose
// Owner can no longer act is still readable by the people working on it, so
// the read side of an assertion does not depend on the Owner being available.
func (f *ownerFreezeFixture) archiveReadProjectAs(t *testing.T, projectID, identity string) map[string]any {
	t.Helper()
	read := f.request(t, http.MethodGet, "/projects/"+projectID, "", "", identity, 200)
	var envelope struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(read.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	return envelope.Data
}

// archiveAuditActions reads the project's audit trail and returns the actions
// it carries.
func (f *ownerFreezeFixture) archiveAuditActions(t *testing.T, projectID string) []string {
	t.Helper()
	read := f.request(t, http.MethodGet, "/projects/"+projectID+"/audit", "", "", f.tokens["owner-user"], 200)
	var envelope struct {
		Data []struct {
			Action string `json:"action"`
		} `json:"data"`
	}
	if err := json.Unmarshal(read.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	actions := make([]string, 0, len(envelope.Data))
	for _, item := range envelope.Data {
		actions = append(actions, item.Action)
	}
	return actions
}

// archiveDraftProject creates a project that never left the draft.
func (f *ownerFreezeFixture) archiveDraftProject(t *testing.T, key string) workspacecore.Project {
	t.Helper()
	created := f.request(t, http.MethodPost, "/projects",
		`{"name":"archive draft synthetic project","template_id":"template-demo"}`,
		key, f.tokens["owner-user"], 201)
	var envelope struct {
		Data workspacecore.Project `json:"data"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	return envelope.Data
}

// TestOwnerArchivesAnActiveProject is the action itself: the Owner ends the
// project, and the project says it is finished.
func TestOwnerArchivesAnActiveProject(t *testing.T) {
	f := newOwnerFreezeFixture(t)
	project := f.activeProject(t)

	archived := f.request(t, http.MethodPost, "/projects/"+project.ID+"/archive",
		archiveRequestBody(project.ProjectVersion), "archive-owner-1", f.tokens["owner-user"], 200)

	var envelope struct {
		Data struct {
			Status         string `json:"status"`
			ProjectVersion int64  `json:"project_version"`
		} `json:"data"`
		Meta struct {
			Replayed bool `json:"replayed"`
		} `json:"meta"`
	}
	if err := json.Unmarshal(archived.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Data.Status != "archived" {
		t.Fatalf("archived project status = %q, want archived (%s)", envelope.Data.Status, archived.Body.String())
	}
	if envelope.Data.ProjectVersion != project.ProjectVersion+1 {
		t.Fatalf("archived project version = %d, want %d", envelope.Data.ProjectVersion, project.ProjectVersion+1)
	}
	if envelope.Meta.Replayed {
		t.Fatalf("the first archive was reported as a replay: %s", archived.Body.String())
	}
	if got := f.archiveReadProject(t, project.ID)["status"]; got != "archived" {
		t.Fatalf("the archived project reads back as %v, want archived", got)
	}
}

// TestArchiveIsIdempotent is the retry guarantee: a repeated request is one
// action, so it cannot produce two archive events.
func TestArchiveIsIdempotent(t *testing.T) {
	f := newOwnerFreezeFixture(t)
	project := f.activeProject(t)
	path := "/projects/" + project.ID + "/archive"
	body := archiveRequestBody(project.ProjectVersion)

	f.request(t, http.MethodPost, path, body, "archive-owner-retry", f.tokens["owner-user"], 200)
	retry := f.request(t, http.MethodPost, path, body, "archive-owner-retry", f.tokens["owner-user"], 200)

	var envelope struct {
		Meta struct {
			Replayed bool `json:"replayed"`
		} `json:"meta"`
	}
	if err := json.Unmarshal(retry.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if !envelope.Meta.Replayed {
		t.Fatalf("the retried archive was not reported as a replay: %s", retry.Body.String())
	}
	actions := f.archiveAuditActions(t, project.ID)
	archives := 0
	for _, action := range actions {
		if action == "project.archive" {
			archives++
		}
	}
	if archives != 1 {
		t.Fatalf("archive audit events = %d, want 1 (%v)", archives, actions)
	}
	if got := f.archiveReadProject(t, project.ID)["project_version"]; got != float64(project.ProjectVersion+1) {
		t.Fatalf("archived project version = %v, want %d", got, project.ProjectVersion+1)
	}
}

// TestArchiveWritesAnAuditEvent is the traceability guarantee: the end of a
// project is recorded the way the other Owner-only project decisions are.
func TestArchiveWritesAnAuditEvent(t *testing.T) {
	f := newOwnerFreezeFixture(t)
	project := f.activeProject(t)

	f.request(t, http.MethodPost, "/projects/"+project.ID+"/archive",
		archiveRequestBody(project.ProjectVersion), "archive-owner-audit", f.tokens["owner-user"], 200)

	actions := f.archiveAuditActions(t, project.ID)
	found := false
	for _, action := range actions {
		if action == "project.archive" {
			found = true
		}
	}
	if !found {
		t.Fatalf("the archive wrote no project.archive audit event: %v", actions)
	}
}

// TestArchiveRefusesANonOwner keeps the action Owner-only, and keeps the
// refusal the way the other Owner-only actions refuse: no reason code, so the
// caller cannot mistake it for the freeze.
func TestArchiveRefusesANonOwner(t *testing.T) {
	f := newOwnerFreezeFixture(t)
	project := f.activeProject(t)
	f.addAdmin(t, project.ID)

	refused := f.request(t, http.MethodPost, "/projects/"+project.ID+"/archive",
		archiveRequestBody(project.ProjectVersion), "archive-admin-1", f.tokens["admin-user"], 404)
	if reason := errorReason(t, refused); reason != "" {
		t.Fatalf("non-Owner archive refusal reason = %q, want none (%s)", reason, refused.Body.String())
	}
	if !strings.Contains(refused.Body.String(), `"code":"not_found"`) {
		t.Fatalf("non-Owner archive refusal lost its error code: %s", refused.Body.String())
	}
	if got := f.archiveReadProject(t, project.ID)["status"]; got != "active" {
		t.Fatalf("the project was archived by a non-Owner: status = %v", got)
	}
}

// TestArchiveRefusesAStaleProjectVersion keeps a decision taken on an older
// project from landing on the changed one.
func TestArchiveRefusesAStaleProjectVersion(t *testing.T) {
	f := newOwnerFreezeFixture(t)
	project := f.activeProject(t)

	refused := f.request(t, http.MethodPost, "/projects/"+project.ID+"/archive",
		archiveRequestBody(project.ProjectVersion-1), "archive-owner-stale", f.tokens["owner-user"], 409)
	if !strings.Contains(refused.Body.String(), `"code":"version_conflict"`) {
		t.Fatalf("stale archive refusal is not a version conflict: %s", refused.Body.String())
	}
	if got := f.archiveReadProject(t, project.ID)["status"]; got != "active" {
		t.Fatalf("a stale archive decision landed anyway: status = %v", got)
	}
}

// TestDraftProjectCannotBeArchived keeps the two terminal markers apart: a
// draft is discarded, an active project is archived.
func TestDraftProjectCannotBeArchived(t *testing.T) {
	f := newOwnerFreezeFixture(t)
	project := f.archiveDraftProject(t, "archive-draft-create")

	refused := f.request(t, http.MethodPost, "/projects/"+project.ID+"/archive",
		archiveRequestBody(project.ProjectVersion), "archive-draft-1", f.tokens["owner-user"], 422)
	if !strings.Contains(refused.Body.String(), `"code":"invalid_state"`) {
		t.Fatalf("draft archive refusal is not an invalid state: %s", refused.Body.String())
	}
	if got := f.archiveReadProject(t, project.ID)["status"]; got != "draft" {
		t.Fatalf("the draft project was archived: status = %v", got)
	}
}

// TestArchivedProjectAcceptsNoFurtherChange is the terminal guarantee: an
// archived project is finished, not paused.
func TestArchivedProjectAcceptsNoFurtherChange(t *testing.T) {
	f := newOwnerFreezeFixture(t)
	project := f.activeProject(t)

	f.request(t, http.MethodPost, "/projects/"+project.ID+"/archive",
		archiveRequestBody(project.ProjectVersion), "archive-owner-terminal", f.tokens["owner-user"], 200)

	changed := f.request(t, http.MethodPut, "/projects/"+project.ID+"/spec",
		`{"expected_spec_revision":1,"fields":{"research_subject":"归档之后不该写进去"}}`,
		"archive-terminal-spec", f.tokens["owner-user"], 422)
	if !strings.Contains(changed.Body.String(), `"code":"invalid_state"`) {
		t.Fatalf("the archived project accepted a change: %s", changed.Body.String())
	}
}

// TestOwnerUnavailableFreezesArchive is the freeze this ticket is about: the
// Owner whose tenant identity lapsed may not archive, and the refusal names
// the freeze instead of hiding it behind "not found".
func TestOwnerUnavailableFreezesArchive(t *testing.T) {
	f := newOwnerFreezeFixture(t)
	project := f.activeProject(t)
	chapters := f.chapters(t, project.ID)
	if len(chapters) == 0 {
		t.Fatalf("activated project has no chapter: %+v", project)
	}
	f.addMember(t, project.ID, chapters[0].ID)
	f.lapseOwner(t, project.ID)

	frozen := f.request(t, http.MethodPost, "/projects/"+project.ID+"/archive",
		archiveRequestBody(project.ProjectVersion), "archive-owner-frozen", f.tokens["owner-user"], 404)
	if reason := errorReason(t, frozen); reason != "owner_unavailable" {
		t.Fatalf("frozen archive refusal reason = %q, want owner_unavailable (%s)", reason, frozen.Body.String())
	}
	// The Owner can no longer read the project, so the read side of this
	// assertion is made by somebody who still can.
	if got := f.archiveReadProjectAs(t, project.ID, f.tokens["member-user"])["status"]; got != "active" {
		t.Fatalf("the archive ran while the Owner was unavailable: status = %v", got)
	}
}
