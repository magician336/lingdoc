package docx

import (
	"archive/zip"
	"bytes"
	"hash/crc32"
	"io"
	"strings"
	"testing"
)

func TestValidateAcceptsEveryDocumentRenderProduces(t *testing.T) {
	retained := demoInput()
	plain := Input{
		ProjectName: "合成资料演示项目", DeliveryKind: "internal_demo",
		Sources: []Source{{ID: "s-demo", DisplayTitle: "合成测试材料", Locator: "测试段落 1", QuotedText: "100 条虚构记录"}},
		Chapters: []Chapter{
			{Title: "研究问题", BodyMarkdown: "材料包含100条虚构记录 [[source:s-demo]]。", SourceIDs: []string{"s-demo"}},
			{Title: "研究方案", BodyMarkdown: "使用合成记录检验软件流程。"},
		},
	}
	for name, in := range map[string]Input{"retained appendix": retained, "empty appendix": plain} {
		t.Run(name, func(t *testing.T) {
			file, err := Render(in)
			if err != nil {
				t.Fatal(err)
			}
			if err := Validate(in, file); err != nil {
				t.Fatalf("Render produced a file its own validator rejects: %v", err)
			}
		})
	}
}

func TestValidateRejectsABrokenPackage(t *testing.T) {
	file, err := Render(demoInput())
	if err != nil {
		t.Fatal(err)
	}
	document, err := readMember("word/document.xml", file)
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		file []byte
	}{
		{name: "not a zip at all", file: []byte("这不是一个压缩包")},
		{name: "truncated archive", file: file[:len(file)/2]},
		{name: "empty", file: nil},
		{name: "missing the styles part", file: repackage(t, file, nil, []string{"word/styles.xml"})},
		{name: "missing the content types part", file: repackage(t, file, nil, []string{"[Content_Types].xml"})},
		{name: "missing the document part", file: repackage(t, file, nil, []string{"word/document.xml"})},
		{name: "document is not well-formed", file: repackage(t, file, map[string]string{"word/document.xml": "<w:document><w:body>"}, nil)},
		{name: "document carries no element", file: repackage(t, file, map[string]string{"word/document.xml": ""}, nil)},
		{name: "document ends inside a paragraph", file: repackage(t, file, map[string]string{"word/document.xml": strings.Replace(document, "</w:body></w:document>", "", 1)}, nil)},
		{name: "wrong namespace", file: repackage(t, file, map[string]string{"word/document.xml": strings.Replace(document, wordprocessingNS, "http://example.invalid/not-wordprocessingml", 1)}, nil)},
		{name: "member checksum disagrees with its content", file: withBrokenChecksum(t, file)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := Validate(demoInput(), test.file); err == nil {
				t.Fatal("validator accepted a broken package")
			}
		})
	}
}

// 这一族是真正要紧的：文件本身是完好的 DOCX，但它说的不是冻结输入说的话。
// 渲染器一旦漏写一段、写反顺序或改坏数字，形状就是这样的。
func TestValidateRejectsADocumentThatDoesNotMatchTheInput(t *testing.T) {
	file, err := Render(demoInput())
	if err != nil {
		t.Fatal(err)
	}
	document, err := readMember("word/document.xml", file)
	if err != nil {
		t.Fatal(err)
	}
	head, paragraphs, tail := splitParagraphs(t, document)
	join := func(edited []string) []byte {
		return repackage(t, file, map[string]string{"word/document.xml": head + strings.Join(edited, "") + tail}, nil)
	}
	without := func(index int) []string {
		out := append([]string(nil), paragraphs...)
		return append(out[:index], out[index+1:]...)
	}

	chapterOne := indexOfParagraph(t, paragraphs, "研究问题")
	chapterTwo := indexOfParagraph(t, paragraphs, "研究方案")

	tests := []struct {
		name string
		file []byte
	}{
		{name: "a body paragraph was dropped", file: join(without(indexOfParagraph(t, paragraphs, "这不是研究结论。")))},
		{name: "a chapter heading was dropped", file: join(without(chapterTwo))},
		{name: "the chapters were written in the wrong order", file: join(swap(paragraphs, chapterOne, chapterTwo))},
		{name: "the citation appendix was dropped", file: join(without(indexOfParagraph(t, paragraphs, referencesHeading)))},
		{name: "the review appendix was dropped", file: join(without(indexOfParagraph(t, paragraphs, appendixHeading)))},
		{name: "a key number was altered", file: join(editParagraph(paragraphs, indexOfParagraph(t, paragraphs, "100条"), "100条", "999条"))},
		{name: "a citation number was renumbered", file: join(editParagraph(paragraphs, indexOfParagraph(t, paragraphs, "[1] 合成测试材料"), "[1]", "[7]"))},
		{name: "the disposition was swapped", file: join(editParagraph(paragraphs, indexOfParagraph(t, paragraphs, retainedLabel), retainedLabel, resolvedLabel))},
		{name: "an extra paragraph appeared", file: join(append(append([]string(nil), paragraphs...), `<w:p><w:r><w:t>多出来的一段</w:t></w:r></w:p>`))},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := Validate(demoInput(), test.file)
			if err == nil {
				t.Fatal("validator accepted a document that does not say what the input says")
			}
			if !strings.Contains(err.Error(), "paragraph") {
				t.Fatalf("validator failed for the wrong reason: %v", err)
			}
		})
	}

	// 反证：把段落原样拼回去必须仍然通过——否则上面那群用例可能只是被某个语法
	// 事故一起打回来的。
	if err := Validate(demoInput(), join(paragraphs)); err != nil {
		t.Fatalf("reassembling the document verbatim broke validation: %v", err)
	}
}

