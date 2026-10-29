package workspacecore

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

// utf16LengthOf mirrors the server offset convention: runes above the BMP
// count as two UTF-16 code units.
func utf16LengthOf(s string) int {
	length := 0
	for _, r := range s {
		if r > 0xFFFF {
			length += 2
			continue
		}
		length++
	}
	return length
}

func TestLocateImpactPositionsFindsRegisteredValuesWithParagraphAndUtf16Offsets(t *testing.T) {
	// Two emoji keep the UTF-16 bookkeeping honest: each one is two code
	// units but one rune, so a byte- or rune-based offset would disagree.
	body := "😀😀旧研究主题全文"
	hints := locateImpactPositions(body, []impactLocationValue{
		{Source: ImpactLocationSourceRegisteredValue, FieldKey: "research_subject", Value: "旧研究主题全文"},
	})
	if len(hints) != 1 {
		t.Fatalf("hints = %+v, want exactly one", hints)
	}
	hint := hints[0]
	if hint.ParagraphIndex != 1 || hint.StartOffset != 4 || hint.EndOffset != 11 {
		t.Fatalf("hint position = %+v, want paragraph 1, utf16 [4,11)", hint)
	}
	if hint.Source != ImpactLocationSourceRegisteredValue || hint.FieldKey != "research_subject" || hint.Value != "旧研究主题全文" {
		t.Fatalf("hint identity = %+v", hint)
	}
	if hint.Excerpt != "😀😀旧研究主题全文" {
		t.Fatalf("excerpt = %q", hint.Excerpt)
	}

	paragraphed := "首段 unrelated。\n\n旧研究主题全文见于次段，再提一次旧研究主题全文。\n\n末段含登记目标值为定位而设。"
	values := impactLocationValues(ChangeSet{Fields: []ChangeFieldDelta{{Key: "research_subject", OldValue: "旧研究主题全文"}}}, map[string]string{"research_goal": "登记目标值为定位而设"})
	hints = locateImpactPositions(paragraphed, values)
	if len(hints) != 3 {
		t.Fatalf("hints = %+v, want three", hints)
	}
	// The two old-value hits sit in paragraph 2 at hand-computed UTF-16
	// offsets: paragraph 1 (13 runes) plus the blank line (2) puts paragraph
	// 2 at 15; the second hit follows 16 more runes.
	want := []ImpactLocationHint{
		{Source: ImpactLocationSourceChangeOldValue, FieldKey: "research_subject", Value: "旧研究主题全文", ParagraphIndex: 2, StartOffset: 15, EndOffset: 22},
		{Source: ImpactLocationSourceChangeOldValue, FieldKey: "research_subject", Value: "旧研究主题全文", ParagraphIndex: 2, StartOffset: 31, EndOffset: 38},
		{Source: ImpactLocationSourceRegisteredValue, FieldKey: "research_goal", Value: "登记目标值为定位而设", ParagraphIndex: 3, StartOffset: 44, EndOffset: 54},
	}
	for i, expected := range want {
		got := hints[i]
		if got.Source != expected.Source || got.FieldKey != expected.FieldKey || got.Value != expected.Value ||
			got.ParagraphIndex != expected.ParagraphIndex || got.StartOffset != expected.StartOffset || got.EndOffset != expected.EndOffset {
			t.Fatalf("hint %d = %+v, want %+v", i, got, expected)
		}
		if !strings.Contains(got.Excerpt, got.Value) {
			t.Fatalf("hint %d excerpt %q lost the value", i, got.Excerpt)
		}
	}
	for i, hint := range hints {
		if i > 0 && hint.StartOffset < hints[i-1].StartOffset {
			t.Fatalf("hints not sorted by offset: %+v", hints)
		}
		if utf16LengthOf(hint.Excerpt) > 2*impactLocationContextRunes+utf16LengthOf(hint.Value) {
			t.Fatalf("hint %d excerpt unbounded: %q", i, hint.Excerpt)
		}
	}

	if got := locateImpactPositions("", values); len(got) != 0 {
		t.Fatalf("empty body hints = %+v", got)
	}
	if got := locateImpactPositions(paragraphed, nil); len(got) != 0 {
		t.Fatalf("no values hints = %+v", got)
	}
}

func TestLocateImpactPositionsPrefersChangeOldValueSourceOnDuplicateValues(t *testing.T) {
	values := impactLocationValues(
		ChangeSet{Fields: []ChangeFieldDelta{
			{Key: "goal", OldValue: "  共同登记的值  "},
			{Key: "scope", OldValue: "共同登记的值"},
		}},
		map[string]string{"goal": "共同登记的值", "subject": "单独登记的研究值", "tiny": "小"},
	)
	// The duplicated string keeps only its first (change) source and
	// sub-floor values ("小") drop exactly as in the #753 gate, leaving the
	// change value once and the spec-only value once.
	if len(values) != 2 {
		t.Fatalf("values = %+v, want two after dedup and floor", values)
	}
	if values[0].Source != ImpactLocationSourceChangeOldValue || values[0].FieldKey != "goal" || values[0].Value != "共同登记的值" {
		t.Fatalf("first value = %+v", values[0])
	}
	if values[1].FieldKey != "subject" || values[1].Source != ImpactLocationSourceRegisteredValue || values[1].Value != "单独登记的研究值" {
		t.Fatalf("second value = %+v", values[1])
	}
}

