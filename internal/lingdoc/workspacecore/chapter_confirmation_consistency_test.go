package workspacecore

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The consumers of "is this chapter confirmed" must give the same answer for
// the same chapter.
//
// The delivery preflight reads the chapter view, the stabilization impact
// resolution reads the impact object state, and the rules governance runtime
// asks the same predicate. When they judged the question separately, a chapter
// re-confirmed under a newer research-conditions revision was "confirmed" for
// the delivery preflight and "not confirmed" for the stabilization gate.
//
// This test drives the two workspace-core consumers over the real store and the
// real service seam and asserts they agree — both when the chapter is
// confirmed and when it is not. The third consumer lives in another package
// and is pinned by a source assertion below, so it cannot drift back to a
// second judgment.
func TestConfirmationConsumersAgreeOnTheSameChapter(t *testing.T) {
	svc := impactTaskStore(t, filepath.Join(t.TempDir(), "confirmation-consistency.db"))
	actor := Actor{TenantID: 3202, UserID: "owner"}
	p, _, _ := appliedChangeFixture(t, svc, actor)
	chapter := mustListChapters(t, svc, actor, p.ID)[0]
	if chapter.CurrentVersionID == nil {
		t.Fatal("夹具章节没有当前版本")
	}
	ref := chapterRefVersion(chapter)

	read := func() (bool, bool) {
		delivery := mustListChapters(t, svc, actor, p.ID)[0].ConfirmationValid
		stabilization := false
		err := svc.repository.Transaction(context.Background(), TransactionOptions{ReadOnly: true}, func(tx Transaction) error {
			state, err := currentImpactObject(tx, p, ref)
			if err != nil {
				return err
			}
			stabilization = state.Confirmed
			return nil
		})
		if err != nil {
			t.Fatalf("读影响对象状态失败：%v", err)
		}
		return delivery, stabilization
	}

	delivery, stabilization := read()
	if delivery || stabilization {
		t.Fatalf("未确认的章节被判为已确认：交付侧=%v 稳定化侧=%v", delivery, stabilization)
	}

	fresh := reloadProject(t, svc, actor, p.ID)
	confirmChapterAtSpecRevision(t, svc, actor, p.ID, fresh, mustListChapters(t, svc, actor, p.ID)[0])
	delivery, stabilization = read()
	if !delivery || !stabilization {
		t.Fatalf("已确认的章节被判为未确认：交付侧=%v 稳定化侧=%v", delivery, stabilization)
	}
	if delivery != stabilization {
		t.Fatalf("两处消费者结论不一致：交付侧=%v 稳定化侧=%v", delivery, stabilization)
	}
}

// The rules governance runtime is the third consumer, and it lives in another
// package. It must ask the same predicate rather than judge the question
// again — the whole point of the predicate is that there is one judgment.
//
// This is a source assertion on purpose: the runtime needs a live database and
// a full governance fixture, and what must not change is *which* function
// answers the question, not the wiring around it.
func TestRulesGovernanceRuntimeAsksTheSamePredicate(t *testing.T) {
	path := filepath.Join("..", "workspace", "template_governance_runtime.go")
	source, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读 %s 失败：%v", path, err)
	}
	text := string(source)
	if !strings.Contains(text, "core.ChapterConfirmationOf(") && !strings.Contains(text, "core.ChapterConfirmed(") {
		t.Fatal("规则治理运行时没有问 ChapterConfirmed：它必须与另外两处共用同一个谓词")
	}
	// The lenient check DR-47-01 removed must not come back: an empty chapter
	// id is no longer acceptable.
	if strings.Contains(text, `c.ChapterID == ""`) {
		t.Fatal("规则治理运行时又接受了空的章节标识（DR-47-01 已裁定收紧为相等）")
	}
}
