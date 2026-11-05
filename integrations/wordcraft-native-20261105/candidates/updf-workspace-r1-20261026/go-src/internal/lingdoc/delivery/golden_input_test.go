package delivery

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 契约把冻结输入的规范形式与摘要成对发布（contracts/frozen-input.canonical.json
// 与 .sha256），prepareRelease 样例里的 snapshot_digest 就是这一枚。摘要必须能被
// 重算出来，否则发布的那对文件只是装饰：消费者拿到快照无从核对，
// 而「同一份输入永远得到同一个摘要」正是冻结的意义。
func publishedFrozenInput(t *testing.T) (DeliveryInput, string) {
	t.Helper()
	dir := filepath.Join("..", "..", "..", "docs", "08-本轮实施方案", "contracts")
	raw, err := os.ReadFile(filepath.Join(dir, "frozen-input.canonical.json"))
	if err != nil {
		t.Fatalf("read published frozen input: %v", err)
	}
	want, err := os.ReadFile(filepath.Join(dir, "frozen-input.sha256"))
	if err != nil {
		t.Fatalf("read published digest: %v", err)
	}
	var input DeliveryInput
	if err := json.Unmarshal(raw, &input); err != nil {
		t.Fatalf("decode published frozen input: %v", err)
	}
	return input, strings.TrimSpace(string(want))
}

func TestFrozenInputDigestMatchesThePublishedArtifact(t *testing.T) {
	input, want := publishedFrozenInput(t)
	got, err := digestFrozenInput(input)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("digest = %s\n     want the published %s", got, want)
	}
}

// 冻结输入里的 template 必须是契约发布的那一份，而它的要害是**带 rules**。
// 工作区自己那份 workspacecore.Template 没有 Rules，拿它来冻结，T13 找不到规则声明，
// 会把每一次检查都判成 not_evaluated——一次没跑过的检查被报成「没跑」，
// 而不是「通过」，这是对的方向；但那时冻结出来的东西已经不是这份契约的输入了。
func TestDemoTemplateIsThePublishedTemplate(t *testing.T) {
	input, _ := publishedFrozenInput(t)
	got, err := canonicalJSON(DemoTemplate())
	if err != nil {
		t.Fatal(err)
	}
	want, err := canonicalJSON(input.Template)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("demo template = %s\n           want %s", got, want)
	}
}

// 摘要必须只取决于内容，不取决于 Go 结构体的字段声明顺序——那是一条实现细节，
// 而契约发布的是字节。同一份输入换一次结构体排版就换一枚摘要的话，
// 快照摘要就不再可比。
func TestFrozenInputDigestIsStableAcrossFieldOrdering(t *testing.T) {
	input, want := publishedFrozenInput(t)
	first, err := digestFrozenInput(input)
	if err != nil {
		t.Fatal(err)
	}
	// 重新走一遍 JSON 往返：字段顺序与内存布局无关，摘要不该变。
	raw, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	var roundTrip DeliveryInput
	if err := json.Unmarshal(raw, &roundTrip); err != nil {
		t.Fatal(err)
	}
	second, err := digestFrozenInput(roundTrip)
	if err != nil {
		t.Fatal(err)
	}
	if first != want || second != want {
		t.Fatalf("digests = %s / %s, want %s", first, second, want)
	}
}
