package workspacecore

import (
	"context"
	"errors"
	"testing"
)

// #468 残留②的复现:回退的逆向在前(独立事务)、控制记录写在后(另一个事务)。
// 控制写失败时(这里用错误的乐观版本号让 mutation 返回 ErrVersionConflict),
// 逆向已经发生且不回滚——系统里留下一条没人认领的逆向变更集草稿。
//
// 完成线:控制写失败 ⇒ 逆向一并回滚(或补偿把状态拉回)——调用方拿到的错误
// 之外,领域里不应该有任何回退留下的东西。
func TestG6RollbackThatFailsToWriteTheControlLeavesNoInverseBehind(t *testing.T) {
	svc, actor, p := extendedImpactStore(t)
	ctx := context.Background()
	chapter := extendedChapter(t, svc, actor, p)
	change := appliedChangeSet(t, svc, actor, p, chapter)

	// 控制写注定失败:ExpectedVersion 给一个不存在的版本号。
	_, _, _, writeErr := svc.ControlRuntime(ctx, actor, p.ID, "rollback-bad-version", RuntimeControlInput{
		ExpectedVersion: 5,
		Scope:           RuntimeScope{Kind: "project", Value: p.ID}, Action: "rollback", Reason: "回退(控制写将失败)",
		Rollback: &RuntimeRollbackInput{ChangeSetID: change.ID, TemplateVersion: p.TemplateVersion, RulesetHash: p.TemplateCopy.RulesetHash},
	})
	if !errors.Is(writeErr, ErrVersionConflict) {
		t.Fatalf("the control write did not fail the way the fixture intended: %v", writeErr)
	}

	changes, err := svc.ListChangeSets(ctx, actor, p.ID)
	if err != nil {
		t.Fatalf("list the change sets: %v", err)
	}
	inverse := 0
	for _, candidate := range changes {
		if candidate.ReversesChangeSetID == change.ID {
			inverse++
		}
	}
	if inverse != 0 {
		t.Fatalf("a failed control write left %d inverse change set(s) behind: %+v", inverse, changes)
	}
}

// #468 契约①:逆向与控制写合成一个事务之后,重放语义必须仍然成立——成功的
// 回退用同一个 key 再发一次,拿到的是**重放**的同一份控制记录,逆向变更集
// 不会被创建第二份。合成之前这件事由两层各自的重放保证;合成之后由单笔
// ledger 记录保证(写事务回滚时 ledger 一并消失,重试从头执行,含逆向)。
func TestG6RollbackReplayDoesNotDuplicateTheInverse(t *testing.T) {
	svc, actor, p := extendedImpactStore(t)
	ctx := context.Background()
	chapter := extendedChapter(t, svc, actor, p)
	change := appliedChangeSet(t, svc, actor, p, chapter)

	input := RuntimeControlInput{
		Scope: RuntimeScope{Kind: "project", Value: p.ID}, Action: "rollback", Reason: "回退(将重放)",
		Rollback: &RuntimeRollbackInput{ChangeSetID: change.ID, TemplateVersion: p.TemplateVersion, RulesetHash: p.TemplateCopy.RulesetHash},
	}
	raw, _, _, err := svc.ControlRuntime(ctx, actor, p.ID, "rollback-replay-key", input)
	if err != nil {
		t.Fatalf("rollback: %v", err)
	}
	replayedRaw, replayStatus, replayed, err := svc.ControlRuntime(ctx, actor, p.ID, "rollback-replay-key", input)
	if err != nil || !replayed || replayStatus != 200 || string(replayedRaw) != string(raw) {
		t.Fatalf("rollback replay: status=%d replay=%v same=%v err=%v", replayStatus, replayed, string(replayedRaw) == string(raw), err)
	}

	changes, err := svc.ListChangeSets(ctx, actor, p.ID)
	if err != nil {
		t.Fatalf("list the change sets: %v", err)
	}
	inverse := 0
	for _, candidate := range changes {
		if candidate.ReversesChangeSetID == change.ID {
			inverse++
		}
	}
	if inverse != 1 {
		t.Fatalf("the replay duplicated the inverse change set: %d found: %+v", inverse, changes)
	}
}

// 模板分支:逆向一个**模板升级**变更集时,归档字段的溯源请求必须由
// runtimeControl 自己的 Prepare 收集(修复引入该 case 之前,合成 mutation 里
// 的归档字段校验拿不到 proof 请求,回退会以 403 来源拒绝失败——那也是一类
// "控制没写成,状态却动了"的缺口)。原子性对模板分支同样成立:控制写失败
// ⇒ 逆提案一并回滚。
func TestG6RollbackOfATemplateUpgradeFailsAtomicallyToo(t *testing.T) {
	svc, actor, baseline, after, source, _ := r26TemplateDeltaFixture(t, false)
	ctx := context.Background()

	_, _, _, writeErr := svc.ControlRuntime(ctx, actor, after.ID, "rollback-template-bad-version", RuntimeControlInput{
		ExpectedVersion: 7,
		Scope:           RuntimeScope{Kind: "project", Value: after.ID}, Action: "rollback", Reason: "回退模板升级(控制写将失败)",
		Rollback: &RuntimeRollbackInput{ChangeSetID: source.ID, TemplateVersion: baseline.TemplateVersion, RulesetHash: baseline.TemplateCopy.RulesetHash},
	})
	if !errors.Is(writeErr, ErrVersionConflict) {
		t.Fatalf("the control write did not fail the way the fixture intended: %v", writeErr)
	}

	changes, err := svc.ListChangeSets(ctx, actor, after.ID)
	if err != nil {
		t.Fatalf("list the change sets: %v", err)
	}
	inverse := 0
	for _, candidate := range changes {
		if candidate.ReversesChangeSetID == source.ID {
			inverse++
		}
	}
	if inverse != 0 {
		t.Fatalf("a failed control write on a template upgrade left %d inverse change set(s) behind", inverse)
	}

	// 同形状的成功回退也要真的能走通(归档请求齐备,不是靠 403 失败):换一个
	// 正确的乐观版本号再回退一次(新键,版本从 0 起算),应当成功且逆提案恰有一条。
	if _, _, _, err := svc.ControlRuntime(ctx, actor, after.ID, "rollback-template-ok", RuntimeControlInput{
		ExpectedVersion: 0,
		Scope:           RuntimeScope{Kind: "project", Value: after.ID}, Action: "rollback", Reason: "回退模板升级",
		Rollback: &RuntimeRollbackInput{ChangeSetID: source.ID, TemplateVersion: baseline.TemplateVersion, RulesetHash: baseline.TemplateCopy.RulesetHash},
	}); err != nil {
		t.Fatalf("the rollback of a template upgrade could not run at all: %v", err)
	}
	changes, err = svc.ListChangeSets(ctx, actor, after.ID)
	if err != nil {
		t.Fatalf("list the change sets again: %v", err)
	}
	inverse = 0
	for _, candidate := range changes {
		if candidate.ReversesChangeSetID == source.ID {
			inverse++
		}
	}
	if inverse != 1 {
		t.Fatalf("the successful rollback did not produce exactly one inverse: %d", inverse)
	}
}
