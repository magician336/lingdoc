package workspacecore

import (
	"context"
	"slices"
	"testing"
)

// TestBuildAgentContextSelectionReasonsCarryStableEntryKeys pins the contract
// #432 asked for: every reason names the entry it is about, by kind and id,
// plus the rule that pulled it in. The prose is for the reader and may change;
// these three fields may not.
func TestBuildAgentContextSelectionReasonsCarryStableEntryKeys(t *testing.T) {
	f := newAgentFixture(t, false)
	task := f.task(t)
	anchor := f.anchor(t, "source-1", "g2-reason-anchor")
	card := f.card(t, []SourceAnchor{anchor}, nil, "g2-reason-card", "supports")

	current, err := f.svc.GetProject(context.Background(), f.owner, f.project.ID)
	if err != nil {
		t.Fatal(err)
	}
	pack, err := f.svc.BuildAgentContext(context.Background(), f.owner, f.project.ID, AgentContextInput{
		VersionExpectation:        VersionExpectation{ExpectedProjectVersion: current.ProjectVersion, ExpectedContextRevision: current.CurrentContextRevision, ExpectedVersion: 0},
		TaskID:                    task.ID,
		SourceIDs:                 []string{"source-2"},
		CardRefs:                  []VersionRef{{ID: card.ID, Version: card.Version}},
		SensitiveContextConfirmed: true,
	})
	if err != nil {
		t.Fatalf("build context: %v", err)
	}

	// The source the card depends on is not the source the user picked: the
	// code says which rule brought each one in, while both keep one entry key.
	wanted := []SelectionReason{
		{Kind: SelectionReasonKindTask, ID: task.ID, Code: SelectionReasonUserSelectedTask, Text: "用户选择的当前调研任务"},
		{Kind: SelectionReasonKindCard, ID: card.ID, Code: SelectionReasonUserSelectedCard, Text: "用户选择证据卡；层级=" + card.Status},
		{Kind: SelectionReasonKindSource, ID: "source-1", Code: SelectionReasonCardDependency, Text: "用户选择或卡片明确依赖的当前来源：" + f.source.sources["source-1"].Locator},
		{Kind: SelectionReasonKindSource, ID: "source-2", Code: SelectionReasonUserSelectedSource, Text: "用户选择或卡片明确依赖的当前来源：" + f.source.sources["source-2"].Locator},
	}
	if !slices.Equal(pack.SelectionReasons, wanted) {
		t.Fatalf("selection reasons lost their entry keys:\n got: %+v\nwant: %+v", pack.SelectionReasons, wanted)
	}

	// Every reason answers an entry that is actually in the package.
	for _, reason := range pack.SelectionReasons {
		switch reason.Kind {
		case SelectionReasonKindTask:
			if reason.ID != pack.TaskID || pack.TaskID == "" {
				t.Fatalf("task reason does not name the package's task: %+v", reason)
			}
		case SelectionReasonKindCard:
			if !slices.ContainsFunc(pack.CardRefs, func(ref VersionRef) bool { return ref.ID == reason.ID }) {
				t.Fatalf("card reason does not name a card in the package: %+v", reason)
			}
		case SelectionReasonKindSource:
			if !slices.ContainsFunc(pack.SourceRefs, func(ref AgentSourceRef) bool { return ref.SourceID == reason.ID }) {
				t.Fatalf("source reason does not name a source in the package: %+v", reason)
			}
		default:
			t.Fatalf("unknown reason kind: %+v", reason)
		}
	}
}
