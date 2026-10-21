package candidateadoption

import (
	"time"

	"github.com/Tencent/WeKnora/internal/lingdoc/writingaction"
)

// 采纳夹具的动作与来源标注。
//
// 候选必须记下自己是怎么来的，采纳才可能被确认（见 AdoptionConfirmation）。
// 这两个值是**夹具的一部分**，不是断言的放宽：候选说「我是生成出来的」，
// 确认载荷就得确认同一件事。
const (
	fixtureWritingAction = writingaction.ActionGenerate
	fixtureOrigin        = writingaction.OriginGenerated
)

// fixtureConfirmation 造一份「整段采纳到正式版本」的确认载荷。
func fixtureConfirmation(actor string) *AdoptionConfirmation {
	return &AdoptionConfirmation{
		WritingAction: fixtureWritingAction,
		Target:        writingaction.TargetFormalVersionWhole,
		ConfirmedAt:   time.Date(2026, 10, 22, 1, 0, 0, 0, time.UTC),
		ConfirmedBy:   actor,
		Origin:        fixtureOrigin,
	}
}
