package workspace

import (
	"archive/zip"
	"bytes"
	"io"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/lingdoc/delivery"
	"github.com/Tencent/WeKnora/internal/lingdoc/docx"
)

// deliveryDocument 是那个适配器的一个实例。它是一个无状态的值类型，但
// `DeliveryDocument{}` 直接写在 if 头里会被当成代码块的花括号——Go 那句著名的
// 「missing parens」——所以在这里起个名字。
var deliveryDocument = DeliveryDocument{}

// renderableInput 是一份 T14 认得的冻结输入。它是字面量而不是走 T12→T13 冻出来的，
// 因为这一组用例问的是适配器怎么处理各种**残缺的**输入——那些输入冻不出来，只能
// 手工摆。整条链能不能跑通用 TestDeliveryDocumentRendersAFrozenRelease 证明。
//
// 第二章也带确认记录（空的处置表）：T13 对每一章都要求确认，没有确认的章节根本
// 冻不进一份 passed 快照——所以适配器要求每一章都有确认，与上游是同一条口径。
func renderableInput() delivery.DeliveryInput {
	return delivery.DeliveryInput{
		ProjectID:    "project-1",
		ProjectName:  "合成资料演示项目",
		DeliveryKind: delivery.DeliveryKindInternalDemo,
		Sources: []delivery.FrozenSource{{
			ID: "source-1", DisplayTitle: "合成测试材料", Locator: "测试段落 1", QuotedText: "100 条虚构记录",
		}},
		Chapters: []delivery.SnapshotChapter{
			{
				ChapterID: "chapter-1", Title: "研究问题",
				BodyMarkdown: "材料包含100条虚构记录 [[source:source-1]]。",
				SourceIDs:    []string{"source-1"},
				ReviewItems:  []delivery.ReviewItem{{ID: "review-1", Statement: "不能证明真实研究结论"}},
				Confirmation: &delivery.Confirmation{Decisions: []delivery.ReviewDecision{{
					ReviewItemID: "review-1", Disposition: delivery.DispositionRetainedWarning, Reason: "仅供内部演示",
				}}},
			},
			{
				ChapterID: "chapter-2", Title: "研究方案",
				BodyMarkdown: "使用合成记录检验软件流程。",
				Confirmation: &delivery.Confirmation{},
			},
		},
	}
}

// 处置的两份字面量在 delivery 与 docx 各定义一次（docx 不依赖 delivery）。适配器
// 原样透传而不是翻译，所以两边一旦漂开就会变成「每次导出都在渲染时失败」。这条断言
// 把它挪到编译期旁边的一个固定位置，失败时一眼看得出是这件事。
func TestDispositionVocabularyIsSharedAcrossTheTwoPackages(t *testing.T) {
	if delivery.DispositionResolved != docx.DispositionResolved {
		t.Fatalf("resolved: delivery=%q docx=%q", delivery.DispositionResolved, docx.DispositionResolved)
	}
	if delivery.DispositionRetainedWarning != docx.DispositionRetainedWarning {
		t.Fatalf("retained_warning: delivery=%q docx=%q", delivery.DispositionRetainedWarning, docx.DispositionRetainedWarning)
	}
}

func TestDeliveryDocumentRendersAndValidatesItsOwnFile(t *testing.T) {
	input := renderableInput()
	file, err := deliveryDocument.RenderFrozen(input)
	if err != nil {
		t.Fatalf("RenderFrozen: %v", err)
	}
	if err := deliveryDocument.ValidateFrozen(input, file); err != nil {
		t.Fatalf("a freshly rendered file failed validation: %v", err)
	}

	text := documentText(t, file)
	// F01-23 的三条：两章都在、「100」这个数字在、附录列出待核项原文与保留原因。
	for _, expected := range []string{"研究问题", "研究方案", "100条虚构记录", "不能证明真实研究结论", "仅供内部演示"} {
		if !strings.Contains(text, expected) {
			t.Errorf("rendered file does not carry %q", expected)
		}
	}
}

