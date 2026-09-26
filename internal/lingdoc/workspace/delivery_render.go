package workspace

import (
	"fmt"
	"sort"

	"github.com/Tencent/WeKnora/internal/lingdoc/delivery"
	"github.com/Tencent/WeKnora/internal/lingdoc/docx"
)

// DeliveryDocument 是交付的 DOCX 形态：生成它，以及核对生成出来的那份文件。
//
// 两个方法落在同一个类型上，是因为它们共用同一跳翻译（冻结输入 → DOCX 输入）。
// 拆成两个类型就得把那一跳写两遍，而两份翻译迟早会在某个字段上分叉——那时校验器
// 会开始拒绝渲染器自己产出的文件，或者更糟，放行它。
//
// 放在 workspace 而不是 docx，理由与 DeliveryInputBuilder 同一条：它是两个包之间的
// 适配器，而 workspace 已经同时握着交付侧与装配点；docx 不反向依赖 delivery。
type DeliveryDocument struct{}

// 交付侧要求的两个接口，签名一旦漂开就在这里编译不过——比等到装配处
// 拿到 nil 再回头找要早得多。
var (
	_ delivery.FrozenRenderer  = DeliveryDocument{}
	_ delivery.FrozenValidator = DeliveryDocument{}
)

// RenderFrozen 把冻结输入渲染成一份可编辑的 DOCX。
//
// 渲染器自身的约束（只支持 internal_demo、恰好两章、正文标记与 source_ids 对得上、
// 拒绝富 Markdown）一律不由这里兜底：冻结输入违反它们是 T13 检查该拦下的毛病，
// 走到这里还违反就是真的该失败——渲一份正文与引用对不上的文件，比不渲更糟。
func (DeliveryDocument) RenderFrozen(input delivery.DeliveryInput) ([]byte, error) {
	document, err := documentOf(input)
	if err != nil {
		return nil, err
	}
	return docx.Render(document)
}

// ValidateFrozen 拿渲染器刚产出的字节核对冻结输入。它不问渲染器说了什么，
// 只问文件里有什么——§7 要求这一步在提供下载**之前**发生。
func (DeliveryDocument) ValidateFrozen(input delivery.DeliveryInput, file []byte) error {
	document, err := documentOf(input)
	if err != nil {
		return err
	}
	return docx.Validate(document, file)
}

// documentOf 是这一跳的全部内容。
//
// 两套类型之间只有一处真实的语义差：**处置不在待核项上，而在确认记录的处置表里**。
// 其余都是同名字段搬运。所以这一跳存在的唯一理由就是把那张表按 review_item_id
// 联接到待核项上。
//
// T12 写入确认时（candidateadoption 的 validateReviewDecisions）与 T13 冻结时
// （delivery 的 checkDecisions）都已要求这张表与待核项严格双射，所以能走到导出这一步
// 的输入，联接必然成立。这里仍然自己查一遍重复、缺失与孤儿：那是**两次检查之间的
// 时间差**——确认之后、冻结之后，输入仍可能被别处改动。宁可让导出失败，也不渲一份
// 待核附录不完整的文件，因为附录正是这份交付要说清楚的东西。
func documentOf(input delivery.DeliveryInput) (docx.Input, error) {
	document := docx.Input{ProjectName: input.ProjectName, DeliveryKind: input.DeliveryKind}
	for _, source := range input.Sources {
		document.Sources = append(document.Sources, docx.Source{
			ID:           source.ID,
			DisplayTitle: source.DisplayTitle,
			Locator:      source.Locator,
			QuotedText:   source.QuotedText,
		})
	}
	for _, chapter := range input.Chapters {
		converted, err := documentChapter(chapter)
		if err != nil {
			return docx.Input{}, err
		}
		document.Chapters = append(document.Chapters, converted)
	}
	return document, nil
}

func documentChapter(chapter delivery.SnapshotChapter) (docx.Chapter, error) {
	if chapter.Confirmation == nil {
		return docx.Chapter{}, fmt.Errorf("chapter %q has no confirmation to read review dispositions from", chapter.ChapterID)
	}
	decisions := make(map[string]delivery.ReviewDecision, len(chapter.Confirmation.Decisions))
	for _, decision := range chapter.Confirmation.Decisions {
		if _, exists := decisions[decision.ReviewItemID]; exists {
			return docx.Chapter{}, fmt.Errorf("chapter %q decides review item %q twice", chapter.ChapterID, decision.ReviewItemID)
		}
		decisions[decision.ReviewItemID] = decision
	}

	out := docx.Chapter{
		Title:        chapter.Title,
		BodyMarkdown: chapter.BodyMarkdown,
		SourceIDs:    chapter.SourceIDs,
	}
	for _, item := range chapter.ReviewItems {
		decision, decided := decisions[item.ID]
		if !decided {
			return docx.Chapter{}, fmt.Errorf("chapter %q leaves review item %q without a disposition", chapter.ChapterID, item.ID)
		}
		delete(decisions, item.ID)
		// OriginCandidateID 如实丢弃：它记录这条待核项从哪个候选来，是仓内的溯源
		// 信息，不在 DOCX 契约里——交付件要说的是结论与理由，不是内部血缘。
		out.ReviewItems = append(out.ReviewItems, docx.ReviewItem{
			ID:          item.ID,
			Statement:   item.Statement,
			Disposition: decision.Disposition,
			Reason:      decision.Reason,
		})
	}
	if len(decisions) != 0 {
		return docx.Chapter{}, fmt.Errorf("chapter %q carries dispositions for review items it does not have: %v", chapter.ChapterID, sortedKeys(decisions))
	}
	return out, nil
}

func sortedKeys(decisions map[string]delivery.ReviewDecision) []string {
	out := make([]string, 0, len(decisions))
	for id := range decisions {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}
