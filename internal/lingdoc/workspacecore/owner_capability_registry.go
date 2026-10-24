package workspacecore

// The registry of the Owner-only entries.
//
// An Owner-only entry is a write only the project's Owner may do. The
// interface learns that such an entry is frozen from the permission read
// outlet, and the outlet answers per *capability*, not per entry. So the
// relation between an entry and the capability the interface reads it by is
// the thing that has to be written down -- and it has to have exactly one
// home. A second copy of the enumeration is a second answer to the same
// question, and the two answers drift apart: an entry that is not in this
// registry cannot be reported as frozen, so the interface shows "you may
// not" where the truth is "the Owner is gone".
//
// Two capabilities are involved, and they are not the same thing:
//
//   - the capability the write path authorizes with. The activation asks for
//     "write", the archive asks for "manage", the formal export asks for
//     "ops-control";
//   - the capability the interface reads the freeze by. Every entry is read
//     by "manage", because one read answers for all of them; the formal
//     export is read by its own "ops-control" as well.
//
// Only the second one decides whether a refusal may be reported as a freeze.
// The first one must not: "write" is a capability every member may hold, so
// treating it as Owner-only would swallow the existing
// "capability_or_scope_denied" meaning -- a member refused a write that is
// outside their scope would be told the project is frozen.
//
// The registry is the single source. OwnerOnlyCapability is derived from it,
// and the guard in owner_capability_registry_test.go checks that the code's
// Owner-only entries and this table cannot drift apart.

import "slices"

// OwnerOnlyEntry is one write entry only the project's Owner may use.
type OwnerOnlyEntry struct {
	// Name is the entry point that enforces the Owner-only decision. It is
	// the Go method name, so the guard can find it in the sources and a
	// reader can find it in the code.
	Name string
	// AuthorizationCapability is the capability the write path authorizes
	// with. It is recorded, not consulted: the read outlet must not treat
	// it as Owner-only, because most of these capabilities are shared with
	// ordinary collaboration.
	AuthorizationCapability string
	// ReadCapabilities are the capabilities the interface reads this entry's
	// freeze by. A refusal of one of them may be reported as
	// owner_unavailable; a refusal of anything else may not.
	ReadCapabilities []string
}

// ownerOnlyEntries is every write entry only the Owner may use.
//
// The table is the whole enumeration. Adding an Owner-only entry means
// adding a row here and calling the freeze gate in that entry; the guard
// test fails when the code and this table disagree.
var ownerOnlyEntries = []OwnerOnlyEntry{
	// The activation: the Owner's decision that puts the project live.
	{
		Name:                    "ActivateProject",
		AuthorizationCapability: string(AuthorizationWrite),
		ReadCapabilities:        []string{string(AuthorizationManage)},
	},
	// The template upgrade and the change set apply are one decision: the
	// upgrade rides on the change set that carries it.
	{
		Name:                    "ApplyChangeSet",
		AuthorizationCapability: string(AuthorizationManage),
		ReadCapabilities:        []string{string(AuthorizationManage)},
	},
	// The change set reject: the Owner's decision not to apply.
	{
		Name:                    "RejectChangeSet",
		AuthorizationCapability: string(AuthorizationManage),
		ReadCapabilities:        []string{string(AuthorizationManage)},
	},
	// The formal export: the runtime control only the Owner may use. It is
	// read by its own capability as well, because that is the capability the
	// export entry asks about.
	{
		Name:                    "AuthorizeDeliveryExportInTransaction",
		AuthorizationCapability: string(AuthorizationOpsControl),
		ReadCapabilities:        []string{string(AuthorizationManage), string(AuthorizationOpsControl)},
	},
	// The archive.
	{
		Name:                    "ArchiveProject",
		AuthorizationCapability: string(AuthorizationManage),
		ReadCapabilities:        []string{string(AuthorizationManage)},
	},
	// The owner transfer, in both of its shapes. They are Owner-only writes
	// like the entries above, so they carry the same freeze.
	{
		Name:                    "TransferOwner",
		AuthorizationCapability: string(AuthorizationManage),
		ReadCapabilities:        []string{string(AuthorizationManage)},
	},
	{
		Name:                    "RequestOwnerTransfer",
		AuthorizationCapability: string(AuthorizationManage),
		ReadCapabilities:        []string{string(AuthorizationManage)},
	},
}

// ownerOnlyEntryCapabilities is the derived enumeration: the capabilities by
// which an Owner-only entry's freeze may be reported.
//
// It is derived once, from the registry, so the two can never disagree.
var ownerOnlyEntryCapabilities = deriveOwnerOnlyEntryCapabilities()

// deriveOwnerOnlyEntryCapabilities collects the registry's read
// capabilities, in the registry's order and without repeats.
func deriveOwnerOnlyEntryCapabilities() []string {
	var capabilities []string
	for _, entry := range ownerOnlyEntries {
		for _, capability := range entry.ReadCapabilities {
			if !slices.Contains(capabilities, capability) {
				capabilities = append(capabilities, capability)
			}
		}
	}
	return capabilities
}

// OwnerOnlyEntryCapabilities returns the capabilities by which an Owner-only
// entry's freeze may be reported.
//
// The list is the registry's read capabilities, so it is the enumeration a
// caller has to cover: a refusal of any of them may be answered with
// owner_unavailable, and a refusal of anything else may not.
func OwnerOnlyEntryCapabilities() []string {
	return slices.Clone(ownerOnlyEntryCapabilities)
}

// OwnerOnlyCapability reports whether the capability names an entry only the
// Owner may use.
//
// The answer comes from the registry, so it cannot drift away from the
// entries that actually exist: a capability that is Owner-only but missing
// from the registry answers a refusal with the actor's own lack of authority,
// and the interface cannot tell "the Owner is unavailable" from "you may
// not".
//
// A refusal of any other capability is about the actor's own authority, not
// about a freeze, and saying it is a freeze would swallow the existing
// "capability_or_scope_denied" meaning.
//
// Adding a new Owner-only action means adding its row to the registry in the
// same change: a capability that is Owner-only but missing from the table
// answers a refusal with the actor's own lack of authority.
func OwnerOnlyCapability(capability string) bool {
	return slices.Contains(ownerOnlyEntryCapabilities, capability)
}
