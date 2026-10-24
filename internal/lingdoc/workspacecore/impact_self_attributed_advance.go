package workspacecore

import (
	"errors"
	"strconv"
)

// The impacted-object re-read, and the two rules that keep it from being
// unsatisfiable for a material whose license this very change set changed.
//
// The re-read in impactConfirmationBlockers compares every impacted object
// against the version the persisted assessment recorded. For a material that
// the change set itself re-authorized, that comparison is against a version
// the change set's OWN apply has already replaced: applying a license change
// advances the material's own revision by one step, so the snapshot the
// assessment stored can never be current again. Reading that as drift makes
// the license change set structurally impossible to stabilize.
//
// Two rules follow, and they are two rules, not one:
//
//   - A1 (self-attributed advance): a current version that is EXACTLY the
//     version this change set's own apply was declared to produce is that
//     change set's own step, not drift. The declaration comes from the change
//     set's own pinned delta, so it can only ever cover the object this change
//     set changed, and only the exact version it declared. Every other
//     version - including a later one somebody else produced - stays drift and
//     stays blocking.
//
//   - B (a material needs a valid version, not a re-confirmation): the
//     stabilization spec asks for a re-review and a re-bound confirmation for
//     EvidenceCard, ResearchClaim, Chapter and RouteNode, and asks only that
//     the material authorization and the ProjectAsset/SourceAnchor versions
//     are still valid. A material therefore carries no confirmation to
//     re-bind, and the whole requirement for it is version validity.

// semanticSelfAdvanceTarget states the version this change set's own apply is
// expected to have produced for the object its delta names.
//
// The declaration is the delta's own: ExpectedVersion is the version the
// object had when the change set was pinned and assessed, and the apply path
// advances that object by exactly one version step. The apply re-verified,
// under its optimistic lock, that the object was still at ExpectedVersion and
// unchanged (same content hash) before it advanced it, so a current version
// equal to exactly this target is attributable to this change set.
//
// Only kinds whose apply advances the object's own version have an entry, so
// the exemption can never widen into "semantic change sets as a whole".
func semanticSelfAdvanceTarget(delta SemanticDelta) (int64, bool) {
	switch delta.Kind {
	case "ProjectAssetScope":
		return delta.ExpectedVersion + assetPolicyVersionAdvance, true
	}
	return 0, false
}

// selfAttributedAdvances maps "Kind/ID" to the version this change set's own
// apply is expected to have produced for that object.
//
// The change set's persisted semantic plan is the only source. It is the
// declaration the change set itself carries, it is the same plan the apply
// validated under its optimistic lock, and it is written before the apply, so
// it cannot be rewritten by the very drift it is used to detect.
//
// A change set that cannot be read yields no declaration, and therefore no
// exemption: the re-read then keeps blocking, which is the fail-closed answer.
func selfAttributedAdvances(tx Transaction, p Project, changeID string) map[string]string {
	out := map[string]string{}
	if changeID == "" {
		return out
	}
	changes, ok := tx.(changeSetTransaction)
	if !ok {
		return out
	}
	change, err := changes.ChangeSet(p.ID, changeID)
	if err != nil {
		return out
	}
	if len(change.SemanticDeltas) == 0 {
		// Not every store hydrates the plan into the change set; read it
		// explicitly rather than silently losing the declaration.
		change, err = HydrateSemanticChange(tx, change)
		if err != nil {
			return out
		}
	}
	for _, delta := range change.SemanticDeltas {
		target, ok := semanticSelfAdvanceTarget(delta)
		if !ok {
			continue
		}
		ref := semanticRef(delta)
		out[ref.Kind+"/"+ref.ID] = strconv.FormatInt(target, 10)
	}
	return out
}

// projectAssetVersionBlocker judges an impacted material the way the
// stabilization spec asks: its version must still be valid.
//
// "Still valid" has two halves, and both are facts about the material rather
// than about the person who changed it:
//
//   - The material's revision must be the one the assessment recorded, or
//     exactly the one this change set's own apply produced. A revision that
//     is neither is somebody else's work, and still blocks.
//
//   - The content fingerprint the grant was pinned to must still be the
//     material's content. A material whose content was replaced under the
//     same id is no longer covered by the grant, so the grant is invalid even
//     where the revision number still looks untouched.
//
// The second half is why this cannot be reduced to a version comparison: the
// revision number and the fingerprint are different facts, and a replaced
// material can keep its number.
func projectAssetVersionBlocker(tx Transaction, p Project, ref ObjectVersionRef, advances map[string]string) (ImpactBlocker, bool) {
	port, ok := tx.(assetPolicyTransaction)
	if !ok {
		return ImpactBlocker{Code: "impact_object_stale", TargetID: ref.ID,
			Reason: "受影响对象的版本已被替换，需重新评估影响"}, true
	}
	truth, err := port.AssetTruth(p, ref.ID)
	if err != nil {
		if errors.Is(err, ErrVersionConflict) {
			// The active revision no longer carries the fingerprint the
			// knowledge has now: the material's content was replaced under the
			// same id, so the grant pinned to the old content cannot cover it.
			return ImpactBlocker{Code: "impact_object_authorization_invalid", TargetID: ref.ID,
				Reason: "受影响资料的内容已变更，原授权不再适用，需重新授权"}, true
		}
		return ImpactBlocker{Code: "impact_object_stale", TargetID: ref.ID,
			Reason: "受影响对象的版本已被替换，需重新评估影响"}, true
	}
	// The grant names the content fingerprint it was issued against. A
	// material whose content was replaced keeps a revision number in some
	// paths, so the fingerprint is the fact that says whether the grant still
	// covers what the material carries now.
	stored, _, err := loadStoredObject[ProjectAssetPolicy](tx, p.ID, AssetPolicyKind, ref.ID)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return ImpactBlocker{Code: "impact_object_stale", TargetID: ref.ID,
			Reason: "受影响对象的版本已被替换，需重新评估影响"}, true
	}
	if err == nil && stored.SourceVersionHash != "" && stored.SourceVersionHash != truth.Fingerprint {
		return ImpactBlocker{Code: "impact_object_authorization_invalid", TargetID: ref.ID,
			Reason: "受影响资料的内容已变更，原授权不再适用，需重新授权"}, true
	}
	current := strconv.FormatInt(truth.Asset.AssetRevision, 10)
	if current == ref.Version {
		// The assessed version is still the current one: nothing moved.
		return ImpactBlocker{}, false
	}
	if expected, ok := advances[ref.Kind+"/"+ref.ID]; ok && current == expected {
		// Exactly the version this change set's own apply was declared to
		// produce. Anything else - including a later version - is drift.
		return ImpactBlocker{}, false
	}
	return ImpactBlocker{Code: "impact_object_stale", TargetID: ref.ID,
		Reason: "受影响对象的版本已被替换，需重新评估影响"}, true
}
