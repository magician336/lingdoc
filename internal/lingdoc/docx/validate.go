package docx

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"strings"
)

// Validate 拿**已经生成出来的字节**核对冻结输入，回答的是「这份文件里确实有该有的
// 东西吗」——而不是「渲染器说它成功了吗」。§7 要求校验结构、正文顺序、关键数字、
// 引用和待核附录，通过后才提供下载；F13「导出器产生损坏文件」正是以它为断言对象。
//
// 做法是把文件解回正文段落，与 documentParagraphs 算出的期望逐段比对。一条相等
// 就把 §7 点名的五项一次全占了：
//
//   - 结构——压缩包成员齐全、XML 良构、每个成员的 CRC 都对；
//   - 正文顺序——第 i 段必须等于第 i 段，多一段少一段都算错；
//   - 关键数字——数字长在正文段里，段相等则数字相等；
//   - 引用——`[[source:x]]` 解析成的 `[n]` 与引用资料段的原文都在段里；
//   - 待核附录——附录每一条的处理结论与保留原因都在段里。
//
// 所以这里**没有**另设一个「关键数字」检查：段落全等时它永远不会独立触发，写出来
// 只是给人「数字有人专门看着」的错觉。真正需要独立成一条的判定，是上面五项里有哪
// 一项能在段落全等的前提下仍然出错——目前一项也没有。
//
// 严格逐段相等意味着只适用于**刚渲染出来的**文件：Word 或 WPS 重存一次就会重排
// 段落，那时它会失败。这是刻意的，不要去给下载来的文件复检时用它。
func Validate(in Input, file []byte) error {
	expected, err := documentParagraphs(in)
	if err != nil {
		return fmt.Errorf("frozen input cannot form a document: %w", err)
	}
	actual, err := paragraphsInFile(file)
	if err != nil {
		return err
	}
	if len(actual) != len(expected) {
		return fmt.Errorf("%s holds %d paragraphs, want %d", documentPart, len(actual), len(expected))
	}
	for i, spec := range expected {
		if actual[i] != spec.Text {
			return fmt.Errorf("paragraph %d is %q, want %q", i+1, actual[i], spec.Text)
		}
	}
	return nil
}

const documentPart = "word/document.xml"

const wordprocessingNS = "http://schemas.openxmlformats.org/wordprocessingml/2006/main"

// packageParts 是一份可编辑 DOCX 必须带的成员：类型声明、包级关系、正文、样式，
// 以及把正文与样式连起来的那条关系。Render 每个都产出，缺一个 Word 就会报文件损坏。
var packageParts = []string{
	"[Content_Types].xml",
	"_rels/.rels",
	documentPart,
	"word/styles.xml",
	"word/_rels/document.xml.rels",
}

// paragraphsInFile 把一份 DOCX 读成它的正文段落序列。它只认这一段格式——什么样的
// 标签算段落、文字藏在哪儿——所以它与渲染器同住一个包。
func paragraphsInFile(file []byte) ([]string, error) {
	reader, err := zip.NewReader(bytes.NewReader(file), int64(len(file)))
	if err != nil {
		return nil, fmt.Errorf("exported file is not a readable ZIP package: %w", err)
	}
	present := make(map[string]bool, len(reader.File))
	var document *zip.File
	for _, member := range reader.File {
		present[member.Name] = true
		if member.Name == documentPart {
			document = member
		}
	}
	for _, name := range packageParts {
		if !present[name] {
			return nil, fmt.Errorf("exported file has no %s part", name)
		}
	}
	if err := verifyMembers(reader); err != nil {
		return nil, err
	}
	stream, err := document.Open()
	if err != nil {
		return nil, fmt.Errorf("cannot open %s: %w", documentPart, err)
	}
	defer func() { _ = stream.Close() }()
	return readParagraphs(stream)
}

// verifyMembers 把每个成员整个读一遍。archive/zip 只在读到 EOF 时才核对 CRC，不读
// 完就等于没验完整性——字节被截断或改坏正是 F13 那个场景。
func verifyMembers(reader *zip.Reader) error {
	for _, member := range reader.File {
		stream, err := member.Open()
		if err != nil {
			return fmt.Errorf("part %s cannot be opened: %w", member.Name, err)
		}
		_, err = io.Copy(io.Discard, stream)
		if err == nil {
			err = stream.Close()
		}
		if err != nil {
			return fmt.Errorf("part %s is corrupt: %w", member.Name, err)
		}
	}
	return nil
}

// readParagraphs 走一遍 WordprocessingML 的 token 流，按出现顺序收集每个 w:p 的
// 全部 w:t 文字。段落之外（比如文末的 w:sectPr）的文字不算正文。
func readParagraphs(source io.Reader) ([]string, error) {
	var (
		paragraphs []string
		current    strings.Builder
		depth      int
		rooted     bool
	)
	decoder := xml.NewDecoder(source)
	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("%s is not well-formed XML: %w", documentPart, err)
		}
		switch element := token.(type) {
		case xml.StartElement:
			switch {
			case !rooted:
				if element.Name.Local != "document" || element.Name.Space != wordprocessingNS {
					return nil, fmt.Errorf("%s root element is {%s}%s, want the WordprocessingML document", documentPart, element.Name.Space, element.Name.Local)
				}
				rooted = true
			case element.Name.Local == "p":
				depth++
			case element.Name.Local == "t" && depth > 0:
				var text string
				if err := decoder.DecodeElement(&text, &element); err != nil {
					return nil, fmt.Errorf("%s holds an unreadable text run: %w", documentPart, err)
				}
				current.WriteString(text)
			}
		case xml.EndElement:
			if element.Name.Local == "p" && depth > 0 {
				depth--
				if depth == 0 {
					paragraphs = append(paragraphs, current.String())
					current.Reset()
				}
			}
		}
	}
	if !rooted {
		return nil, fmt.Errorf("%s carries no document element", documentPart)
	}
	if depth != 0 {
		return nil, fmt.Errorf("%s ends inside an unclosed paragraph", documentPart)
	}
	return paragraphs, nil
}
