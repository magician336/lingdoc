package evidence

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

// t09ReportPath 是 T09 验证报告的落点，供交接记录（T09-项目资料与来源.md §7）引用。
// 与 T03 同规矩：报告由测试生成，能重跑；散文不能。
const t09ReportPath = "../../docs/08-本轮实施方案/T09-验证报告.json"

// TestT09VerificationRun 把 T09 的验收各跑一遍并落成报告。
//
// 与 T03 那次的关键差别：绑定存储、迁移出来的表、原文读取都是**真实实现**
// （真 SQLite、真 DDL、真文件），替身只剩两个外部依赖——知识库权限判定
// （T07 未合入）与底座读取端口。
//
// 资料服务的验收不涉及模型，所以 declared 取 mode 阶梯里最保守的一档：
// 报低不报高（契约 §9）。Note 里逐条写明哪部分是真的。
func TestT09VerificationRun(t *testing.T) {
	ctx := context.Background()
	actor := Actor{UserID: "u-verifier", TenantID: "7"}
	store := newTestStore(t)

	rep := NewReport(ModeMock)
	rep.Note = "绑定存储与表结构为真实实现（SQLite 实库 + migrations/sqlite/000019 的真实 DDL）；" +
		"资料版本、允许集合、授权判定为真实领域实现；原文读取为真实实现（读合成资料文件）；" +
		"替身只有两处：知识库权限判定（T07/PR#4 未合入，资料服务按 02-接口与Mock约定 §2 只消费不自建）" +
		"与底座读取端口 KnowledgeReader。HTTP 薄适配（S4）与界面切片（S5）未交付，逐条标 not_run。" +
		"资料服务不涉及模型，故 mode 取阶梯里最保守的一档。"

	record := func(id string, run func() (CheckStatus, string)) {
		status, detail := run()
		rep.Add(Check{ID: id, Status: status, Detail: detail})
	}

	bind := func(projectID, knowledgeID, kbID string, mutate func(*KnowledgeSignal)) Asset {
		in := bindInputKB(projectID, knowledgeID, kbID, func(in *BindInput) {
			in.Signal = signal(mutate)
		})
		asset, err := store.Bind(ctx, in)
		if err != nil {
			t.Fatalf("Bind(%s/%s): %v", projectID, knowledgeID, err)
		}
		return asset
	}

	ready := bind("p-1", "k-demo", "kb-ok", nil)
	busy := bind("p-1", "k-busy", "kb-ok", func(s *KnowledgeSignal) {
		s.ParseStatus = types.ParseStatusProcessing
	})
	denied := bind("p-1", "k-denied", "kb-noauth", nil)
	otherProject := bind("p-2", "k-other", "kb-ok", nil)

	kb := &fakeKBRead{allowed: map[string]bool{"kb-ok": true, "kb-noauth": false}}
	gateway := NewAssetGateway(store, NewFixedAuthorizer(store, kb))

	// 任务池验收①：绑定 ready 资料。F03 第二步说明未就绪资料也能绑定（201 + processing），
	// 就绪与否在使用时拦——所以这里同时钉住「绑得上」与「绑成什么状态」。
	record("T09-绑定资料与就绪状态（F03 第一步）", func() (CheckStatus, string) {
		if ready.ProcessingState != AssetStateReady {
			return CheckFailed, fmt.Sprintf("已解析资料的 processing_state = %q, want ready", ready.ProcessingState)
		}
		if busy.ProcessingState != AssetStateProcessing {
			return CheckFailed, fmt.Sprintf("未就绪资料的 processing_state = %q, want processing（绑定不得被就绪度拦住）",
				busy.ProcessingState)
		}
		if ready.AssetRevision != 1 || busy.AssetRevision != 1 {
			return CheckFailed, fmt.Sprintf("首次绑定 revision = %d/%d, want 1/1",
				ready.AssetRevision, busy.AssetRevision)
		}
		return CheckPassed, ""
	})

	// 任务池验收②：只交出「已绑定、已就绪、此刻仍被授权」的那一批。
	record("T09-只允许已绑定且已就绪的资料（F03 第二步）", func() (CheckStatus, string) {
		res, err := gateway.ResolveAllowed(ctx, "p-1", actor, []string{ready.ID, busy.ID})
		if err != nil {
			return CheckFailed, fmt.Sprintf("ResolveAllowed 返回错误: %v", err)
		}
		if len(res.Allowed) != 1 || res.Allowed[0].ID != ready.ID {
			return CheckFailed, fmt.Sprintf("Allowed = %v, want 仅就绪的那一份", assetIDs(res.Allowed))
		}
		if len(res.Denied) != 1 || res.Denied[0].Reason != DenyNotReady {
			return CheckFailed, fmt.Sprintf("未就绪资料的拒绝原因 = %+v, want not_ready", res.Denied)
		}
		return CheckPassed, ""
	})

	// 任务池验收④：核对请求 ID 集合与实际返回 ID 集合。F22 的反面就是这条：
	// 一小部分没有授权，整体就必须逐项说清，而不是只报成功的子集。
	record("T09-请求集合与返回集合逐项相等（F22）", func() (CheckStatus, string) {
		requested := []string{ready.ID, denied.ID, busy.ID, "a-missing", ready.ID}
		res, err := gateway.ResolveAllowed(ctx, "p-1", actor, requested)
		if err != nil {
			return CheckFailed, fmt.Sprintf("ResolveAllowed 返回错误: %v", err)
		}
		if len(res.Requested) != 4 {
			return CheckFailed, fmt.Sprintf("Requested 去重后 = %d 项, want 4", len(res.Requested))
		}
		if len(res.Allowed) != 1 || res.Allowed[0].ID != ready.ID {
			return CheckFailed, fmt.Sprintf("Allowed = %v, want 仅 %s", assetIDs(res.Allowed), ready.ID)
		}
		want := map[string]DenyReason{
			denied.ID:   DenyNotAuthorized,
			busy.ID:     DenyNotReady,
			"a-missing": DenyNotFound,
		}
		if len(res.Denied) != len(want) {
			return CheckFailed, fmt.Sprintf("Denied = %d 项, want %d——缺项就是把部分授权当成了全部成功",
				len(res.Denied), len(want))
		}
		for _, d := range res.Denied {
			if want[d.AssetID] != d.Reason {
				return CheckFailed, fmt.Sprintf("%s 的拒绝原因 = %q, want %q", d.AssetID, d.Reason, want[d.AssetID])
			}
		}
		seen := map[string]bool{}
		for _, a := range res.Allowed {
			seen[a.ID] = true
		}
		for _, d := range res.Denied {
			seen[d.AssetID] = true
		}
		for _, id := range res.Requested {
			if !seen[id] {
				return CheckFailed, fmt.Sprintf("%s 既没允许也没拒绝", id)
			}
		}
		return CheckPassed, ""
	})

	// 任务池验收：拒绝错误项目。别的项目的资料 ID 与不存在的 ID 必须收敛成
	// 同一个原因，否则「403 还是 404」就把别的项目里有什么泄露出去了。
	record("T09-跨项目 ID 与不存在的 ID 收敛 not_found", func() (CheckStatus, string) {
		res, err := gateway.ResolveAllowed(ctx, "p-1", actor, []string{otherProject.ID})
		if err != nil {
			return CheckFailed, fmt.Sprintf("ResolveAllowed 返回错误: %v", err)
		}
		if len(res.Allowed) != 0 {
			return CheckFailed, "别的项目的资料进了本项目允许集合"
		}
		if len(res.Denied) != 1 || res.Denied[0].Reason != DenyNotFound {
			return CheckFailed, fmt.Sprintf("拒绝原因 = %+v, want not_found", res.Denied)
		}
		return CheckPassed, ""
	})

	// 任务池验收：当前授权检查。F07 的撤权必须**当场**生效：不许用上一次的结论。
	record("T09-撤权后现查即拒（F07）", func() (CheckStatus, string) {
		revoking := &fakeKBRead{
			allowed:  map[string]bool{"kb-ok": true},
			revokeAt: map[string]int{"kb-ok": 2},
		}
		live := NewAssetGateway(store, NewFixedAuthorizer(store, revoking))

		before, err := live.ResolveAllowed(ctx, "p-1", actor, []string{ready.ID})
		if err != nil {
			return CheckFailed, fmt.Sprintf("撤权前: %v", err)
		}
		if len(before.Allowed) != 1 {
			return CheckFailed, fmt.Sprintf("撤权前的允许集合 = %v, want 1 项", assetIDs(before.Allowed))
		}
		after, err := live.ResolveAllowed(ctx, "p-1", actor, []string{ready.ID})
		if err != nil {
			return CheckFailed, fmt.Sprintf("撤权后: %v", err)
		}
		if len(after.Allowed) != 0 {
			return CheckFailed, "撤权后仍放行——授权判定被缓存了"
		}
		if len(after.Denied) != 1 || after.Denied[0].Reason != DenyNotAuthorized {
			return CheckFailed, fmt.Sprintf("撤权后的拒绝原因 = %+v, want not_authorized（HTTP 上映射 403）", after.Denied)
		}
		if revoking.calls < 2 {
			return CheckFailed, fmt.Sprintf("权限判定只被问了 %d 次——两次请求之间没有现查", revoking.calls)
		}
		return CheckPassed, ""
	})

	// 契约 §10 点名给 T09 的那条：资料版本的可信取得方式。判据是「内容或解析定位
	// 改变才变」（契约 §4），不是每次观测都动。
	record("T09-资料版本：内容变了才递增", func() (CheckStatus, string) {
		same, err := store.ObserveAsset(ctx, "p-1", ready.ID, signal(func(s *KnowledgeSignal) {
			s.KnowledgeID = "k-ready"
		}))
		if err != nil {
			return CheckFailed, fmt.Sprintf("ObserveAsset(同内容): %v", err)
		}
		if same.Changed || same.Next.Revision != 1 {
			return CheckFailed, fmt.Sprintf("同内容观测改了版本: %+v", same)
		}

		changed, err := store.ObserveAsset(ctx, "p-1", ready.ID, signal(func(s *KnowledgeSignal) {
			s.KnowledgeID = "k-ready"
			s.FileHash = "hash-v2"
		}))
		if err != nil {
			return CheckFailed, fmt.Sprintf("ObserveAsset(内容变化): %v", err)
		}
		if !changed.Changed || changed.Next.Revision != 2 {
			return CheckFailed, fmt.Sprintf("内容变化未递增版本: %+v", changed)
		}
		if changed.Reason == "" {
			return CheckFailed, "递增没有记下原因——人工复核时看不出为什么变了"
		}

		// 递增必须落库，且旧修订要留痕：冻结与确认要能回指「当时那一版」。
		stored, err := store.BoundAssets(ctx, "p-1")
		if err != nil {
			return CheckFailed, fmt.Sprintf("BoundAssets: %v", err)
		}
		for _, a := range stored {
			if a.ID == ready.ID && a.AssetRevision != 2 {
				return CheckFailed, fmt.Sprintf("落库后的 revision = %d, want 2", a.AssetRevision)
			}
		}
		revisions, err := store.Revisions(ctx, ready.ID)
		if err != nil {
			return CheckFailed, fmt.Sprintf("Revisions: %v", err)
		}
		if len(revisions) != 2 {
			return CheckFailed, fmt.Sprintf("修订行数 = %d, want 2", len(revisions))
		}
		if revisions[0].Status != RevisionSuperseded || revisions[1].Status != RevisionActive {
			return CheckFailed, fmt.Sprintf("修订状态 = %q/%q, want superseded/active",
				revisions[0].Status, revisions[1].Status)
		}
		return CheckPassed, ""
	})

	// 任务池验收③：稳定定位。这里比 T03 那次更进一步——原文由**真实读取实现**
	// 从文件里取回，不再是内存里的替身文本。
	record("T09-定位可回到原文（真实原文读取 + 合成资料）", func() (CheckStatus, string) {
		origin := syntheticOrigin(t)
		path := filepath.Join(t.TempDir(), "synthetic.txt")
		// 写成 LF：合成资料本身是 LF，但 Windows 检出可能把它变成 CRLF，
		// 而坐标是按 rune 写死的。落一份确定的字节，判据才只取决于实现。
		if err := os.WriteFile(path, []byte(origin), 0o600); err != nil {
			return CheckFailed, fmt.Sprintf("写合成资料: %v", err)
		}
		reader := NewOriginReader(&fakeKnowledge{rows: map[string]*types.Knowledge{
			"k-demo": {ID: "k-demo", FilePath: path, FileType: "txt", ParseStatus: types.ParseStatusCompleted},
		}})

		text, ok, err := reader.OriginText(ctx, "k-demo")
		if err != nil {
			return CheckFailed, fmt.Sprintf("OriginText: %v", err)
		}
		if !ok {
			return CheckFailed, "纯文本合成资料应当能直读（强档）"
		}
		if text != origin {
			return CheckFailed, "直读回来的正文与文件内容不一致"
		}

		srcs, err := NewSourceResolver(reader).Resolve(ctx, ready, []*types.SearchResult{goalHit()})
		if err != nil {
			return CheckFailed, fmt.Sprintf("Resolve: %v", err)
		}
		if len(srcs) != 1 || srcs[0].Status != SourceAvailable {
			return CheckFailed, fmt.Sprintf("来源状态 = %+v, want 1 条 available", srcs)
		}
		if srcs[0].QuotedText != goalText {
			return CheckFailed, fmt.Sprintf("引文 = %q, want %q", srcs[0].QuotedText, goalText)
		}
		sum := sha256.Sum256([]byte(srcs[0].QuotedText))
		if srcs[0].QuotedTextHash != hex.EncodeToString(sum[:]) {
			return CheckFailed, "QuotedTextHash 与 sha256(引文) 不符"
		}
		// 来源带的版本必须就是「产出它时用的那份资料」的版本。这一条要能失败，
		// 就不能拿同一个对象自我比对：库里此刻是第 2 版（上一条检查刚推进过），
		// 而手上这份 ready 还是第 1 版。于是它钉住两件事——
		// 版本如实带上，且 Resolve **不会偷偷回库刷新**（否则「这段引文出自
		// 哪一版」就成了一个无法复核的说法）。
		stored, err := store.BoundAssets(ctx, "p-1")
		if err != nil {
			return CheckFailed, fmt.Sprintf("BoundAssets: %v", err)
		}
		var latest Asset
		for _, a := range stored {
			if a.ID == ready.ID {
				latest = a
			}
		}
		if latest.AssetRevision != 2 {
			return CheckFailed, fmt.Sprintf("库里的版本 = %d, want 2（前一条检查应当已经落库）",
				latest.AssetRevision)
		}
		if ready.AssetRevision != 1 {
			return CheckFailed, fmt.Sprintf("手上那份资料的版本 = %d, want 1（它没有被回读）",
				ready.AssetRevision)
		}
		if srcs[0].AssetRevision != 1 {
			return CheckFailed, fmt.Sprintf("来源版本 = %d, want 1——Resolve 回库刷新了，来源的版本就不代表它产出时的资料",
				srcs[0].AssetRevision)
		}
		fresh, err := NewSourceResolver(reader).Resolve(ctx, latest, []*types.SearchResult{goalHit()})
		if err != nil {
			return CheckFailed, fmt.Sprintf("Resolve(当前版本): %v", err)
		}
		if len(fresh) != 1 || fresh[0].AssetRevision != 2 {
			return CheckFailed, fmt.Sprintf("用第 2 版解析出的来源版本 = %+v, want 2", fresh)
		}
		return CheckPassed, ""
	})

	// 取不到原文时必须老实降弱档。这条不是「顺手测一下」：它是 §3.2 声明的
	// 诚实上限的可执行形式——真实 PDF/DOCX 上「指回原文」只有弱档。
	record("T09-拿不到原文时降弱档（如实声明上限）", func() (CheckStatus, string) {
		reader := NewOriginReader(&fakeKnowledge{rows: map[string]*types.Knowledge{
			"k-demo": {ID: "k-demo", FilePath: "whatever.pdf", FileType: "pdf"},
		}})
		if _, ok, err := reader.OriginText(ctx, "k-demo"); err != nil || ok {
			return CheckFailed, fmt.Sprintf("PDF 被当成了可直读：ok=%v err=%v", ok, err)
		}

		srcs, err := NewSourceResolver(reader).Resolve(ctx, ready, []*types.SearchResult{goalHit()})
		if err != nil {
			return CheckFailed, fmt.Sprintf("Resolve: %v", err)
		}
		// 弱档只能靠坐标自洽：自洽则仍可用，但这一档的强度是声明过的。
		if len(srcs) != 1 || srcs[0].Status != SourceAvailable {
			return CheckFailed, fmt.Sprintf("坐标自洽的命中在弱档下应当仍可用，得到 %+v", srcs)
		}
		return CheckPassed, ""
	})

	record("T09-S4-HTTP 薄适配与幂等（未交付）", func() (CheckStatus, string) {
		return CheckNotRun, "未实现。原因见 T09-项目资料与来源.md §6.1/§6.2：" +
			"契约 evidence 域四操作的挂载点（internal/router/router.go）是共用入口文件，" +
			"与未合入的 PR#4 冲突；且 PR#4 已实现幂等（lingdoc_operations + workspacecore.operation），" +
			"T09 应复用而不是自己再落一张表。领域接口已就绪，接线即可。" +
			"S4 另有三条别漏（§7「S4 接线时不要漏这几条」）：契约 §2 的 SourcePolicy.Validate 薄封装（本领域未提供）；" +
			"F02 的「空范围」400 要在 HTTP 层按 minItems:1 造（领域层对空集合返回成功空集是对的）；" +
			"KnowledgeReader.UsesBuiltinConverter 必须实现，否则文件类强档全程降档"
	})

	record("T09-S5-界面切片（未交付）", func() (CheckStatus, string) {
		return CheckNotRun, "未实现。原因见 T09-项目资料与来源.md §6.1：前端底座" +
			"（frontend/src/api/lingdoc/、views/lingdoc/）由未合入的 PR#4 引入，本分支挂不上"
	})

	if problems := rep.Problems(); len(problems) > 0 {
		t.Fatalf("报告记录有缺陷: %v", problems)
	}
	if failed := rep.Failed(); len(failed) > 0 {
		t.Errorf("有 %d 条检查失败: %+v", len(failed), failed)
	}

	raw, err := json.MarshalIndent(rep, "", "  ")
	if err != nil {
		t.Fatalf("序列化报告: %v", err)
	}
	if err := os.WriteFile(filepath.Clean(t09ReportPath), append(raw, '\n'), 0o644); err != nil {
		t.Fatalf("写报告 %s: %v", t09ReportPath, err)
	}
	t.Logf("报告已写入 %s：mode=%s，通过 %d、失败 %d、未运行 %d",
		t09ReportPath, rep.EffectiveMode(), len(rep.Passed()), len(rep.Failed()), len(rep.NotRun()))
}
