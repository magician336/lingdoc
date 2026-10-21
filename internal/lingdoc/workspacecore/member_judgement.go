package workspacecore

// The workspace's judgements about a project's members.
//
// These three are not the member and owner context's own: the workspace asks
// them everywhere — in the change set, the evidence card, the runtime
// control, the review comment, the semantic delta. A project has one owner
// and one member list, and every part of the workspace asks the same
// questions about them.
//
// So they stay here, in one place, and a context that has left the aggregate
// calls them by name.

// ProjectMember returns the member with that user id, if the project has one.
func ProjectMember(project Project, userID string) (Member, bool) {
	return projectMember(project, userID)
}

// GovernanceRole returns the member's governance role.
//
// An owner stays an owner; a member with no governance role is a plain
// member.
func GovernanceRole(member Member) string {
	return governanceRole(member)
}

// ProjectOwner reports whether the user owns the project.
func ProjectOwner(project Project, userID string) bool {
	return projectOwner(project, userID)
}
