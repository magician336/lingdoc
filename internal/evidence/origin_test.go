package evidence

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

// fakeKnowledge 是底座的替身。契约：资料不存在返回 (nil, nil)，不是错误。
//
// reroutedTypes 列出「被 KB 规则改派给别的引擎」的文件类型——零值表示
// 没配任何规则，也就是内置 Go 转换器这一档。
type fakeKnowledge struct {
	rows          map[string]*types.Knowledge
	err           error
	calls         int
	engineCalls   int
	reroutedTypes map[string]bool
	engineErr     error
}

func (f *fakeKnowledge) KnowledgeForOrigin(_ context.Context, id string) (*types.Knowledge, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	row, ok := f.rows[id]
	if !ok {
		return nil, nil
	}
	return row, nil
}

// UsesBuiltinConverter 按「这份资料的扩展名有没有被改派」回答。
// 判不定（资料不存在）时返回 false：保守一侧是降档。
func (f *fakeKnowledge) UsesBuiltinConverter(_ context.Context, id string) (bool, error) {
	f.engineCalls++
	if f.engineErr != nil {
		return false, f.engineErr
	}
	row, ok := f.rows[id]
	if !ok {
		return false, nil
	}
	return !f.reroutedTypes[normalizeExtension(row.FileType)], nil
}

// writeText 落一个真实文件，返回它的路径。
func writeText(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("写测试文件: %v", err)
	}
	return path
}

func manualKnowledge(t *testing.T, content string) *types.Knowledge {
	t.Helper()
	row := &types.Knowledge{ID: "k-manual", Type: types.KnowledgeTypeManual, Title: "手工资料"}
	if err := row.SetManualMetadata(types.NewManualKnowledgeMetadata(content, types.ManualKnowledgeStatusPublish, 1)); err != nil {
		t.Fatalf("写手工资料元数据: %v", err)
	}
	return row
}

// 手工资料的正文在 metadata 里。它能当强档不是因为「手工写的就是原文」——
// 那是个假设；而是因为这一段恰好原样通过了切分前的改写。
// 首尾空白与 CRLF 的情况见 TestOriginDeclinesManualContentThePipelineRewrites。
func TestOriginReadsManualContent(t *testing.T) {
	// 刻意不带尾随换行：分块器看到的是 TrimSpace 之后的那一串，
	// 这里要钉的是「逐字取回」，把首尾空白的事留给下一条测试。
	const md = "# 标题\n\n手工写的一段正文。"
	reader := NewOriginReader(&fakeKnowledge{rows: map[string]*types.Knowledge{
		"k-manual": manualKnowledge(t, md),
	}})

	text, ok, err := reader.OriginText(context.Background(), "k-manual")
	if err != nil {
		t.Fatalf("OriginText: %v", err)
	}
	if !ok {
		t.Fatal("手工资料的正文应当可直接取回（强档）")
	}
	if text != md {
		t.Fatalf("取回的正文与写入的不一致：%q", text)
	}
}

// 纯文本文件：文件字节就是解析输入，逐字可比。
// 这里刻意用大写扩展名——底座按用户文件名取扩展名，大小写不定。
func TestOriginReadsPlainTextFile(t *testing.T) {
	const body = "第一段。\n\n第二段，含中文与标点。\n"
	for _, fileType := range []string{"txt", "md", "TXT", ".MD", "markdown"} {
		t.Run(fileType, func(t *testing.T) {
			path := writeText(t, "doc."+strings.TrimPrefix(fileType, "."), body)
			reader := NewOriginReader(&fakeKnowledge{rows: map[string]*types.Knowledge{
				"k-file": {ID: "k-file", FilePath: path, FileType: fileType, ParseStatus: types.ParseStatusCompleted},
			}})

			text, ok, err := reader.OriginText(context.Background(), "k-file")
			if err != nil {
				t.Fatalf("OriginText: %v", err)
			}
			if !ok {
				t.Fatalf("%s 应当直读（强档）", fileType)
			}
			if text != body {
				t.Fatalf("取回内容与文件字节不一致：%q", text)
			}
		})
	}
}

