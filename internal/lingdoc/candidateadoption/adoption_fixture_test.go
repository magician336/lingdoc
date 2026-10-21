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

// fixtureConfirmationFor 造一份确认载荷，采纳目标是给定的那个。
//
// 动作仍是同一个（generate 允许全部三个目标），变的只是「写到哪里」——所以
// 这条夹具改的正是本票要盯的那一维。
func fixtureConfirmationFor(target writingaction.AdoptionTarget, actor string) *AdoptionConfirmation {
	confirmation := fixtureConfirmation(actor)
	confirmation.Target = target
	return confirmation
}

// fixturePartialConfirmation 造一份「采纳到工作副本选段」的确认载荷。
func fixturePartialConfirmation(actor string, start, end int) *AdoptionConfirmation {
	confirmation := fixtureConfirmationFor(writingaction.TargetWorkingCopyPartial, actor)
	confirmation.Scope = &AdoptionScope{StartUTF16: start, EndUTF16: end}
	return confirmation
}

// fixtureWorkingCopyState 造一份「客户端看到的工作副本」声明。
//
// 没有工作副本的用例声明修订号 0 与空正文的摘要：服务端把「没有工作副本」
// 看成修订号 0、正文为空，两边算的是同一件事。
func fixtureWorkingCopyState(revision int64, body string) (int64, string) {
	return revision, workingCopyBodyHash(body)
}
