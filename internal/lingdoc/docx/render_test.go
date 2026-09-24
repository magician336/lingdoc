package docx

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"io"
	"strings"
	"testing"
)

func demoInput() Input {
	return Input{
		ProjectName: "合成资料演示项目", DeliveryKind: "internal_demo",
		Sources: []Source{{ID: "s-demo", DisplayTitle: "合成测试材料", Locator: "测试段落 1", QuotedText: "100 条虚构记录"}},
		Chapters: []Chapter{
			{Title: "研究问题", BodyMarkdown: "材料包含100条虚构记录 [[source:s-demo]]。\n这不是研究结论。", SourceIDs: []string{"s-demo"}, ReviewItems: []ReviewItem{{ID: "review-1", Statement: "不能证明真实结论", Disposition: "retained_warning", Reason: "仅供内部演示"}}},
			{Title: "研究方案", BodyMarkdown: "使用合成记录检验软件流程。"},
		},
	}
}

func TestRenderEditableDocumentStructure(t *testing.T) {
	data, err := Render(demoInput())
	if err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	parts := map[string]bool{}
	var textParts []string
	for _, file := range zr.File {
		parts[file.Name] = true
		if file.Name != "word/document.xml" {
			continue
		}
		r, err := file.Open()
		if err != nil {
			t.Fatal(err)
		}
		decoder := xml.NewDecoder(r)
		for {
			tok, err := decoder.Token()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatal(err)
			}
			if start, ok := tok.(xml.StartElement); ok && start.Name.Local == "t" {
				var value string
				if err := decoder.DecodeElement(&value, &start); err != nil {
					t.Fatal(err)
				}
				textParts = append(textParts, value)
			}
		}
		_ = r.Close()
	}
	for _, name := range []string{"[Content_Types].xml", "_rels/.rels", "word/document.xml", "word/styles.xml"} {
		if !parts[name] {
			t.Errorf("missing DOCX part %s", name)
		}
	}
	joined := strings.Join(textParts, "\n")
	for _, expected := range []string{"仅供内部演示", "研究问题", "100条虚构记录 [1]", "研究方案", "引用资料", "[1] 合成测试材料", "待核事项与处理结论", "保留警示", "仅供内部演示"} {
		if !strings.Contains(joined, expected) {
			t.Errorf("missing %q in document text", expected)
		}
	}
	if strings.Index(joined, "研究问题") >= strings.Index(joined, "研究方案") {
		t.Fatal("chapters out of order")
	}
}

