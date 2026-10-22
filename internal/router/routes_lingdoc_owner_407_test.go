package router

// The project level read outlet that tells the project page and the members
// page what is going on with the Owner.
//
// The outlet is a domain fact, but the guarantee the product makes is a
// contract one: a member can read the Owner's availability, the two read
// outlets agree about the same project, and the frozen state is derived
// rather than stored. So the assertions live on the HTTP seam -- the
// production composition, the production authentication, the production
// membership -- and never on the judgement inside it.
//
// The fixture and its helpers come from the shared owner freeze test file;
// this file holds only the cases that are about the project level outlet.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"testing"

	"github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
)

// ownerAvailabilityFact is the project level read outlet's payload, read as
// the contract reads it: the wire shape, not a Go type.
type ownerAvailabilityFact struct {
	OwnerUserID   string   `json:"owner_user_id"`
	Available     bool     `json:"available"`
	ReasonCode    string   `json:"reason_code"`
	RecoveryPaths []string `json:"recovery_paths"`
}

// ownerAvailability reads the project level read outlet that tells the
// project page and the members page what is going on with the Owner.
func ownerAvailability(t *testing.T, response *httptest.ResponseRecorder) ownerAvailabilityFact {
	t.Helper()
	var envelope struct {
		Data ownerAvailabilityFact `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode owner availability envelope: %v (%s)", err, response.Body.String())
	}
	return envelope.Data
}

// readProjectAs reads the project as one member, whoever that is: the Owner
// may not be able to read the project while the Owner is lapsed.
func (f *ownerFreezeFixture) readProjectAs(t *testing.T, projectID, identity string) workspacecore.Project {
	t.Helper()
	read := f.request(t, http.MethodGet, "/projects/"+projectID, "", "", identity, 200)
	var envelope struct {
		Data workspacecore.Project `json:"data"`
	}
	if err := json.Unmarshal(read.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	return envelope.Data
}

// projectKeys lists the field names of the project payload, so that a derived
// state that landed on the project would show up as a new field.
func projectKeys(project workspacecore.Project) []string {
	raw, err := json.Marshal(project)
	if err != nil {
		return []string{"unmarshalable"}
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return []string{"unmarshalable"}
	}
	keys := make([]string, 0, len(fields))
	for key := range fields {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// TestOwnerAvailabilityOutletReportsTheFrozenOwner is the project level read
// outlet: the project page and the members page have to be able to say what
// is going on with the Owner and how the project gets back to work.
func TestOwnerAvailabilityOutletReportsTheFrozenOwner(t *testing.T) {
	f := newOwnerFreezeFixture(t)
	project := f.activeProject(t)
	chapters := f.chapters(t, project.ID)
	if len(chapters) == 0 {
		t.Fatalf("activated project has no chapter: %+v", project)
	}
	f.addMember(t, project.ID, chapters[0].ID)

	f.lapseOwner(t, project.ID)

	read := f.request(t, http.MethodGet, "/projects/"+project.ID+"/owner-availability",
		"", "", f.tokens["member-user"], 200)
	availability := ownerAvailability(t, read)
	if availability.OwnerUserID != "owner-user" {
		t.Fatalf("owner = %q, want owner-user (%s)", availability.OwnerUserID, read.Body.String())
	}
	if availability.Available {
		t.Fatalf("a lapsed Owner is reported as available: %s", read.Body.String())
	}
	if availability.ReasonCode != "owner_unavailable" {
		t.Fatalf("reason = %q, want owner_unavailable (%s)", availability.ReasonCode, read.Body.String())
	}
	want := []string{"restore_tenant_identity", "tenant_owner_nomination"}
	if !reflect.DeepEqual(availability.RecoveryPaths, want) {
		t.Fatalf("recovery paths = %v, want %v (%s)", availability.RecoveryPaths, want, read.Body.String())
	}

	// The two read outlets read one judgement: the permission outlet and the
	// project level outlet say the same thing about the same project.
	permission := f.request(t, http.MethodGet, "/projects/"+project.ID+"/permissions?capability=manage",
		"", "", f.tokens["member-user"], 200)
	allowed, reason := permissionReason(t, permission)
	if allowed || reason != "owner_unavailable" {
		t.Fatalf("permission read outlet = allowed %v reason %q, want false / owner_unavailable (%s)",
			allowed, reason, permission.Body.String())
	}
}

// TestOwnerAvailabilityOutletReportsAHealthyOwner is the other half: a
// project whose Owner may still act is not a project in trouble.
func TestOwnerAvailabilityOutletReportsAHealthyOwner(t *testing.T) {
	f := newOwnerFreezeFixture(t)
	project := f.activeProject(t)

	read := f.request(t, http.MethodGet, "/projects/"+project.ID+"/owner-availability",
		"", "", f.tokens["owner-user"], 200)
	availability := ownerAvailability(t, read)
	if availability.OwnerUserID != "owner-user" {
		t.Fatalf("owner = %q, want owner-user (%s)", availability.OwnerUserID, read.Body.String())
	}
	if !availability.Available {
		t.Fatalf("a healthy Owner is reported as unavailable: %s", read.Body.String())
	}
	if availability.ReasonCode != "allowed" {
		t.Fatalf("reason = %q, want allowed (%s)", availability.ReasonCode, read.Body.String())
	}
	if len(availability.RecoveryPaths) != 0 {
		t.Fatalf("a healthy project offers recovery paths: %v (%s)", availability.RecoveryPaths, read.Body.String())
	}
}

// TestOwnerAvailabilityIsDerivedRatherThanStored is the promise that the
// frozen state is a fact read at the moment of the question: nothing on the
// project grows a field, and nobody has to run a repair action to clear it.
func TestOwnerAvailabilityIsDerivedRatherThanStored(t *testing.T) {
	f := newOwnerFreezeFixture(t)
	project := f.activeProject(t)
	chapters := f.chapters(t, project.ID)
	if len(chapters) == 0 {
		t.Fatalf("activated project has no chapter: %+v", project)
	}
	f.addMember(t, project.ID, chapters[0].ID)

	before := f.readProjectAs(t, project.ID, f.tokens["member-user"])
	f.lapseOwner(t, project.ID)
	frozen := ownerAvailability(t, f.request(t, http.MethodGet, "/projects/"+project.ID+"/owner-availability",
		"", "", f.tokens["member-user"], 200))
	if frozen.Available {
		t.Fatalf("a lapsed Owner is reported as available")
	}

	// The project itself carries no frozen state: its payload does not grow a
	// field and it does not name the reason.
	after := f.readProjectAs(t, project.ID, f.tokens["member-user"])
	if !reflect.DeepEqual(projectKeys(before), projectKeys(after)) {
		t.Fatalf("the project payload changed shape when the Owner lapsed: %v -> %v",
			projectKeys(before), projectKeys(after))
	}

	// The Owner's identity comes back. There is no repair action to run: the
	// next read answers "available" again.
	f.restoreOwner(t, project.ID)
	restored := ownerAvailability(t, f.request(t, http.MethodGet, "/projects/"+project.ID+"/owner-availability",
		"", "", f.tokens["member-user"], 200))
	if !restored.Available || restored.ReasonCode != "allowed" {
		t.Fatalf("restored Owner = available %v reason %q, want true / allowed", restored.Available, restored.ReasonCode)
	}
}
