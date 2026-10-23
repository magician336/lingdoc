package workspacecore

// Archived reports whether the project has stopped being active.
//
// A discarded project and an archived one are the same fact for every
// operation that would change it: the project is no longer open for business,
// so the operation must be refused rather than applied to a project nobody
// looks at any more.
//
// The two are stated separately because the storage keeps them apart: the
// discarded timestamp is a column, and the archived status is a value the
// storage vocabulary does not allow yet. The predicate reads both, so the
// guard does not have to know which one the project carries.
func (p Project) Archived() bool {
	return p.DiscardedAt != nil || p.Status == "archived"
}
