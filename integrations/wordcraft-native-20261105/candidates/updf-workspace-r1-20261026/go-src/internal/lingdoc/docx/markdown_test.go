package docx

import (
	"archive/zip"
	"bytes"
	"io"
	"strings"
	"testing"
)

func part(t *testing.T, file []byte, name string) string {
	t.Helper()
	z, e := zip.NewReader(bytes.NewReader(file), int64(len(file)))
	if e != nil {
		t.Fatal(e)
	}
	for _, f := range z.File {
		if f.Name == name {
			r, e := f.Open()
			if e != nil {
				t.Fatal(e)
			}
			defer func() {
				if err := r.Close(); err != nil {
					t.Errorf("close package part: %v", err)
				}
			}()
			b, e := io.ReadAll(r)
			if e != nil {
				t.Fatal(e)
			}
			return string(b)
		}
	}
	t.Fatalf("missing %s", name)
	return ""
}
func TestRichDocumentPreservesStructureAndCitations(t *testing.T) {
	in := demoInput()
	in.Chapters[0].BodyMarkdown = "## 二级标题\n\n中文😀 **重点**与*斜体* [[source:s-demo]]。\n\n- 第一项\n- 第二项\n\n3. 顺序三\n4. 顺序四\n\n### 三级标题\n\n#### 四级标题"
	file, err := Render(in)
	if err != nil {
		t.Fatal(err)
	}
	if err := Validate(in, file); err != nil {
		t.Fatal(err)
	}
	xml := part(t, file, "word/document.xml")
	for _, required := range []string{`w:val="Heading2"`, `w:val="Heading3"`, `w:val="Heading4"`, `<w:b/>`, `<w:i/>`, `<w:numPr>`, `中文😀 `, `[1]`} {
		if !strings.Contains(xml, required) {
			t.Errorf("missing actual OOXML structure %q", required)
		}
	}
	numbering := part(t, file, "word/numbering.xml")
	for _, required := range []string{`w:val="bullet"`, `w:val="decimal"`, `w:start w:val="3"`} {
		if !strings.Contains(numbering, required) {
			t.Errorf("missing numbering %s", required)
		}
	}
	if strings.Contains(xml, "**重点**") {
		t.Fatal("Markdown leaked into Word text")
	}
}
func TestMarkdownWhitelistRefusesUnsupportedSemanticNodes(t *testing.T) {
	for _, body := range []string{"# 正文章标题", "##### 五级", "```go\nx\n```", "> 引用块", "[链接](https://example.com)", "<b>原始html</b>", "列A | 列B\n--- | ---\nA | B", "![图](x.png)", "$x$", "~~删除线~~"} {
		if ValidateMarkdown(body) == nil {
			t.Errorf("unsupported semantics flattened: %q", body)
		}
	}
	for _, body := range []string{"## 标题\n\n正文", "**加粗**与_斜体_", "- 项目一\n- 项目二", "1. 一\n2. 二", "标识 foo_bar_baz", "引用 [[source:a-b_c]]"} {
		if err := ValidateMarkdown(body); err != nil {
			t.Errorf("supported rejected: %q %v", body, err)
		}
	}
}

func TestValidatorDetectsFormattingTamperWithIdenticalText(t *testing.T) {
	in := demoInput()
	in.Chapters[1].BodyMarkdown = "## 标题\n\n**重点**与*斜体*\n\n1. 首项\n2. 次项"
	file, err := Render(in)
	if err != nil {
		t.Fatal(err)
	}
	for _, change := range []struct{ part, old, next string }{{"word/document.xml", "<w:b/>", ""}, {"word/document.xml", `w:val="Heading2"`, `w:val="Normal"`}, {"word/numbering.xml", `w:val="decimal"`, `w:val="bullet"`}} {
		tampered := repackage(t, file, map[string]string{change.part: strings.Replace(part(t, file, change.part), change.old, change.next, 1)}, nil)
		if Validate(in, tampered) == nil {
			t.Fatalf("format tamper accepted %s %s", change.part, change.old)
		}
	}
}

func TestDocumentLiteralEscapesAndEntitiesMatchEditorText(t *testing.T) {
	in := demoInput()
	in.Chapters[1].BodyMarkdown = "**粗体** 与 \\*星号\\* &amp; &#x1F600; \\&amp; &amp;amp;"
	file, err := Render(in)
	if err != nil {
		t.Fatal(err)
	}
	paragraphs, err := paragraphsInFile(file)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, p := range paragraphs {
		if p == "粗体 与 *星号* & 😀 &amp; &amp;" {
			found = true
		}
	}
	if !found {
		t.Fatalf("literal semantic text lost: %q", paragraphs)
	}
	for _, test := range []struct{ raw, want string }{{`\*字面量\*`, "*字面量*"}, {`\&amp;`, "&amp;"}, {`&#38;amp;`, "&amp;"}, {`&amp;#x1F600;`, "&#x1F600;"}} {
		if got := markdownText(test.raw); got != test.want {
			t.Errorf("%q -> %q want %q", test.raw, got, test.want)
		}
	}
}

func TestValidatorRejectsDisconnectedListAndStyleParts(t *testing.T) {
	in := demoInput()
	in.Chapters[1].BodyMarkdown = "## 标题\n\n1. 首项\n2. 次项"
	file, err := Render(in)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct{ part, old string }{{"word/_rels/document.xml.rels", `<Relationship Id="rId2" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/numbering" Target="numbering.xml"/>`}, {"[Content_Types].xml", `<Override PartName="/word/numbering.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.numbering+xml"/>`}, {"word/_rels/document.xml.rels", `<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/styles" Target="styles.xml"/>`}} {
		original := part(t, file, test.part)
		if !strings.Contains(original, test.old) {
			t.Fatal("test mutation missed real link")
		}
		broken := repackage(t, file, map[string]string{test.part: strings.Replace(original, test.old, "", 1)}, nil)
		if Validate(in, broken) == nil {
			t.Fatalf("disconnected format package accepted: %s", test.part)
		}
	}
}

func TestEscapedLiteralMathAndLinkSyntaxAreNotSemanticNodes(t *testing.T) {
	for _, body := range []string{`字面 \$x\$`, `字面 \[链接\]\(x\)`, `字面 \~\~删除\~\~`} {
		if err := ValidateMarkdown(body); err != nil {
			t.Errorf("escaped literal rejected %q %v", body, err)
		}
	}
}