// 待核项的处置不在待核项上，而在确认记录的处置表里——这是两套类型之间唯一的真实
// 语义差，也是这一跳存在的全部理由。它必须真的按 review_item_id 联结，而不是随手
// 取一条：联结错了的话 docx.Validate 照样通过（它校验的是同一份翻译结果），
// 只有正文里那串文字能证明。
func TestDeliveryDocumentJoinsEachItemWithItsOwnDisposition(t *testing.T) {
	input := renderableInput()
	input.Chapters[0].ReviewItems = []delivery.ReviewItem{
		{ID: "review-1", Statement: "第一条待核项"},
		{ID: "review-2", Statement: "第二条待核项"},
	}
	input.Chapters[0].Confirmation.Decisions = []delivery.ReviewDecision{
		{ReviewItemID: "review-2", Disposition: delivery.DispositionResolved, Reason: "第二条已解决的理由"},
		{ReviewItemID: "review-1", Disposition: delivery.DispositionRetainedWarning, Reason: "第一条保留的理由"},
	}
	file, err := deliveryDocument.RenderFrozen(input)
	if err != nil {
		t.Fatal(err)
	}
	text := documentText(t, file)
	for _, expected := range []string{"第一条待核项｜处理：保留警示｜理由：第一条保留的理由", "第二条待核项｜处理：已解决（人工处置）｜理由：第二条已解决的理由"} {
		if !strings.Contains(text, expected) {
			t.Fatalf("appendix does not carry %q", expected)
		}
	}
}

// 0 个待核项的章节是合法的：demo 模板的 method 一节就没有待核项。它必须渲成
// 「无待核事项。」，而不是让整次导出失败，也不是留一段空白。
func TestDeliveryDocumentAcceptsAChapterWithNoReviewItems(t *testing.T) {
	input := renderableInput()
	input.Chapters[0].ReviewItems = nil
	input.Chapters[0].Confirmation = &delivery.Confirmation{}
	file, err := deliveryDocument.RenderFrozen(input)
	if err != nil {
		t.Fatal(err)
	}
	if err := deliveryDocument.ValidateFrozen(input, file); err != nil {
		t.Fatal(err)
	}
	if text := documentText(t, file); !strings.Contains(text, "无待核事项。") {
		t.Fatal("a release with nothing to review did not say so")
	}
}

// 残缺的处置表必须让导出失败，而不是渲一份待核附录不完整的文件——附录正是这份
// 交付要说清楚的东西。
func TestDeliveryDocumentRefusesAnIncompleteDispositionTable(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*delivery.DeliveryInput)
		want   string
	}{
		{
			name:   "a chapter carries no confirmation at all",
			mutate: func(in *delivery.DeliveryInput) { in.Chapters[0].Confirmation = nil },
			want:   "no confirmation",
		},
		{
			name:   "the disposition table is empty",
			mutate: func(in *delivery.DeliveryInput) { in.Chapters[0].Confirmation.Decisions = nil },
			want:   "without a disposition",
		},
		{
			name: "a review item is missing from the table",
			mutate: func(in *delivery.DeliveryInput) {
				in.Chapters[0].ReviewItems = append(in.Chapters[0].ReviewItems, delivery.ReviewItem{ID: "review-2", Statement: "第二条"})
			},
			want: "without a disposition",
		},
		{
			name: "the same item is decided twice",
			mutate: func(in *delivery.DeliveryInput) {
				duplicate := in.Chapters[0].Confirmation.Decisions[0]
				in.Chapters[0].Confirmation.Decisions = append(in.Chapters[0].Confirmation.Decisions, duplicate)
			},
			want: "twice",
		},
		{
			name: "the table decides an item the chapter does not have",
			mutate: func(in *delivery.DeliveryInput) {
				in.Chapters[0].Confirmation.Decisions = append(in.Chapters[0].Confirmation.Decisions,
					delivery.ReviewDecision{ReviewItemID: "review-ghost", Disposition: delivery.DispositionResolved, Reason: "无主处置"})
			},
			want: "review-ghost",
		},
		{
			name: "the disposition is outside the published vocabulary",
			mutate: func(in *delivery.DeliveryInput) {
				in.Chapters[0].Confirmation.Decisions[0].Disposition = "maybe_later"
			},
			want: "unsupported review disposition",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := renderableInput()
			test.mutate(&input)
			file, err := deliveryDocument.RenderFrozen(input)
			if err == nil {
				t.Fatalf("rendered %d bytes of a file whose review appendix cannot be trusted", len(file))
			}
			if !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want it to name the reason (%q)", err, test.want)
			}
			// 校验入口走的是同一次翻译，所以它也必须拒绝——否则一条残缺的输入
			// 会在这里被判成「文件没问题」。
			if err := deliveryDocument.ValidateFrozen(input, []byte("PK\x03\x04whatever")); err == nil {
				t.Fatal("the validator accepted an input the renderer refuses")
			}
		})
	}
}

