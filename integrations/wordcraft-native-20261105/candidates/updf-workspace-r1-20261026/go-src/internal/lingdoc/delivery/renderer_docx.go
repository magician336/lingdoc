package delivery

import (
	"fmt"
	"github.com/Tencent/WeKnora/internal/lingdoc/docx"
)

// DOCXRenderer adapts frozen delivery values to the existing T05 renderer.
// It does not query live chapters, rewrite confirmations or invent decisions.
type DOCXRenderer struct{}

func (DOCXRenderer) RenderFrozen(input DeliveryInput) ([]byte, error) {
	in := docx.Input{ProjectName: input.ProjectName, DeliveryKind: input.DeliveryKind}
	for _, s := range input.Sources {
		in.Sources = append(in.Sources, docx.Source{ID: s.ID, DisplayTitle: s.DisplayTitle, Locator: s.Locator, QuotedText: s.QuotedText})
	}
	for _, c := range input.Chapters {
		chapter := docx.Chapter{Title: c.Title, BodyMarkdown: c.BodyMarkdown, SourceIDs: c.SourceIDs}
		decisions := map[string]ReviewDecision{}
		if c.Confirmation != nil {
			for _, d := range c.Confirmation.Decisions {
				if _, exists := decisions[d.ReviewItemID]; exists {
					return nil, fmt.Errorf("duplicate review decision")
				}
				decisions[d.ReviewItemID] = d
			}
		}
		for _, item := range c.ReviewItems {
			d, exists := decisions[item.ID]
			if !exists {
				return nil, fmt.Errorf("missing review decision for %s", item.ID)
			}
			chapter.ReviewItems = append(chapter.ReviewItems, docx.ReviewItem{ID: item.ID, Statement: item.Statement, Disposition: d.Disposition, Reason: d.Reason})
		}
		in.Chapters = append(in.Chapters, chapter)
	}
	return docx.Render(in)
}

var _ FrozenRenderer = DOCXRenderer{}