// 手工正文同样会被改写（`knowledge_create.go:1237-1274`）。第一步 TrimSpace
// 是纯函数，所以这里镜像它、取回对齐后的文本；后三步（图片解析、换行归一）
// 重建不出来，只能排除——否则坐标与切分输入对不上，就会判出**假的 stale**。
func TestOriginDeclinesManualContentThePipelineRewrites(t *testing.T) {
	declines := map[string]string{
		"CRLF 会被 NormalizeLineEndings 归一": "第一行\r\n第二行",
		"远程图片会被解析成存储地址":                   "![图](https://example.com/a.png)\n正文",
		"data URI 同上":                     "![](data:image/png;base64,AAAA)\n正文",
	}
	for name, content := range declines {
		t.Run(name, func(t *testing.T) {
			reader := NewOriginReader(&fakeKnowledge{rows: map[string]*types.Knowledge{
				"k-manual": manualKnowledge(t, content),
			}})
			text, ok, err := reader.OriginText(context.Background(), "k-manual")
			if err != nil {
				t.Fatalf("降档不该是错误：%v", err)
			}
			if ok {
				t.Fatalf("不该声称可直读，取回了 %q", text)
			}
		})
	}

	// 首尾空白是纯函数那一步，镜像它比降档更对：取回的字串必须等于
	// 分块器看到的那一串（否则块尾的偏移会越界），而不是「首尾带空白的原文」。
	trimmed := map[string]struct{ raw, want string }{
		"首部的空白":  {"\n\n# 标题\n正文。\n", "# 标题\n正文。"},
		"尾随的换行":  {"# 标题\n正文。\n\n", "# 标题\n正文。"},
		"首尾都有空白": {"  # 标题\n正文。  \n", "# 标题\n正文。"},
	}
	for name, c := range trimmed {
		t.Run(name, func(t *testing.T) {
			reader := NewOriginReader(&fakeKnowledge{rows: map[string]*types.Knowledge{
				"k-manual": manualKnowledge(t, c.raw),
			}})
			text, ok, err := reader.OriginText(context.Background(), "k-manual")
			if err != nil {
				t.Fatalf("OriginText: %v", err)
			}
			if !ok {
				t.Fatal("镜像了 TrimSpace 之后应当仍可直读")
			}
			if text != c.want {
				t.Fatalf("取回的正文 = %q, want %q（分块器看到的那一串）", text, c.want)
			}
		})
	}
}

// 强档还有一个前提：这段正文没被别的解析引擎经手。KB 规则能把 txt/md
// 改派出去（`knowledge_process.go:3771`），那时切分吃的是引擎产出的 markdown，
// 与文件字节、与手工正文都不再逐字相等。问不到、或回答说改派了，都降档。
func TestOriginDeclinesWhenAnotherEngineParsesTheContent(t *testing.T) {
	const body = "第一段。\n\n第二段。\n"
	ctx := context.Background()

	t.Run("纯文本文件被改派", func(t *testing.T) {
		base := &fakeKnowledge{
			rows: map[string]*types.Knowledge{
				"k": {ID: "k", FilePath: writeText(t, "doc.txt", body), FileType: "txt"},
			},
			reroutedTypes: map[string]bool{"txt": true},
		}
		if _, ok, err := NewOriginReader(base).OriginText(ctx, "k"); err != nil || ok {
			t.Fatalf("被改派的文件仍声称可直读：ok=%v err=%v", ok, err)
		}
		if base.engineCalls == 0 {
			t.Error("没有问过解析引擎就发了强档")
		}
	})

	t.Run("引擎问不出来", func(t *testing.T) {
		// 判不定是降档，不是失败；但底座真的坏了要如实报错。
		base := &fakeKnowledge{
			rows:      map[string]*types.Knowledge{"k": {ID: "k", FilePath: writeText(t, "doc.txt", body), FileType: "txt"}},
			engineErr: errors.New("知识库配置读不到"),
		}
		if _, _, err := NewOriginReader(base).OriginText(ctx, "k"); err == nil {
			t.Fatal("引擎判定故障被吞成了降档")
		}
	})

	t.Run("手工资料不受引擎路由影响", func(t *testing.T) {
		// 手工资料走的是另一条链路（直接进 Go 分块器），KB 的引擎规则管不到它。
		base := &fakeKnowledge{
			rows:          map[string]*types.Knowledge{"k-manual": manualKnowledge(t, "# 标题\n\n正文。\n")},
			reroutedTypes: map[string]bool{"txt": true},
		}
		if _, ok, err := NewOriginReader(base).OriginText(ctx, "k-manual"); err != nil || !ok {
			t.Fatalf("手工资料被引擎规则误伤：ok=%v err=%v", ok, err)
		}
	})
}

