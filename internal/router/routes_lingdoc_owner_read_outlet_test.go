package router

// The permission read outlet, corrected.
//
// The outlet answers "may this actor do this, and why not". It has two
// answers for a refusal, and they mean different things: the actor's own
// lack of authority, and the freeze that comes from the Owner's tenant
// identity having lapsed. Only the second one may be reported as
// "owner_unavailable" -- otherwise a plain member who is refused "write"
// because the write is outside their scope is told the entry is frozen, and
// the existing "capability_or_scope_denied" meaning is swallowed.
//
// The freeze is a domain fact, but the guarantee the product makes is a
// contract one, so the assertions live on the HTTP seam: the production
// composition, the production authentication, the production membership.
// The fixture and its helpers come from the shared owner freeze test file and
// are reused as they are.

import (
	"net/http"
	"testing"

	"github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
)

// permissionRead asks the permission read outlet for one capability and
// returns what it answered.
func permissionRead(t *testing.T, f *ownerFreezeFixture, projectID, capability, identity string) (bool, string) {
	t.Helper()
	response := f.request(t, http.MethodGet,
		"/projects/"+projectID+"/permissions?capability="+capability,
		"", "", identity, 200)
	return permissionReason(t, response)
}

// TestPermissionReadOutletKeepsTheExistingReasonForOtherCapabilities is the
// correction itself: a refusal that has nothing to do with the freeze keeps
// the reason it has always carried.
//
// The member of a frozen project is refused "write" and "review" because
// those capabilities are outside their scope -- not because the project is
// frozen. The outlet says so, and the interface can tell the two apart.
//
// "read" is not the example here, and it cannot be: the outlet only answers
// for an actor that may read the project, and "read" is allowed for every
// actor that reaches it. It can therefore never carry a refusal reason here.
func TestPermissionReadOutletKeepsTheExistingReasonForOtherCapabilities(t *testing.T) {
	f := newOwnerFreezeFixture(t)
	project := f.activeProject(t)
	chapters := f.chapters(t, project.ID)
	if len(chapters) == 0 {
		t.Fatalf("activated project has no chapter: %+v", project)
	}
	f.addMember(t, project.ID, chapters[0].ID)

	// The baseline: while the Owner may still act, the same refusal carries
	// the same reason.
	healthyAllowed, healthyReason := permissionRead(t, f, project.ID, "write", f.tokens["member-user"])
	if healthyAllowed || healthyReason != "capability_or_scope_denied" {
		t.Fatalf("healthy project's write read = allowed %v reason %q, want false / capability_or_scope_denied",
			healthyAllowed, healthyReason)
	}

	f.lapseOwner(t, project.ID)

	for _, capability := range []string{"write", "review"} {
		allowed, reason := permissionRead(t, f, project.ID, capability, f.tokens["member-user"])
		if allowed || reason != "capability_or_scope_denied" {
			t.Fatalf("frozen project's %s read = allowed %v reason %q, want false / capability_or_scope_denied",
				capability, allowed, reason)
		}
	}
}

// TestPermissionReadOutletNamesTheFreezeForOwnerOnlyCapabilities is the
// button-level judgement the interface relies on: the Owner-only entries are
// frozen, and the outlet says that is why.
func TestPermissionReadOutletNamesTheFreezeForOwnerOnlyCapabilities(t *testing.T) {
	f := newOwnerFreezeFixture(t)
	project := f.activeProject(t)
	chapters := f.chapters(t, project.ID)
	if len(chapters) == 0 {
		t.Fatalf("activated project has no chapter: %+v", project)
	}
	f.addMember(t, project.ID, chapters[0].ID)

	f.lapseOwner(t, project.ID)

	allowed, reason := permissionRead(t, f, project.ID, "manage", f.tokens["member-user"])
	if allowed || reason != "owner_unavailable" {
		t.Fatalf("frozen project's manage read = allowed %v reason %q, want false / owner_unavailable",
			allowed, reason)
	}

	// The capability may carry a "base:target" suffix. The base is the
	// capability the authorization asks for, so the base is what decides
	// whether the entry is an Owner-only one.
	allowed, reason = permissionRead(t, f, project.ID, "manage:"+chapters[0].ID, f.tokens["member-user"])
	if allowed || reason != "owner_unavailable" {
		t.Fatalf("frozen project's manage read with a target = allowed %v reason %q, want false / owner_unavailable",
			allowed, reason)
	}
}