func TestRejectSilentCitationLoss(t *testing.T) {
	tests := []struct {
		name   string
		change func(*Input)
	}{
		{"undeclared marker", func(in *Input) { in.Chapters[0].SourceIDs = nil }},
		{"unreferenced declaration", func(in *Input) { in.Chapters[0].BodyMarkdown = "没有引用" }},
		{"missing source", func(in *Input) { in.Sources = nil }},
		{"unsupported list", func(in *Input) { in.Chapters[0].BodyMarkdown = "- 第一项"; in.Chapters[0].SourceIDs = nil }},
		{"unsupported numbered list", func(in *Input) { in.Chapters[0].BodyMarkdown = "1. 第一项"; in.Chapters[0].SourceIDs = nil }},
		{"unsupported emphasis", func(in *Input) { in.Chapters[0].BodyMarkdown = "*重点*"; in.Chapters[0].SourceIDs = nil }},
		{"unsupported underscore emphasis", func(in *Input) { in.Chapters[0].BodyMarkdown = "_重点_"; in.Chapters[0].SourceIDs = nil }},
		{"unsupported inline code", func(in *Input) { in.Chapters[0].BodyMarkdown = "`code`"; in.Chapters[0].SourceIDs = nil }},
		{"unsupported strikethrough", func(in *Input) { in.Chapters[0].BodyMarkdown = "~~删除线~~"; in.Chapters[0].SourceIDs = nil }},
		{"unsupported inline link", func(in *Input) {
			in.Chapters[0].BodyMarkdown = "[链接](https://example.com)"
			in.Chapters[0].SourceIDs = nil
		}},
		{"unsupported reference link", func(in *Input) { in.Chapters[0].BodyMarkdown = "[链接][ref]"; in.Chapters[0].SourceIDs = nil }},
		{"unsupported autolink", func(in *Input) { in.Chapters[0].BodyMarkdown = "<https://example.com>"; in.Chapters[0].SourceIDs = nil }},
		{"unsupported inline html", func(in *Input) { in.Chapters[0].BodyMarkdown = "<em>重点</em>"; in.Chapters[0].SourceIDs = nil }},
		{"unsupported inline math", func(in *Input) { in.Chapters[0].BodyMarkdown = "$x+y$"; in.Chapters[0].SourceIDs = nil }},
		{"unsupported setext heading", func(in *Input) { in.Chapters[0].BodyMarkdown = "标题\n---"; in.Chapters[0].SourceIDs = nil }},
		{"unsupported indented code", func(in *Input) { in.Chapters[0].BodyMarkdown = "    code"; in.Chapters[0].SourceIDs = nil }},
		{"unsupported hard break", func(in *Input) {
			in.Chapters[0].BodyMarkdown = "第一行  \n第二行"
			in.Chapters[0].SourceIDs = nil
		}},
		{"unsupported table", func(in *Input) {
			in.Chapters[0].BodyMarkdown = "列A | 列B\n--- | ---\nA | B"
			in.Chapters[0].SourceIDs = nil
		}},
		{"invalid XML control", func(in *Input) { in.ProjectName = "测试\x00" }},
		{"invalid disposition", func(in *Input) { in.Chapters[0].ReviewItems[0].Disposition = "unknown" }},
		{"formal export", func(in *Input) { in.DeliveryKind = "formal" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := demoInput()
			tt.change(&in)
			if _, err := Render(in); err == nil {
				t.Fatal("expected refusal")
			}
		})
	}
}

func TestAllowsLiteralPipeInPlainParagraph(t *testing.T) {
	in := demoInput()
	in.Chapters[1].BodyMarkdown = "A | B 是普通文本。"
	data, err := Render(in)
	if err != nil {
		t.Fatalf("literal pipe should remain valid plain text: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("render returned an empty DOCX")
	}
}

func TestAllowsUnderscoresInsidePlainIdentifiers(t *testing.T) {
	for _, body := range []string{
		"foo_bar_baz 是普通标识符。",
		"snake_case_value 可以作为字段名。",
		"field_name 不应被当成强调。",
		"foo__bar__baz 也只是标识符。",
		"版本_1_说明包含 Unicode 字符和数字。",
		"field_name 与 snake_case_value；A | B。",
	} {
		t.Run(body, func(t *testing.T) {
			in := demoInput()
			in.Chapters[1].BodyMarkdown = body
			data, err := Render(in)
			if err != nil {
				t.Fatalf("identifier underscore should remain valid plain text: %v", err)
			}
			assertDOCXContainsText(t, data, body)
		})
	}
}

func TestRejectsBoundaryUnderscoreEmphasis(t *testing.T) {
	for _, body := range []string{
		"_重点_",
		"前文 _重点_ 后文",
		"__重点__",
		"（_重点_）",
		"_snake_case_value_",
	} {
		t.Run(body, func(t *testing.T) {
			in := demoInput()
			in.Chapters[1].BodyMarkdown = body
			if _, err := Render(in); err == nil {
				t.Fatal("expected underscore emphasis to be rejected")
			}
		})
	}
}

// Check the rendered text, not just that Render returned a nonempty ZIP.
func assertDOCXContainsText(t *testing.T, data []byte, expected string) {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range zr.File {
		if file.Name != "word/document.xml" {
			continue
		}
		r, err := file.Open()
		if err != nil {
			t.Fatal(err)
		}
		defer r.Close()
		decoder := xml.NewDecoder(r)
		for {
			token, err := decoder.Token()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatal(err)
			}
			if start, ok := token.(xml.StartElement); ok && start.Name.Local == "t" {
				var value string
				if err := decoder.DecodeElement(&value, &start); err != nil {
					t.Fatal(err)
				}
				if value == expected {
					return
				}
			}
		}
	}
	t.Fatalf("DOCX did not preserve exact text %q", expected)
}
