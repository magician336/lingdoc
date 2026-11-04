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
