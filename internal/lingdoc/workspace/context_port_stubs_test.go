package workspace

// The five context ports, and the test-side assembly shortcut.
//
// The legacy wide ports (ApplicationService, WorkspaceReadApplication,
// WorkspaceWriteApplication) are retired: the composition root no longer
// infers one port from another, so every context port has to be handed over
// by name. The helper below is the test-side spelling of that: one service
// double, five named ports.

// contextPortStub is a service double that satisfies the five context ports.
type contextPortStub interface {
	ProjectApplication
	MembershipApplication
	ChapterApplication
	TemplateApplication
	ChangeSetApplication
}

// contextPorts assembles the five context ports from one service double.
//
// The ports are named, not inferred: each one is the port of the context it
// belongs to, and the assembly fails when one of them is missing.
func contextPorts(service contextPortStub, sources SourceApplicationService) HandlerDependencies {
	return HandlerDependencies{
		Projects:    service,
		Members:     service,
		Chapters:    service,
		TemplateApp: service,
		ChangeSets:  service,
		Sources:     sources,
	}
}
