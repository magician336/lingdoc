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

// reportPath 验证报告的落点，供交接记录（T03-检索与生成验证.md §7）引用。
// 报告由测试生成而非手写：报告能重跑，散文不能。
const reportPath = "../../docs/08-本轮实施方案/T03-验证报告.json"

// s7EvidencePath 真实回路落下的证据，由 cli/acceptance/e2e 的 TestRAGFullLoop
// 在 WEKNORA_E2E_EVIDENCE 指向此处时写出。
//
// S7 读它，而不是直接写 CheckPassed：没跑过就没有文件，结论只能是 not_run。
const s7EvidencePath = "../../docs/08-本轮实施方案/T03-S7-证据.json"

// TestT03VerificationRun 把 T03 的验收各跑一遍并落成报告。
//
// 跑的是领域代码本身——真实的 rune 坐标运算、真实切分、真实失效判定；
// 替身只有绑定存储与权限判定这两个外部依赖，原文用合成文本。
// 唯一走真实回路的是 S7，它读 e2e 落下的证据文件；证据不在时如实标 not_run，
// 且契约 §9 下整体 mode 不得因此升级。
func TestT03VerificationRun(t *testing.T) {
	ctx := context.Background()
	origin := syntheticOrigin(t)
	actor := Actor{UserID: "u-verifier", TenantID: "t-demo"}

	// declared 是本次运行打算达到的保真度上限。S7 真的跑过真实模型，所以上限是
	// real；EffectiveMode 只在 RequiresModel 的检查确实 passed 时才让它成立，
	// 因此这一句本身不构成结论。
	rep := NewReport(ModeReal)
	rep.Note = "F22/F02/S4/S5：领域代码为真实实现（坐标运算 / rune 切分 / 失效判定），" +
		"绑定存储与权限判定为测试替身，原文为合成文本 testdata/synthetic-asset.txt；" +
		"S7：真实回路（真实服务 + 真实 embedding/chat 模型），" +
		"运行记录见 T03-S7-证据.json（含运行时间、模型名、回答字数与引用数）。"

	record := func(id string, requiresModel bool, run func() (CheckStatus, string)) {
		status, detail := run()
		rep.Add(Check{ID: id, Status: status, RequiresModel: requiresModel, Detail: detail})
	}

	// 允许集合：a-ok 允许、a-noauth 未授权、a-busy 未就绪，另请求一个不存在的 a-missing。
	record("F22-部分授权不得静默当全部成功", false, func() (CheckStatus, string) {
		bindings := &fakeBindings{assets: []Asset{
			testAsset("a-ok", AssetStateReady),
			testAsset("a-noauth", AssetStateReady),
			testAsset("a-busy", AssetStateProcessing),
		}}
		authz := &fakeAuthorizer{denied: map[string]bool{"a-noauth": true}}
		res, err := NewAssetGateway(bindings, authz).ResolveAllowed(
			ctx, "p-demo", actor, []string{"a-ok", "a-noauth", "a-busy", "a-missing"})
		if err != nil {
			return CheckFailed, fmt.Sprintf("ResolveAllowed 返回错误: %v", err)
		}
		if len(res.Requested) != 4 {
			return CheckFailed, fmt.Sprintf("Requested 回显 %d 项, want 4", len(res.Requested))
		}
		if len(res.Allowed) != 1 || res.Allowed[0].ID != "a-ok" {
			return CheckFailed, fmt.Sprintf("Allowed = %v, want 仅 a-ok", assetIDs(res.Allowed))
		}
		want := map[string]DenyReason{
			"a-noauth":  DenyNotAuthorized,
			"a-busy":    DenyNotReady,
			"a-missing": DenyNotFound,
		}
		if len(res.Denied) != 3 {
			return CheckFailed, fmt.Sprintf(
				"Denied 只有 %d 项, want 3——缺项就是把部分授权当成了全部成功", len(res.Denied))
		}
		for _, d := range res.Denied {
			if want[d.AssetID] != d.Reason {
				return CheckFailed, fmt.Sprintf("%s 的拒绝原因 = %q, want %q", d.AssetID, d.Reason, want[d.AssetID])
			}
		}
		return CheckPassed, ""
	})

	record("F02-空范围不得扩大为全库", false, func() (CheckStatus, string) {
		bindings := &fakeBindings{assets: []Asset{testAsset("a-ok", AssetStateReady)}}
		res, err := NewAssetGateway(bindings, &fakeAuthorizer{}).ResolveAllowed(ctx, "p-demo", actor, nil)
		if err != nil {
			return CheckFailed, fmt.Sprintf("ResolveAllowed 返回错误: %v", err)
		}
		if len(res.Allowed) != 0 || len(res.Denied) != 0 {
			return CheckFailed, fmt.Sprintf("空请求返回了 %d 项资料", len(res.Allowed))
		}
		if bindings.calls != 0 {
			return CheckFailed, fmt.Sprintf(
				"空请求仍触达绑定存储 %d 次——这正是把「没有资料」当成「全部资料」的路", bindings.calls)
		}
		return CheckPassed, ""
	})

	record("S4-来源能指回原文（强档）", false, func() (CheckStatus, string) {
		srcs, err := NewSourceResolver(fakeOrigin{text: origin, ok: true}).Resolve(
			ctx, testAsset("a-ok", AssetStateReady), []*types.SearchResult{goalHit()})
		if err != nil {
			return CheckFailed, fmt.Sprintf("Resolve 返回错误: %v", err)
		}
		if len(srcs) != 1 {
			return CheckFailed, fmt.Sprintf("Resolve 返回 %d 条, want 1", len(srcs))
		}
		if srcs[0].Status != SourceAvailable {
			return CheckFailed, fmt.Sprintf("Status = %q, want %q", srcs[0].Status, SourceAvailable)
		}
		// 判据本身：坐标取回的那一段逐字等于引文。
		runes := []rune(origin)
		if got := string(runes[goalStart:goalEnd]); got != srcs[0].QuotedText {
			return CheckFailed, fmt.Sprintf("原文[%d:%d] = %q, 引文 = %q", goalStart, goalEnd, got, srcs[0].QuotedText)
		}
		sum := sha256.Sum256([]byte(srcs[0].QuotedText))
		if srcs[0].QuotedTextHash != hex.EncodeToString(sum[:]) {
			return CheckFailed, "QuotedTextHash 与 sha256(utf8(QuotedText)) 不符"
		}
		return CheckPassed, ""
	})

	record("S5-失效命中不得报 available", false, func() (CheckStatus, string) {
		hit := goalHit()
		hit.ContentRewritten = true
		srcs, err := NewSourceResolver(fakeOrigin{text: origin, ok: true}).Resolve(
			ctx, testAsset("a-ok", AssetStateReady), []*types.SearchResult{hit})
		if err != nil {
			return CheckFailed, fmt.Sprintf("Resolve 返回错误: %v", err)
		}
		if srcs[0].Status == SourceAvailable {
			return CheckFailed, "被标过重写的命中仍报 available"
		}
		if srcs[0].QuotedText != "" {
			return CheckFailed, fmt.Sprintf("失效来源仍带引文 %q", srcs[0].QuotedText)
		}
		if srcs[0].Anchor.StartAt != goalStart || srcs[0].Anchor.EndAt != goalEnd {
			return CheckFailed, "失效来源丢了可重定位坐标"
		}
		return CheckPassed, ""
	})

	// 唯一需要真实模型的一条。判据不在这里发明：读 e2e 那次真实运行的记录。
	// 没跑过就没有文件，只能是 not_run；跑了但记录不满足判据，是 failed。
	record("S7-一次真实模型输出", true, func() (CheckStatus, string) {
		raw, err := os.ReadFile(filepath.Clean(s7EvidencePath))
		if err != nil {
			return CheckNotRun, fmt.Sprintf(
				"没有真实回路的运行记录（读 %s 失败：%v）。真实回路带 acceptance_e2e 构建标记，"+
					"须单独运行 go test -tags acceptance_e2e ./cli/acceptance/e2e/，"+
					"并让 WEKNORA_E2E_EVIDENCE 指向该文件；不在 make test 内",
				s7EvidencePath, err)
		}
		var ev struct {
			RunAt          string `json:"run_at"`
			ChatModel      string `json:"chat_model"`
			EmbeddingModel string `json:"embedding_model"`
			SearchHits     int    `json:"search_hits"`
			AnswerChars    int    `json:"answer_chars"`
			ReferenceCount int    `json:"reference_count"`
		}
		if err := json.Unmarshal(raw, &ev); err != nil {
			return CheckFailed, fmt.Sprintf("真实回路证据 %s 不是合法 JSON：%v", s7EvidencePath, err)
		}
		// 判据逐条对着 T03 验收标准，而不是「测试跑绿了」：
		// 先要这份记录认得出是哪一次真实运行，再要模型真的产出了回答，
		// 最后要回答指得回原文——引用为 0 就是「答了但回溯不了」，不算通过。
		switch {
		case ev.RunAt == "" || ev.ChatModel == "" || ev.EmbeddingModel == "":
			return CheckFailed, fmt.Sprintf(
				"证据 %s 没记全运行时间或模型名——无法确认这是真实模型而非替身跑出来的",
				s7EvidencePath)
		case ev.AnswerChars <= 0:
			return CheckFailed, fmt.Sprintf(
				"证据 %s 记了 %d 字回答，不构成一次真实模型输出", s7EvidencePath, ev.AnswerChars)
		case ev.SearchHits <= 0:
			return CheckFailed, "证据记了 0 条检索命中——回答不是来自资料"
		case ev.ReferenceCount <= 0:
			return CheckFailed, "证据记了 0 处引用——回答指不回原文（T03 验收①）"
		}
		return CheckPassed, ""
	})

	if problems := rep.Problems(); len(problems) > 0 {
		t.Fatalf("报告记录有缺陷: %v", problems)
	}
	if failed := rep.Failed(); len(failed) > 0 {
		t.Errorf("有 %d 条检查失败: %+v", len(failed), failed)
	}
	// mode 报 real 必须有落地证据。判据写在闭包里等于让闭包自己给自己作证，
	// 所以这里独立复核一次：报了 real，磁盘上就得有那次真实运行的记录。
	if rep.EffectiveMode() == ModeReal {
		if _, err := os.Stat(filepath.Clean(s7EvidencePath)); err != nil {
			t.Errorf("mode 报了 real，但真实回路的证据文件不在：%v", err)
		}
	}

	raw, err := json.MarshalIndent(rep, "", "  ")
	if err != nil {
		t.Fatalf("序列化报告: %v", err)
	}
	if err := os.WriteFile(filepath.Clean(reportPath), append(raw, '\n'), 0o644); err != nil {
		t.Fatalf("写报告 %s: %v", reportPath, err)
	}
	t.Logf("报告已写入 %s：mode=%s，通过 %d、失败 %d、未运行 %d",
		reportPath, rep.EffectiveMode(), len(rep.Passed()), len(rep.Failed()), len(rep.NotRun()))
}

func assetIDs(assets []Asset) []string {
	out := make([]string, 0, len(assets))
	for _, a := range assets {
		out = append(out, a.ID)
	}
	return out
}