// 校验器必须真的看文件，而不是复述渲染结果：同一份输入配一份别人的文件要失败。
func TestDeliveryDocumentValidatesTheFileNotTheIntent(t *testing.T) {
	input := renderableInput()
	file, err := deliveryDocument.RenderFrozen(input)
	if err != nil {
		t.Fatal(err)
	}
	other := renderableInput()
	other.Chapters[1].Title = "研究方法"
	if err := deliveryDocument.ValidateFrozen(other, file); err == nil {
		t.Fatal("a file from one input validated against another")
	}
}

// 整条链：T12 的库 → 交付输入 → T13 的检查与冻结 → T14 的渲染与校验。
// 这是「T14 接上了」本身，不是适配器的又一个用例。
func TestDeliveryDocumentRendersAFrozenRelease(t *testing.T) {
	handler, db, assetID := seedBoundSource(t)
	seedDeliveryWorkspace(t, db,
		deliveryQuestionChapter(assetID, currentAssetRevision(t, handler, assetID), deliveryBody, []string{deliverySourceID}),
		deliveryMethodChapter(),
	)
	service := newDeliveryReleaseService(t, handler)
	ctx := policyContext()

	snapshot, _, err := service.Prepare(ctx, deliveryActorID, "project-1", deliveryFreezeKey, deliveryProjectVersion)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if snapshot.Check.Status != delivery.CheckPassed {
		t.Fatalf("snapshot status = %s with %v", snapshot.Check.Status, snapshot.Check.Issues)
	}

	frozen := snapshot.FrozenInput
	file, err := deliveryDocument.RenderFrozen(frozen)
	if err != nil {
		t.Fatalf("渲染冻结快照: %v", err)
	}
	if err := deliveryDocument.ValidateFrozen(frozen, file); err != nil {
		t.Fatalf("冻结快照渲染出的文件过不了校验: %v", err)
	}

	// 附录要列出 review-1 的原文与保留原因——这是 F01-23 点名的那一条，
	// 也是「保留警示随文件带走」在实物上的样子。
	text := documentText(t, file)
	for _, expected := range []string{
		"研究问题", "研究方案",
		"100条合成记录不能证明真实研究结论。",
		"仅作内部演示，保留该项并随文件明确列出。",
		// 引用资料段带的是这条来源的展示标题与原文，两者都来自冻结来源记录。
		"synthetic PDF", sourcePolicyQuote,
	} {
		if !strings.Contains(text, expected) {
			t.Errorf("交付文件里没有 %q", expected)
		}
	}
}

// documentText 取回文件里 word/document.xml 的原始文本。
//
// 这是一次字符串探测，不是解析：用例要问的是「附录里有没有这句话」，而正文里这些
// 字符不会被 XML 转义。文件本身合不合格由 docx.Validate 判，不在这里重复。
func documentText(t *testing.T, file []byte) string {
	t.Helper()
	reader, err := zip.NewReader(bytes.NewReader(file), int64(len(file)))
	if err != nil {
		t.Fatal(err)
	}
	for _, member := range reader.File {
		if member.Name != "word/document.xml" {
			continue
		}
		stream, err := member.Open()
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = stream.Close() }()
		content, err := io.ReadAll(stream)
		if err != nil {
			t.Fatal(err)
		}
		return string(content)
	}
	t.Fatal("rendered file carries no word/document.xml")
	return ""
}