// 拿不到原文的格式必须老实说 ok=false（走弱档），不能猜。
func TestOriginDeclinesFormatsItCannotRead(t *testing.T) {
	path := writeText(t, "doc.pdf", "%PDF-1.7 看起来像 PDF 的字节")
	for _, fileType := range []string{"pdf", "docx", "doc", "html", "", "unknown", "xlsx"} {
		t.Run(fileType, func(t *testing.T) {
			reader := NewOriginReader(&fakeKnowledge{rows: map[string]*types.Knowledge{
				"k": {ID: "k", FilePath: path, FileType: fileType},
			}})
			text, ok, err := reader.OriginText(context.Background(), "k")
			if err != nil {
				t.Fatalf("OriginText: %v", err)
			}
			if ok || text != "" {
				t.Fatalf("file_type=%q 声称可直读：%q", fileType, text)
			}
		})
	}
}

// 声称强档的前提是「文件字节就是切分用的那段文本」。切分前有三处改写
// （CleanInvalidUTF8 / NormalizeLineEndings / 图片改写），任何一处会动到
// 这串字节，偏移就错位，硬claim只会产出假的 stale。逐条钉住。
func TestOriginDeclinesBytesItCannotProve(t *testing.T) {
	cases := map[string]string{
		"CRLF 会被归一成 LF（NormalizeLineEndings）": "第一行\r\n第二行\r\n",
		"单独的 CR 也会被归一":                        "第一行\r第二行",
		"NUL 会被丢掉（CleanInvalidUTF8）":          "正文\x00继续",
		"Markdown 图片引用会被改写成存储地址":              "![图](https://example.com/a.png)\n正文",
		"HTML img 同上":                         "<img src=\"https://example.com/a.png\">\n正文",
		"data URI 同上":                         "![](data:image/png;base64,AAAA)\n正文",
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			reader := NewOriginReader(&fakeKnowledge{rows: map[string]*types.Knowledge{
				"k": {ID: "k", FilePath: writeText(t, "doc.txt", body), FileType: "txt"},
			}})
			text, ok, err := reader.OriginText(context.Background(), "k")
			if err != nil {
				t.Fatalf("OriginText: %v", err)
			}
			if ok {
				t.Fatalf("不该声称可直读，取回了 %q", text)
			}
		})
	}
	t.Run("非法 UTF-8 会被 CleanInvalidUTF8 丢掉字节", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "doc.txt")
		if err := os.WriteFile(path, []byte{0xff, 0xfe, 0x41}, 0o600); err != nil {
			t.Fatalf("写测试文件: %v", err)
		}
		reader := NewOriginReader(&fakeKnowledge{rows: map[string]*types.Knowledge{
			"k": {ID: "k", FilePath: path, FileType: "txt"},
		}})
		if _, ok, _ := reader.OriginText(context.Background(), "k"); ok {
			t.Fatal("非法 UTF-8 不该声称可直读")
		}
	})
}

// BOM 反过来：U+FEFF 是合法 rune、不是 NUL，CleanInvalidUTF8 与
// NormalizeLineEndings 都不动它，所以它留在两侧文本里，偏移仍然一致。
// 这一条是读了改写链路的代码才敢放开——不放开只是少一个强档，放开了就得对。
func TestOriginAcceptsBOMBecausePipelineKeepsIt(t *testing.T) {
	const body = "带 BOM 的正文"
	data := append([]byte{0xef, 0xbb, 0xbf}, []byte(body)...)
	path := filepath.Join(t.TempDir(), "bom.txt")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("写测试文件: %v", err)
	}
	reader := NewOriginReader(&fakeKnowledge{rows: map[string]*types.Knowledge{
		"k": {ID: "k", FilePath: path, FileType: "txt"},
	}})

	text, ok, err := reader.OriginText(context.Background(), "k")
	if err != nil {
		t.Fatalf("OriginText: %v", err)
	}
	if !ok {
		t.Fatal("BOM 在切分链路里被保留，应当可直读")
	}
	if text != string(data) {
		t.Fatalf("取回内容与文件字节不一致：%q", text)
	}
}

