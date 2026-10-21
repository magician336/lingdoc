package workspace

import "testing"

type focusedProjectPortStub struct{ ProjectApplication }
type focusedMembershipPortStub struct{ MembershipApplication }
type focusedChapterPortStub struct{ ChapterApplication }
type focusedTemplatePortStub struct{ TemplateApplication }
type focusedChangeSetPortStub struct{ ChangeSetApplication }

func TestHandlerAcceptsFocusedApplicationPorts(t *testing.T) {
	projects := &focusedProjectPortStub{}
	members := &focusedMembershipPortStub{}
	chapters := &focusedChapterPortStub{}
	templates := &focusedTemplatePortStub{}
	changeSets := &focusedChangeSetPortStub{}
	handler, err := NewHandler(HandlerDependencies{
		Projects:    projects,
		Members:     members,
		Chapters:    chapters,
		TemplateApp: templates,
		ChangeSets:  changeSets,
		Sources:     emptySourcesStub{},
	})
	if err != nil {
		t.Fatalf("assemble the workspace handler: %v", err)
	}
	if handler.projects != projects || handler.members != members || handler.chapters != chapters || handler.templatesApp != templates || handler.changeSets != changeSets {
		t.Fatalf("focused ports were not retained: projects=%T members=%T chapters=%T templates=%T change_sets=%T", handler.projects, handler.members, handler.chapters, handler.templatesApp, handler.changeSets)
	}
}