func TestLocateImpactPositionsNoiseFloorCapsAndDeterminism(t *testing.T) {
	value := impactLocationValue{Source: ImpactLocationSourceRegisteredValue, FieldKey: "k", Value: "重复出现的研究值"}
	repeated := strings.Repeat("前。", 30) + strings.Repeat(value.Value, 8)
	hints := locateImpactPositions(repeated, []impactLocationValue{value})
	if len(hints) != impactLocationMaxMatchesPerValue {
		t.Fatalf("hints = %d, want the per-value cap %d", len(hints), impactLocationMaxMatchesPerValue)
	}
	wide := []impactLocationValue{}
	for i := 0; i < 12; i++ {
		wide = append(wide, impactLocationValue{Source: ImpactLocationSourceRegisteredValue, FieldKey: string(rune('A'+i)) + "字段", Value: "共同的研究值"})
	}
	flooded := strings.Repeat("共同的研究值", 10)
	hints = locateImpactPositions(flooded, wide)
	if len(hints) != impactLocationMaxHintsPerTask {
		t.Fatalf("hints = %d, want the per-task cap %d", len(hints), impactLocationMaxHintsPerTask)
	}
	first := locateImpactPositions(flooded, wide)
	if len(first) != len(hints) {
		t.Fatalf("second run length = %d", len(first))
	}
	for i := range hints {
		if first[i] != hints[i] {
			t.Fatalf("run %d diverged: %+v vs %+v", i, first[i], hints[i])
		}
	}
}

// impactLocationApplyChange runs one field change through the real
// assess-and-apply chain (every change names an affected chapter; the input
// validation refuses none) and returns the refreshed project.
func impactLocationApplyChange(t *testing.T, svc *Service, actor Actor, project Project, chapters []Chapter, tag, fieldName string, field ChangeFieldInput) Project {
	t.Helper()
	raw, _, _, err := svc.CreateChangeSet(context.Background(), actor, project.ID, tag+"-create", CreateChangeSetInput{
		ExpectedContextRevision: project.CurrentContextRevision,
		Fields:                  map[string]ChangeFieldInput{fieldName: field},
		AffectedChapterIDs:      []string{chapters[0].ID},
		Reason:                  "定位搭建:" + tag,
	})
	if err != nil {
		t.Fatalf("create %s: %v", tag, err)
	}
	var change ChangeSet
	if err := json.Unmarshal(raw, &change); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := svc.ApplyChangeSet(context.Background(), actor, project.ID, change.ID, tag+"-apply"); err != nil {
		t.Fatalf("apply %s: %v", tag, err)
	}
	project, err = svc.GetProject(context.Background(), actor, project.ID)
	if err != nil {
		t.Fatal(err)
	}
	return project
}

// impactLocationSeed builds an active project whose registered values are
// long enough to pass the #753 noise floor. Registered values can only be
// set through changes on an active project, so two seed changes lift both
// fields first; the watched strings exist in the spec before the change
// under test runs.
func impactLocationSeed(t *testing.T, body string, tenant uint64) (*Service, Actor, Project, []Chapter) {
	t.Helper()
	svc := testStore(t, filepath.Join(t.TempDir(), "impact-location.db"))
	actor := Actor{TenantID: tenant, UserID: "owner"}
	project := seedActiveImpactProject(t, svc, actor)
	chapters, err := svc.ListChapters(context.Background(), actor, project.ID)
	if err != nil || len(chapters) == 0 {
		t.Fatalf("chapters: %v %+v", err, chapters)
	}
	project = impactLocationApplyChange(t, svc, actor, project, chapters, "impact-location-s1", "research_subject", ChangeFieldInput{OldValue: "旧主题", NewValue: "旧研究主题全文"})
	if _, _, _, err := svc.SaveChapter(context.Background(), actor, project.ID, chapters[0].ID, "impact-location-chapter", SaveChapterInput{
		ExpectedChapterVersionID: chapters[0].CurrentVersionID,
		ExpectedSpecRevision:     project.SpecRevision,
		BodyMarkdown:             body,
		SourceIDs:                []string{},
	}); err != nil {
		t.Fatalf("seed chapter: %v", err)
	}
	return svc, actor, project, chapters
}

func impactLocationChapterTask(t *testing.T, tasks []ImpactTask, changeSetID string) ImpactTask {
	t.Helper()
	for _, task := range tasks {
		if task.Target.Kind == "ChapterVersion" && task.ChangeSetID == changeSetID {
			return task
		}
	}
	t.Fatalf("no ChapterVersion task of %s in %+v", changeSetID, tasks)
	return ImpactTask{}
}

