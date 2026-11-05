package rulesetstore

import (
	"context"
	"sync"
	"testing"

	"github.com/Tencent/WeKnora/internal/lingdoc/template"
	"github.com/Tencent/WeKnora/internal/types"
)

// #193 AC2:withdraw/recall 两种安全停并发竞争同一已发布版本时,行锁序列化
// 之下必须恰好一个动作成功——败者拿到 ErrRuleSetNotPublished(发布态只能转出
// 一次),且审计行与终态与「赢者」一致,无部分写入。
//
// 这与 AC2 的并发 bind/migrate(各自有 goroutine 级直测)互补,把状态机竞争
// 的最后一条缝隙(recall 与 withdraw 竞争)也钉住。
func TestStoreConcurrentWithdrawAndRecallLetsExactlyOneWin(t *testing.T) {
	db := openStoreTestDB(t, true)
	store := NewStore(db, testAuthorizer{allow: true})
	ctx := context.Background()
	actor := Actor{TenantID: 1, UserID: "admin-1", Role: string(types.TenantRoleAdmin)}
	draft := testDraft("v-race")

	if err := store.CreateDraft(ctx, actor, draft); err != nil {
		t.Fatal(err)
	}
	published, err := store.Publish(ctx, actor, draft.ID, draft.Version)
	if err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	type outcome struct {
		state template.RuleSetState
		err   error
	}
	results := make([]outcome, 2)
	wg.Add(2)
	go func() {
		defer wg.Done()
		set, err := store.Withdraw(ctx, actor, draft.ID, draft.Version)
		state := template.RuleSetState("")
		if err == nil {
			state = set.State
		}
		results[0] = outcome{state: state, err: err}
	}()
	go func() {
		defer wg.Done()
		set, err := store.Recall(ctx, actor, draft.ID, draft.Version)
		state := template.RuleSetState("")
		if err == nil {
			state = set.State
		}
		results[1] = outcome{state: state, err: err}
	}()
	wg.Wait()

	winners, losers := 0, 0
	for _, result := range results {
		if result.err == nil {
			winners++
			continue
		}
		losers++
	}
	// AC2 的核心:恰好一个动作成功,败者必须失败(SQLite 竞争下败者的错误
	// 形状随锁时序在 ErrRuleSetNotPublished 与 busy 包装之间变化,故只断言
	// 非零错误;形状差异登记在验证报告)。
	if winners != 1 || losers != 1 {
		t.Fatalf("the concurrent withdraw/recall race produced %d winners and %d losers (results=%+v): want exactly one winner", winners, losers, results)
	}

	// 终态与审计与赢者一致:恰好两条转换审计(create/publish 之外 +1),读回
	// 的状态是赢者的状态,历史哈希不变。
	stored, err := store.GetVersion(ctx, actor, draft.ID, draft.Version)
	if err != nil {
		t.Fatal(err)
	}
	winnerState := results[0].state
	if winnerState == template.RuleSetState("") {
		winnerState = results[1].state
	}
	if stored.State != winnerState {
		t.Fatalf("stored state = %q, want the winner's %q", stored.State, winnerState)
	}
	if stored.RulesetHash != published.RulesetHash {
		t.Fatalf("the race changed the historical binding hash: %+v", stored)
	}
	var audits int64
	if err := db.Table("audit_logs").Where("tenant_id = ? AND scope_type = ? AND scope_id = ?", actor.TenantID, "lingdoc_ruleset", draft.ID).Count(&audits).Error; err != nil {
		t.Fatal(err)
	}
	if audits != 3 {
		t.Fatalf("audit records = %d, want create+publish+winner only", audits)
	}
}
