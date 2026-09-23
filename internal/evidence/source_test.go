package evidence

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

// 合成原文 testdata/synthetic-asset.txt 共 167 runes / 475 bytes。
// 下列坐标是一次性算准后写死的字面量，不在运行时推导——否则
// "原文[start:end] == QuotedText" 会变成同义反复，测不出 rune/byte 混淆。
const (
	goalStart, goalEnd = 28, 52
	goalText           = "本课题拟研究合成材料在常温条件下的稳定性变化规律"
	totalRunes         = 167
)

type fakeOrigin struct {
	text string
	ok   bool
	err  error
}

func (f fakeOrigin) OriginText(_ context.Context, _ string) (string, bool, error) {
	return f.text, f.ok, f.err
}

func syntheticOrigin(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "synthetic-asset.txt"))
	if err != nil {
		t.Fatalf("读取合成资料: %v", err)
	}
	return string(b)
}

func goalHit() *types.SearchResult {
	return &types.SearchResult{
		ID:          "c-1",
		KnowledgeID: "k-demo",
		ChunkIndex:  2,
		Content:     goalText,
		StartAt:     goalStart,
		EndAt:       goalEnd,
	}
}

// 强档正例：原文可取回时，QuotedText 必须是按 rune 坐标取回的那一段。
//
// 按字节切的实现会在这里失败——它取回的是 24 字节（约 8 个汉字），
// 与 hit.Content 不等，于是误判为 stale。这正是本测试要抓的错。
func TestResolveQuotesOriginByRuneOffsets(t *testing.T) {
	origin := syntheticOrigin(t)
	if got := len([]rune(origin)); got != totalRunes {
		t.Fatalf("合成原文 rune 数 = %d, want %d（fixture 被改过，坐标字面量需重算）", got, totalRunes)
	}

	r := NewSourceResolver(fakeOrigin{text: origin, ok: true})
	got, err := r.Resolve(context.Background(), Asset{ID: "a-1", Title: "合成资料"}, []*types.SearchResult{goalHit()})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("Resolve 返回 %d 条, want 1", len(got))
	}

	src := got[0]
	if src.Status != SourceAvailable {
		t.Errorf("Status = %q, want %q", src.Status, SourceAvailable)
	}
	if src.QuotedText != goalText {
		t.Errorf("QuotedText = %q, want %q", src.QuotedText, goalText)
	}
	sum := sha256.Sum256([]byte(goalText))
	if src.QuotedTextHash != hex.EncodeToString(sum[:]) {
		t.Errorf("QuotedTextHash 与 sha256(utf8(QuotedText)) 不符")
	}
	if src.AssetID != "a-1" {
		t.Errorf("AssetID = %q, want a-1", src.AssetID)
	}
}

// 强档反例：坐标合法但内容已被改写（取回的不是这段）→ 必须判 stale。
//
// 这一条防的是"不读原文、直接把 hit.Content 当 QuotedText 回显"的假实现：
// 那种实现在上面那个正例里会通过，在这里会误报 available。
func TestResolveMarksStaleWhenContentNoLongerMatchesOrigin(t *testing.T) {
	origin := syntheticOrigin(t)
	hit := goalHit()
	hit.Content = "这段正文已被改写，与坐标指向的原文不符"

	r := NewSourceResolver(fakeOrigin{text: origin, ok: true})
	got, err := r.Resolve(context.Background(), Asset{ID: "a-1"}, []*types.SearchResult{hit})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got[0].Status != SourceStale {
		t.Errorf("Status = %q, want %q（坐标取回的正文与命中内容不符）", got[0].Status, SourceStale)
	}
}

// 失效标记优先于强档：即便原文能取回、切片也对得上，被标过重写的命中仍不可采信。
// 术语表 §2 的「默认拒绝」要求标记先于内容判定。
func TestResolveInvalidationFlagBeatsStrongTier(t *testing.T) {
	origin := syntheticOrigin(t)
	hit := goalHit()
	hit.ContentRewritten = true

	r := NewSourceResolver(fakeOrigin{text: origin, ok: true})
	got, err := r.Resolve(context.Background(), Asset{ID: "a-1"}, []*types.SearchResult{hit})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got[0].Status != SourceStale {
		t.Errorf("Status = %q, want %q", got[0].Status, SourceStale)
	}
	if got[0].QuotedText != "" {
		t.Errorf("stale 来源不该带 QuotedText，得到 %q", got[0].QuotedText)
	}
}

// 坐标越界不是 stale 而是 unavailable：这条来源此刻根本取不回来。
func TestResolveMarksUnavailableOnOutOfRangeCoordinates(t *testing.T) {
	origin := syntheticOrigin(t)
	hit := goalHit()
	hit.StartAt, hit.EndAt = totalRunes+10, totalRunes+20

	r := NewSourceResolver(fakeOrigin{text: origin, ok: true})
	got, err := r.Resolve(context.Background(), Asset{ID: "a-1"}, []*types.SearchResult{hit})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got[0].Status != SourceUnavailable {
		t.Errorf("Status = %q, want %q", got[0].Status, SourceUnavailable)
	}
}

// 拿不到原文（PDF/DOCX 等需重解析）时只能给弱档：坐标自洽且未被编辑才 available。
func TestResolveFallsBackToWeakTierWithoutOrigin(t *testing.T) {
	for _, tc := range []struct {
		name string
		mut  func(*types.SearchResult)
		want SourceStatus
	}{
		{"坐标自洽且未被编辑", func(*types.SearchResult) {}, SourceAvailable},
		{"内容被 pipeline 重写过", func(h *types.SearchResult) { h.ContentRewritten = true }, SourceStale},
		{"内容被人工编辑过", func(h *types.SearchResult) { h.ContentRevision = 1 }, SourceStale},
		{"长度与坐标不自洽", func(h *types.SearchResult) { h.EndAt = h.EndAt + 3 }, SourceUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			hit := goalHit()
			tc.mut(hit)

			r := NewSourceResolver(fakeOrigin{ok: false})
			got, err := r.Resolve(context.Background(), Asset{ID: "a-1"}, []*types.SearchResult{hit})
			if err != nil {
				t.Fatalf("Resolve: %v", err)
			}
			if got[0].Status != tc.want {
				t.Errorf("Status = %q, want %q", got[0].Status, tc.want)
			}
		})
	}
}