func TestValidateRejectsInputThatCouldNotHaveBeenRendered(t *testing.T) {
	file, err := Render(demoInput())
	if err != nil {
		t.Fatal(err)
	}
	broken := demoInput()
	broken.Chapters = broken.Chapters[:1]
	if err := Validate(broken, file); err == nil {
		t.Fatal("validator accepted a two-chapter file against a one-chapter input")
	}
}

func readMember(name string, file []byte) (string, error) {
	reader, err := zip.NewReader(bytes.NewReader(file), int64(len(file)))
	if err != nil {
		return "", err
	}
	for _, member := range reader.File {
		if member.Name != name {
			continue
		}
		stream, err := member.Open()
		if err != nil {
			return "", err
		}
		defer func() { _ = stream.Close() }()
		content, err := io.ReadAll(stream)
		if err != nil {
			return "", err
		}
		return string(content), nil
	}
	return "", nil
}

// repackage 把一份渲染结果解回成员表，替换掉 replace 里的成员、丢掉 drop 里的成员，
// 再原样重打一份。用它伪造渲染器的各种坏输出。
func repackage(t *testing.T, file []byte, replace map[string]string, drop []string) []byte {
	t.Helper()
	reader, err := zip.NewReader(bytes.NewReader(file), int64(len(file)))
	if err != nil {
		t.Fatal(err)
	}
	dropped := map[string]bool{}
	for _, name := range drop {
		dropped[name] = true
	}
	var out bytes.Buffer
	writer := zip.NewWriter(&out)
	for _, member := range reader.File {
		if dropped[member.Name] {
			continue
		}
		content, err := readMember(member.Name, file)
		if err != nil {
			t.Fatal(err)
		}
		if replacement, ok := replace[member.Name]; ok {
			content = replacement
		}
		w, err := writer.Create(member.Name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

// withBrokenChecksum 重打一份包，让 document.xml 的 CRC 与它的内容对不上。内容还
// 读得出来，但它已经不是当初写进去的那份了——zip 只在读到 EOF 时才核对 CRC，所以
// 这正是「不读完就验不出完整性」的那条路径。
func withBrokenChecksum(t *testing.T, file []byte) []byte {
	t.Helper()
	reader, err := zip.NewReader(bytes.NewReader(file), int64(len(file)))
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	writer := zip.NewWriter(&out)
	for _, member := range reader.File {
		content, err := readMember(member.Name, file)
		if err != nil {
			t.Fatal(err)
		}
		raw := []byte(content)
		header := &zip.FileHeader{Name: member.Name, Method: zip.Store}
		header.CRC32 = crc32.ChecksumIEEE(raw)
		if member.Name == documentPart {
			header.CRC32 ^= 0xffffffff
		}
		header.UncompressedSize64 = uint64(len(raw))
		header.CompressedSize64 = uint64(len(raw))
		w, err := writer.CreateRaw(header)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(raw); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

// splitParagraphs 把 document.xml 切成「正文之前」「正文各段」「正文之后」三块。
// 渲染器把正文写成一串扁平的 <w:p>，所以按段切开重排就能伪造漏写与乱序。
func splitParagraphs(t *testing.T, document string) (string, []string, string) {
	t.Helper()
	start := strings.Index(document, "<w:p>")
	end := strings.Index(document, "<w:sectPr>")
	if start < 0 || end < start {
		t.Fatalf("rendered document.xml is not the expected shape: %s", document)
	}
	var paragraphs []string
	for _, piece := range strings.Split(document[start:end], "</w:p>") {
		if piece != "" {
			paragraphs = append(paragraphs, piece+"</w:p>")
		}
	}
	return document[:start], paragraphs, document[end:]
}

func indexOfParagraph(t *testing.T, paragraphs []string, text string) int {
	t.Helper()
	for i, paragraph := range paragraphs {
		if strings.Contains(paragraph, text) {
			return i
		}
	}
	t.Fatalf("no paragraph carries %q", text)
	return -1
}

func editParagraph(paragraphs []string, index int, from, to string) []string {
	out := append([]string(nil), paragraphs...)
	out[index] = strings.Replace(out[index], from, to, 1)
	return out
}

// swap 把 from 处的段落挪到 to 处，用来伪造顺序错乱。
func swap(paragraphs []string, from, to int) []string {
	out := append([]string(nil), paragraphs...)
	moved := out[from]
	out = append(out[:from], out[from+1:]...)
	out = append(out[:to], append([]string{moved}, out[to:]...)...)
	return out
}