func TestOriginDeclinesMissingOrOversizedFile(t *testing.T) {
	t.Run("文件不存在", func(t *testing.T) {
		reader := NewOriginReader(&fakeKnowledge{rows: map[string]*types.Knowledge{
			"k": {ID: "k", FilePath: filepath.Join(t.TempDir(), "gone.txt"), FileType: "txt"},
		}})
		_, ok, err := reader.OriginText(context.Background(), "k")
		if err != nil {
			t.Fatalf("文件缺失不该是错误（降弱档即可）：%v", err)
		}
		if ok {
			t.Fatal("文件不存在却声称可直读")
		}
	})
	t.Run("空文件", func(t *testing.T) {
		reader := NewOriginReader(&fakeKnowledge{rows: map[string]*types.Knowledge{
			"k": {ID: "k", FilePath: writeText(t, "empty.txt", ""), FileType: "txt"},
		}})
		_, ok, _ := reader.OriginText(context.Background(), "k")
		if ok {
			t.Fatal("空文件没有可比对的正文")
		}
	})
	t.Run("超过上限", func(t *testing.T) {
		big := strings.Repeat("甲", int(maxOriginBytes)+1)
		reader := NewOriginReader(&fakeKnowledge{rows: map[string]*types.Knowledge{
			"k": {ID: "k", FilePath: writeText(t, "big.txt", big), FileType: "txt"},
		}})
		_, ok, err := reader.OriginText(context.Background(), "k")
		if err != nil {
			t.Fatalf("超限不该是错误：%v", err)
		}
		if ok {
			t.Fatal("超过读取上限却声称可直读")
		}
	})
}

// 读不到原文是「弱档」，不是失败；底座真的坏了才是失败。
// 两者混起来会把一次故障说成「这份资料只能弱档」。
func TestOriginSeparatesWeakTierFromFailure(t *testing.T) {
	ctx := context.Background()

	missing := NewOriginReader(&fakeKnowledge{rows: map[string]*types.Knowledge{}})
	text, ok, err := missing.OriginText(ctx, "k-gone")
	if err != nil || ok || text != "" {
		t.Fatalf("资料不存在应当安静降档，得到 (%q, %v, %v)", text, ok, err)
	}

	broken := errors.New("数据库不可达")
	reader := NewOriginReader(&fakeKnowledge{err: broken})
	if _, _, err := reader.OriginText(ctx, "k-1"); !errors.Is(err, broken) {
		t.Fatalf("底座故障被吞掉了：%v", err)
	}

	emptyID := NewOriginReader(&fakeKnowledge{rows: map[string]*types.Knowledge{}})
	if _, ok, err := emptyID.OriginText(ctx, ""); err != nil || ok {
		t.Fatalf("空 ID 应当直接降档：(%v, %v)", ok, err)
	}
	// 空 ID 不该白白查一次底座。
	if emptyID.(*fileOriginReader).knowledge.(*fakeKnowledge).calls != 0 {
		t.Error("空 ID 触达了底座")
	}
}

// 元数据坏掉、正文为空、正文只有空白：都读不出可比对的原文，一律降档。
func TestOriginDeclinesUnusableManualContent(t *testing.T) {
	cases := map[string]*types.Knowledge{
		"元数据坏了": {ID: "k", Type: types.KnowledgeTypeManual, Metadata: types.JSON(`{"content": `)},
		"正文为空":  manualKnowledge(t, ""),
		"只有空白":  manualKnowledge(t, " \n\t "),
	}
	for name, row := range cases {
		t.Run(name, func(t *testing.T) {
			reader := NewOriginReader(&fakeKnowledge{rows: map[string]*types.Knowledge{"k": row}})
			_, ok, err := reader.OriginText(context.Background(), "k")
			if err != nil {
				t.Fatalf("降档不该是错误：%v", err)
			}
			if ok {
				t.Fatal("没有可比对的正文却声称可直读")
			}
		})
	}
}
