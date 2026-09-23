package evidence

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

// reportPath 验证报告的落点，供交接记录（T03-检索与生成验证.md §7）引用。
// 报告由测试生成而非手写：报告能重跑，散文不能。
const reportPath = "../../docs/08-本轮实施方案/T03-验证报告.json"

// TestT03VerificationRun 把 T03 的验收各跑一遍并落成报告。
//
// 跑的是领域代码本身——真实的 rune 坐标运算、真实切分、真实失效判定；
// 替身只有绑定存储与权限判定这两个外部依赖，原文用合成文本。
// 因此 mode 报 mock：契约 §9 下，没有真实模型输出时整体 mode 永远不是 real。
func TestT03VerificationRun(t *testing.T) {
	ctx := context.Background()
	origin := syntheticOrigin(t)
	actor := Actor{UserID: "u-verifier", TenantID: "t-demo"}

	rep := NewReport(ModeMock)
	rep.Note = "领域代码为真实实现（坐标运算 / rune 切分 / 失效判定）；" +
		"绑定存储与权限判定为测试替身；原文为合成文本 testdata/synthetic-asset.txt；" +
		"真实模型回路见 not_run 项。"

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

	// 唯一需要真实模型的一条。没跑成必须如实标 not_run，且不得让整体 mode 升级。
	record("S7-一次真实模型输出", true, func() (CheckStatus, string) {
		var missing []string
		for _, k := range []string{"WEKNORA_E2E_HOST", "WEKNORA_E2E_TOKEN", "WEKNORA_E2E_CHAT_MODEL"} {
			if os.Getenv(k) == "" {
				missing = append(missing, k)
			}
		}
		if len(missing) > 0 {
			return CheckNotRun, fmt.Sprintf(
				"%s 未设置，本轮无模型权限/凭据；真实回路见 cli/acceptance/e2e/e2e_test.go TestRAGFullLoop",
				strings.Join(missing, ", "))
		}
		return CheckNotRun, "凭据已配置，但真实回路带 acceptance_e2e 构建标记，" +
			"须单独运行 go test -tags acceptance_e2e ./cli/acceptance/e2e/，不在 make test 内"
	})

	if problems := rep.Problems(); len(problems) > 0 {
		t.Fatalf("报告记录有缺陷: %v", problems)
	}
	if failed := rep.Failed(); len(failed) > 0 {
		t.Errorf("有 %d 条检查失败: %+v", len(failed), failed)
	}
	if rep.EffectiveMode() == ModeReal {
		t.Error("mode 报了 real，但本轮没有任何真实模型输出")
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
