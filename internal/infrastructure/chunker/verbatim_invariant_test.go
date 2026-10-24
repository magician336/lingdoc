package chunker

import (
	"fmt"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// Content must be the verbatim slice of the document between Start and End.
//
// This is the invariant the whole coordinate layer rests on: the source
// re-check reads the origin text at [start_at, end_at) and compares it with
// the quoted text of the chunk. When the content carries something the
// document does not have at that position (a synthesized table header, for
// example), the comparison fails and every source of that material is judged
// stale — the material looks broken for reasons that have nothing to do with
// the material itself (#485 / D1).
//
// The table header is the case that broke it: it was prepended to the
// *content* while the coordinates kept pointing at the body, so the content
// was longer than the coordinate span.
// ---------------------------------------------------------------------------

// verbatimDocument is a Markdown document with a table long enough to be
// split across chunks (the header has to be carried into the following
// chunk for the case to trigger at all).
const verbatimDocument = `# 合成材料研究记录

## 1. 研究目的

本记录评估合成样本的离子电导率及其在合成电池对中的界面阻抗演化，并登记每一处结论的来源，供下游章节生成时按来源引用。

## 2. 材料与方法

### 2.1 样本与装配

本轮共评估 2 个批次，编号与复合相含量如下表所示。

| 批次编号 | 样本编号 | 复合相含量（质量分数） | 膜厚（μm） | 装配方式 |
| --- | --- | --- | --- | --- |
| L-12 | SB-4472-L12 | 55.0% | 120 | 合成电池对 CR-9 |
| L-16 | SB-4472-L16 | 62.0% | 120 | 合成电池对 CR-9 |

两个批次使用同一批合成陶瓷粉体，粉体批号 C-2026-031。装配在干燥惰性环境中完成，施加的压力统一为 2.5 MPa，保压 10 min。

### 2.2 测试条件

- 离子电导率：25 °C，交流阻抗法，频率范围 1 Hz–1 MHz，取中高频半圆实轴截距计算。
- 界面阻抗：合成电池对 CR-9，25 °C，循环前后各测一次。
- 循环测试：0.1 C–1.0 C，室温，循环 120 次。

## 3. 结果

批次 L-12 在合成电池对 CR-9 中，初始界面阻抗为 41 Ω，循环 120 次后为 58 Ω，容量保持率为 96.3%，该组数据记为合成来源 [B-R3]。

批次 L-16 在合成电池对 CR-9 中，初始界面阻抗为 37 Ω，循环 120 次后为 63 Ω，容量保持率为 93.8%，该组数据记为合成来源 [B-R4]。

## 附录 B：合成来源登记表

下表登记本记录中每一处可引用结论的来源。所有数值均为合成数据。

| 来源编号 | 所在小节 | 载体 | 可核对量 | 可核对实体名 |
| --- | --- | --- | --- | --- |
| B-R1 | 3.1 | 批次 L-12 离子电导率 | 2.4×10⁻⁴ S/cm；标准差 0.2×10⁻⁴ S/cm | SB-4472 / 批次 L-12 |
| B-R2 | 3.1 | 批次 L-16 离子电导率 | 3.1×10⁻⁴ S/cm；标准差 0.3×10⁻⁴ S/cm | SB-4472 / 批次 L-16 |
`

// assertChunksAreVerbatim checks the invariant on every chunk: the content is
// exactly the document slice between Start and End (rune offsets).
func assertChunksAreVerbatim(t *testing.T, text string, chunks []Chunk) {
	t.Helper()
	runes := []rune(text)
	for i, c := range chunks {
		if c.Start < 0 || c.End > len(runes) || c.End < c.Start {
			t.Fatalf("chunk %d has out-of-range coordinates [%d,%d) for a %d-rune document",
				i, c.Start, c.End, len(runes))
		}
		got := string(runes[c.Start:c.End])
		if got != c.Content {
			t.Fatalf("chunk %d is not the document slice at [%d,%d):\n content: %q\n slice:   %q",
				i, c.Start, c.End, c.Content, got)
		}
		if n := len([]rune(c.Content)); n != c.End-c.Start {
			t.Fatalf("chunk %d content length %d != coordinate span %d", i, n, c.End-c.Start)
		}
	}
}

func TestSplitKeepsContentVerbatimAtItsCoordinates(t *testing.T) {
	for _, chunkSize := range []int{128, 192, 256, 384, 512} {
		t.Run(fmt.Sprintf("chunk_size=%d", chunkSize), func(t *testing.T) {
			chunks := Split(verbatimDocument, SplitterConfig{
				ChunkSize:    chunkSize,
				ChunkOverlap: chunkSize / 5,
				Separators:   []string{"\n\n", "\n", "。"},
			})
			if len(chunks) < 2 {
				t.Fatalf("expected the document to be split, got %d chunk(s)", len(chunks))
			}
			assertChunksAreVerbatim(t, verbatimDocument, chunks)
		})
	}
}

func TestSplitTextKeepsContentVerbatimAtItsCoordinates(t *testing.T) {
	chunks := SplitText(verbatimDocument, SplitterConfig{
		ChunkSize:    200,
		ChunkOverlap: 40,
		Separators:   []string{"\n\n", "\n", "。"},
	})
	if len(chunks) < 2 {
		t.Fatalf("expected the document to be split, got %d chunk(s)", len(chunks))
	}
	assertChunksAreVerbatim(t, verbatimDocument, chunks)
}

// The table header must still reach the embedding input — as context, not as
// content. Dropping it entirely would lose the column names for every chunk
// of the table, which is exactly what the header context exists for.
func TestSplitCarriesTableHeaderAsContextNotContent(t *testing.T) {
	chunks := Split(verbatimDocument, SplitterConfig{
		ChunkSize:    200,
		ChunkOverlap: 40,
		Separators:   []string{"\n\n", "\n", "。"},
	})
	carried := 0
	for _, c := range chunks {
		if strings.Contains(c.ContextHeader, "批次编号") {
			carried++
			if strings.Contains(c.Content, c.ContextHeader) {
				t.Fatalf("the table header must not be part of the content: %q", c.Content)
			}
		}
	}
	if carried == 0 {
		t.Fatalf("no chunk carries the table header as context; the context was lost")
	}
}