// TestPermissionReadOutletNamesTheFreezeForTheOwnerItself is the Owner's own
// side: the Owner whose contributor rights are gone is refused the Owner-only
// entry, and the refusal names the freeze rather than hiding it behind
// "you may not".
//
// The tenant role is what lapses here, not the project membership: the Owner
// can still read the project, so the outlet answers for them.
func TestPermissionReadOutletNamesTheFreezeForTheOwnerItself(t *testing.T) {
	f := newOwnerFreezeFixture(t)
	project := f.activeProject(t)

	lapseOwnerTenantRole(t, f)

	allowed, reason := permissionRead(t, f, project.ID, "manage", f.tokens["owner-user"])
	if allowed || reason != "owner_unavailable" {
		t.Fatalf("frozen Owner's manage read = allowed %v reason %q, want false / owner_unavailable",
			allowed, reason)
	}
}

// TestPermissionReadOutletDoesNotReachTheLapsedOwner is the other side of the
// Owner's own story: the Owner whose *project membership* lapsed cannot read
// the project any more, so the outlet never answers for them at all.
//
// This is why the Owner's own refusal is told by the write side, and why the
// outlet's audience is the member working on the project.
func TestPermissionReadOutletDoesNotReachTheLapsedOwner(t *testing.T) {
	f := newOwnerFreezeFixture(t)
	project := f.activeProject(t)

	f.lapseOwner(t, project.ID)

	response := f.request(t, http.MethodGet,
		"/projects/"+project.ID+"/permissions?capability=manage",
		"", "", f.tokens["owner-user"], 404)
	if reason := errorReason(t, response); reason != "" {
		t.Fatalf("the lapsed Owner's refusal reason = %q, want none (%s)", reason, response.Body.String())
	}
}

// TestPermissionReadOutletNamesTheFreezeForEveryOwnerOnlyEntryCapability is
// the enumeration the registry promises.
//
// The outlet answers per capability, so every capability an Owner-only entry
// is read by has to answer with the freeze while the Owner is unavailable.
// The list comes from the registry rather than from a hand-written one: a
// capability registered as an Owner-only entry capability is covered here the
// moment it is registered, and an Owner-only entry that is *not* registered
// cannot be read as frozen at all -- which is what the registry's guard test
// is about.
//
// The member is the one asking. The freeze is a fact about the project, so
// the member working on it has to be able to read why the Owner-only entries
// are frozen.
func TestPermissionReadOutletNamesTheFreezeForEveryOwnerOnlyEntryCapability(t *testing.T) {
	f := newOwnerFreezeFixture(t)
	project := f.activeProject(t)
	chapters := f.chapters(t, project.ID)
	if len(chapters) == 0 {
		t.Fatalf("activated project has no chapter: %+v", project)
	}
	f.addMember(t, project.ID, chapters[0].ID)

	f.lapseOwner(t, project.ID)

	capabilities := workspacecore.OwnerOnlyEntryCapabilities()
	if len(capabilities) == 0 {
		t.Fatal("the registry names no Owner-only entry capability")
	}
	for _, capability := range capabilities {
		allowed, reason := permissionRead(t, f, project.ID, capability, f.tokens["member-user"])
		if allowed || reason != "owner_unavailable" {
			t.Fatalf("frozen project's %s read = allowed %v reason %q, want false / owner_unavailable",
				capability, allowed, reason)
		}
	}
}

// TestPermissionReadOutletKeepsTheHealthyMeaning is the other half: a
// project whose Owner may still act is not a project in trouble, and the
// outlet keeps saying so.
func TestPermissionReadOutletKeepsTheHealthyMeaning(t *testing.T) {
	f := newOwnerFreezeFixture(t)
	project := f.activeProject(t)
	chapters := f.chapters(t, project.ID)
	if len(chapters) == 0 {
		t.Fatalf("activated project has no chapter: %+v", project)
	}
	f.addMember(t, project.ID, chapters[0].ID)

	// The Owner may still act, so the Owner-only entry may run.
	allowed, reason := permissionRead(t, f, project.ID, "manage", f.tokens["owner-user"])
	if !allowed || reason != "allowed" {
		t.Fatalf("healthy project's manage read = allowed %v reason %q, want true / allowed",
			allowed, reason)
	}

	// The member is not the Owner, so the member's refusal carries no freeze.
	allowed, reason = permissionRead(t, f, project.ID, "manage", f.tokens["member-user"])
	if allowed || reason != "capability_or_scope_denied" {
		t.Fatalf("healthy project's manage read for the member = allowed %v reason %q, want false / capability_or_scope_denied",
			allowed, reason)
	}
}