func impactLocationRunWatchedChange(t *testing.T, svc *Service, actor Actor, project Project, chapters []Chapter, tag, reason string) ImpactTask {
	t.Helper()
	raw, _, _, err := svc.CreateChangeSet(context.Background(), actor, project.ID, tag+"-create", CreateChangeSetInput{
		ExpectedContextRevision: project.CurrentContextRevision,
		Fields:                  map[string]ChangeFieldInput{"research_subject": {OldValue: "旧研究主题全文", NewValue: "新研究主题定稿"}},
		AffectedChapterIDs:      []string{chapters[0].ID},
		Reason:                  reason,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	var change ChangeSet
	if err := json.Unmarshal(raw, &change); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := svc.ApplyChangeSet(context.Background(), actor, project.ID, change.ID, tag+"-apply"); err != nil {
		t.Fatalf("apply: %v", err)
	}
	tasks, err := svc.ListImpactTasks(context.Background(), actor, project.ID)
	if err != nil {
		t.Fatal(err)
	}
	return impactLocationChapterTask(t, tasks, change.ID)
}

func TestCommitChangeImpactTasksAttachesLocationHints(t *testing.T) {
	body := "首段 unrelated。\n\n旧研究主题全文见于次段，再提一次旧研究主题全文。\n\n末段含登记目标值为定位而设。"
	svc, actor, project, chapters := impactLocationSeed(t, body, 341)
	// The registered-value watch list is the applied spec, so lift the goal
	// field with its own change; the subject change under test then runs
	// with both a change old value and an unrelated registered value.
	project = impactLocationApplyChange(t, svc, actor, project, chapters, "impact-location-s2", "research_goal", ChangeFieldInput{OldValue: "旧目标", NewValue: "登记目标值为定位而设"})
	project, err := svc.GetProject(context.Background(), actor, project.ID)
	if err != nil {
		t.Fatal(err)
	}
	chapters, err = svc.ListChapters(context.Background(), actor, project.ID)
	if err != nil {
		t.Fatal(err)
	}
	task := impactLocationRunWatchedChange(t, svc, actor, project, chapters, "impact-location-run", "研究对象变化,定位到正文")
	if len(task.LocationHints) != 3 {
		t.Fatalf("hints = %+v, want three", task.LocationHints)
	}
	oldValueHits := 0
	for _, hint := range task.LocationHints {
		if hint.Source != ImpactLocationSourceChangeOldValue || hint.FieldKey != "research_subject" || hint.Value != "旧研究主题全文" {
			continue
		}
		oldValueHits++
		if hint.ParagraphIndex != 2 || (hint.StartOffset != 15 && hint.StartOffset != 31) {
			t.Fatalf("old value hint = %+v", hint)
		}
	}
	if oldValueHits != 2 {
		t.Fatalf("old value hits = %d, want 2", oldValueHits)
	}
	if !strings.Contains(task.LocationHints[2].Excerpt, "登记目标值为定位而设") || task.LocationHints[2].Source != ImpactLocationSourceRegisteredValue || task.LocationHints[2].ParagraphIndex != 3 {
		t.Fatalf("registered hint = %+v", task.LocationHints[2])
	}
	if task.Status != "OPEN" || task.Severity == "" || task.ResponsibilityDomain == "" {
		t.Fatalf("task verdict fields = %+v", task)
	}
}

func TestCommitChangeImpactTasksWithoutHitsLeavesHintsEmpty(t *testing.T) {
	svc, actor, project, chapters := impactLocationSeed(t, "与本变更无关的正文，不含任何受盯值。", 342)
	task := impactLocationRunWatchedChange(t, svc, actor, project, chapters, "impact-location-empty", "研究对象变化,正文无命中")
	if len(task.LocationHints) != 0 {
		t.Fatalf("hints = %+v, want none", task.LocationHints)
	}
	if task.Status != "OPEN" || task.Action == "" {
		t.Fatalf("task = %+v", task)
	}
}

func TestImpactLocationHintsDoNotChangeTaskVerdict(t *testing.T) {
	withHits, actorA, projectA, chaptersA := impactLocationSeed(t, "首段 unrelated。\n\n旧研究主题全文见于次段。", 343)
	withoutHits, actorB, projectB, chaptersB := impactLocationSeed(t, "完全无关的正文。", 344)
	type verdict struct {
		Action, Severity, Domain, Status string
		Causes                           int
	}
	run := func(svc *Service, actor Actor, project Project, chapters []Chapter, tag string) verdict {
		task := impactLocationRunWatchedChange(t, svc, actor, project, chapters, tag, "同一变更,两个正文")
		return verdict{Action: task.Action, Severity: task.Severity, Domain: task.ResponsibilityDomain, Status: task.Status, Causes: len(task.Causes)}
	}
	withVerdict := run(withHits, actorA, projectA, chaptersA, "impact-verdict-a")
	withoutVerdict := run(withoutHits, actorB, projectB, chaptersB, "impact-verdict-b")
	if withVerdict != withoutVerdict {
		t.Fatalf("verdicts diverge: with=%+v without=%+v", withVerdict, withoutVerdict)
	}
}
