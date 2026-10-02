// Package docx renders LingDoc's internal demo delivery as an editable DOCX.
// It deliberately supports only plain paragraphs and source markers; callers
// must reject richer Markdown before presenting export as successful.
package docx

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

type Input struct {
	ProjectName  string
	DeliveryKind string
	Chapters     []Chapter
	Sources      []Source
}

type Chapter struct {
	Title        string
	BodyMarkdown string
	SourceIDs    []string
	ReviewItems  []ReviewItem
}

type Source struct {
	ID, DisplayTitle, Locator, QuotedText string
}

type ReviewItem struct {
	ID, Statement, Disposition, Reason string
}

var marker = regexp.MustCompile(`\[\[source:([A-Za-z0-9_-]+)\]\]`)
var blockMarkup = regexp.MustCompile(`^(#{1,6}\s|[-*+]\s|[0-9]+[.)]\s|>)`)
var setextOrRuleMarkup = regexp.MustCompile(`^(?:-{3,}|={3,})\s*$`)
var tableDelimiter = regexp.MustCompile(`^\|?\s*:?-{3,}:?\s*(\|\s*:?-{3,}:?\s*)+\|?$`)

// Underscores are handled only by hasUnderscoreEmphasis below. A broad
// _..._ alternative here would short-circuit that identifier-aware check.
var inlineStarOrStrike = regexp.MustCompile(`(?:\*[^*\n]+\*|~~[^~\n]+~~)`)
var inlineCode = regexp.MustCompile("`+[^\\`\\n]+`+")
var inlineLink = regexp.MustCompile(`!?\[[^\]\n]*\](?:\([^\)\n]*\)|\[[^\]\n]*\])`)
var referenceDefinition = regexp.MustCompile(`^\[[^\]\n]+\]:\s*\S+`)
var inlineHTMLOrAutolink = regexp.MustCompile(`<(?:(?:https?://|mailto:)[^>\n]+|/?[A-Za-z][^>\n]*)>`)
var inlineMath = regexp.MustCompile(`\$[^$\n]+\$`)

// Render creates a standalone WordprocessingML package. The caller remains
// responsible for authorization, snapshot/currentness checks and file storage.
func Render(in Input) ([]byte, error) {
	paragraphs, err := documentParagraphs(in)
	if err != nil {
		return nil, err
	}
	var body strings.Builder
	for _, spec := range paragraphs {
		paragraph(&body, spec.Style, spec.Text)
	}
	body.WriteString(`<w:sectPr><w:pgSz w:w="11906" w:h="16838"/></w:sectPr>`)
	var out bytes.Buffer
	zw := zip.NewWriter(&out)
	parts := map[string]string{
		"[Content_Types].xml":          `<?xml version="1.0" encoding="UTF-8"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/><Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/><Override PartName="/word/styles.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.styles+xml"/></Types>`,
		"_rels/.rels":                  `<?xml version="1.0" encoding="UTF-8"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/></Relationships>`,
		"word/_rels/document.xml.rels": `<?xml version="1.0" encoding="UTF-8"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/styles" Target="styles.xml"/></Relationships>`,
		"word/document.xml":            `<?xml version="1.0" encoding="UTF-8"?><w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body>` + body.String() + `</w:body></w:document>`,
		"word/styles.xml":              `<?xml version="1.0" encoding="UTF-8"?><w:styles xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:style w:type="paragraph" w:styleId="Normal" w:default="1"><w:name w:val="Normal"/></w:style><w:style w:type="paragraph" w:styleId="Title"><w:name w:val="Title"/><w:rPr><w:b/><w:sz w:val="36"/></w:rPr></w:style><w:style w:type="paragraph" w:styleId="Subtitle"><w:name w:val="Subtitle"/><w:rPr><w:color w:val="888888"/></w:rPr></w:style><w:style w:type="paragraph" w:styleId="Heading1"><w:name w:val="heading 1"/><w:rPr><w:b/><w:sz w:val="28"/></w:rPr></w:style></w:styles>`,
	}
	names := make([]string, 0, len(parts))
	for name := range parts {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		w, err := zw.Create(name)
		if err != nil {
			return nil, err
		}
		if _, err := w.Write([]byte(parts[name])); err != nil {
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// paragraphSpec 是一段正文的文字与样式。
type paragraphSpec struct {
	Style string
	Text  string
}

// 文档里固定的那几句话。渲染器与校验器都引用这里，不各自抄一份字面量——
// 抄两份的下场是其中一处改了以后，所有导出开始在运行时校验上失败。
const (
	subtitleText      = "仅供内部演示；并非正式申报材料"
	referencesHeading = "引用资料"
	appendixHeading   = "待核事项与处理结论"
	noReviewText      = "无待核事项。"
	resolvedLabel     = "已解决（人工处置）"
	retainedLabel     = "保留警示"
)

// 处置取值。docx 不依赖 delivery 包，所以这两个字符串在这里是权威定义；适配器
// 应当引用它们，而不是再写一遍字面量。
const (
	DispositionResolved = "resolved"
	// DispositionRetainedWarning 的待核项保留在正文里并在附录中标注保留原因，
	// 而不是把整次导出判为失败。
	DispositionRetainedWarning = "retained_warning"
)

func dispositionLabel(disposition string) (string, bool) {
	switch disposition {
	case DispositionResolved:
		return resolvedLabel, true
	case DispositionRetainedWarning:
		return retainedLabel, true
	default:
		return "", false
	}
}

func referenceLine(number int, source Source) string {
	return fmt.Sprintf("[%d] %s；定位：%s；原文：%s", number, source.DisplayTitle, source.Locator, source.QuotedText)
}

func reviewLine(chapterTitle string, item ReviewItem, label string) string {
	return fmt.Sprintf("%s｜%s｜处理：%s｜理由：%s", chapterTitle, item.Statement, label, item.Reason)
}

// documentParagraphs 是这份文档的完整正文模型：从冻结输入算出「文件里应该有什么」。
// 渲染器照着它生成 XML，校验器照着它核对生成出来的字节——共用一份，是为了让这个
// 问题的答案只有一处。
//
// 它同时是这份格式的**全部输入判定**：不支持的 Markdown、未声明的引用、正文与
// source_ids 对不上、处置不在枚举里，都在这里拒绝。所以校验器拿到的一定是渲染器
// 也接受过的输入，它不必再判一遍输入，只需要判文件。
func documentParagraphs(in Input) ([]paragraphSpec, error) {
	if in.DeliveryKind != "internal_demo" {
		return nil, fmt.Errorf("DOCX prototype only supports internal_demo")
	}
	if strings.TrimSpace(in.ProjectName) == "" || len(in.Chapters) != 2 {
		return nil, fmt.Errorf("DOCX prototype requires a project name and two chapters")
	}
	if !validXMLText(in.ProjectName) {
		return nil, fmt.Errorf("project name contains invalid XML text")
	}
	sources := make(map[string]Source, len(in.Sources))
	for _, source := range in.Sources {
		if source.ID == "" || source.DisplayTitle == "" || source.Locator == "" {
			return nil, fmt.Errorf("source requires id, title and locator")
		}
		if !validXMLText(source.DisplayTitle) || !validXMLText(source.Locator) || !validXMLText(source.QuotedText) {
			return nil, fmt.Errorf("source %q contains invalid XML text", source.ID)
		}
		if _, exists := sources[source.ID]; exists {
			return nil, fmt.Errorf("duplicate source %q", source.ID)
		}
		sources[source.ID] = source
	}

	paragraphs := []paragraphSpec{
		{Style: "Title", Text: in.ProjectName},
		{Style: "Subtitle", Text: subtitleText},
	}
	orderedRefs := []string{}
	refNumbers := map[string]int{}
	seenReviews := map[string]bool{}
	for i, chapter := range in.Chapters {
		if strings.TrimSpace(chapter.Title) == "" || strings.TrimSpace(chapter.BodyMarkdown) == "" {
			return nil, fmt.Errorf("chapter %d requires title and body", i+1)
		}
		if !validXMLText(chapter.Title) || !validXMLText(chapter.BodyMarkdown) {
			return nil, fmt.Errorf("chapter %d contains invalid XML text", i+1)
		}
		paragraphs = append(paragraphs, paragraphSpec{Style: "Heading1", Text: chapter.Title})
		declared := map[string]bool{}
		for _, id := range chapter.SourceIDs {
			if declared[id] {
				return nil, fmt.Errorf("chapter %d has duplicate source %q", i+1, id)
			}
			if _, ok := sources[id]; !ok {
				return nil, fmt.Errorf("chapter %d refers to missing source %q", i+1, id)
			}
			declared[id] = true
		}
		used := map[string]bool{}
		for _, rawLine := range strings.Split(strings.ReplaceAll(chapter.BodyMarkdown, "\r\n", "\n"), "\n") {
			line := strings.TrimSpace(rawLine)
			if line == "" {
				continue
			}
			if containsUnsupportedMarkdown(rawLine) {
				return nil, fmt.Errorf("chapter %d contains unsupported Markdown", i+1)
			}
			matches := marker.FindAllStringSubmatch(line, -1)
			for _, match := range matches {
				id := match[1]
				if !declared[id] {
					return nil, fmt.Errorf("chapter %d uses undeclared source %q", i+1, id)
				}
				used[id] = true
				if refNumbers[id] == 0 {
					orderedRefs = append(orderedRefs, id)
					refNumbers[id] = len(orderedRefs)
				}
			}
			line = marker.ReplaceAllStringFunc(line, func(token string) string {
				return fmt.Sprintf("[%d]", refNumbers[marker.FindStringSubmatch(token)[1]])
			})
			if strings.Contains(line, "[[source:") || strings.Contains(line, "![") || strings.Contains(line, "```") {
				return nil, fmt.Errorf("chapter %d contains unsupported markup", i+1)
			}
			paragraphs = append(paragraphs, paragraphSpec{Text: line})
		}
		if len(used) != len(declared) {
			return nil, fmt.Errorf("chapter %d source list does not match body markers", i+1)
		}
		for _, item := range chapter.ReviewItems {
			if item.ID == "" || item.Statement == "" || item.Reason == "" || !validXMLText(item.Statement) || !validXMLText(item.Reason) || seenReviews[item.ID] {
				return nil, fmt.Errorf("chapter %d has incomplete or duplicate review item", i+1)
			}
			if _, ok := dispositionLabel(item.Disposition); !ok {
				return nil, fmt.Errorf("chapter %d has unsupported review disposition %q", i+1, item.Disposition)
			}
			seenReviews[item.ID] = true
		}
	}
	if len(orderedRefs) > 0 {
		paragraphs = append(paragraphs, paragraphSpec{Style: "Heading1", Text: referencesHeading})
		for _, id := range orderedRefs {
			paragraphs = append(paragraphs, paragraphSpec{Text: referenceLine(refNumbers[id], sources[id])})
		}
	}
	paragraphs = append(paragraphs, paragraphSpec{Style: "Heading1", Text: appendixHeading})
	if len(seenReviews) == 0 {
		paragraphs = append(paragraphs, paragraphSpec{Text: noReviewText})
	} else {
		for _, chapter := range in.Chapters {
			for _, item := range chapter.ReviewItems {
				label, _ := dispositionLabel(item.Disposition)
				paragraphs = append(paragraphs, paragraphSpec{Text: reviewLine(chapter.Title, item, label)})
			}
		}
	}
	return paragraphs, nil
}

func containsUnsupportedMarkdown(rawLine string) bool {
	// Indented code and trailing-two-space hard breaks have Markdown semantics
	// that this renderer would otherwise silently trim away.
	if strings.HasPrefix(rawLine, "\t") || strings.HasPrefix(rawLine, "    ") || strings.HasSuffix(rawLine, "  ") {
		return true
	}

	line := strings.TrimSpace(rawLine)
	// Source markers are the only supported markup. Remove valid markers before
	// probing so '-'/'_' inside source IDs are not mistaken for Markdown.
	probe := marker.ReplaceAllString(line, "")
	if blockMarkup.MatchString(probe) ||
		setextOrRuleMarkup.MatchString(probe) ||
		strings.HasPrefix(probe, "```") ||
		strings.HasPrefix(probe, "~~~") ||
		tableDelimiter.MatchString(probe) {
		return true
	}
	return inlineStarOrStrike.MatchString(probe) ||
		hasUnderscoreEmphasis(probe) ||
		inlineCode.MatchString(probe) ||
		inlineLink.MatchString(probe) ||
		referenceDefinition.MatchString(probe) ||
		inlineHTMLOrAutolink.MatchString(probe) ||
		inlineMath.MatchString(probe)
}

// hasUnderscoreEmphasis detects the supported Markdown-emphasis shape without
// treating intraword underscores as markup. Common identifiers such as
// foo_bar_baz remain plain text, while "_重点_" and "A _重点_ B" are rejected.
func hasUnderscoreEmphasis(value string) bool {
	runes := []rune(value)
	for i, r := range runes {
		if r != '_' || i+1 >= len(runes) || unicode.IsSpace(runes[i+1]) {
			continue
		}
		// An opening underscore inside an identifier is literal text.
		if i > 0 && isWordRune(runes[i-1]) {
			continue
		}
		for j := i + 1; j < len(runes); j++ {
			if runes[j] != '_' || j == i+1 || unicode.IsSpace(runes[j-1]) {
				continue
			}
			// A closing underscore followed by a word rune is also intraword.
			if j+1 < len(runes) && isWordRune(runes[j+1]) {
				continue
			}
			return true
		}
	}
	return false
}

func isWordRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_'
}

func paragraph(b *strings.Builder, style, value string) {
	b.WriteString(`<w:p>`)
	if style != "" {
		b.WriteString(`<w:pPr><w:pStyle w:val="` + style + `"/></w:pPr>`)
	}
	b.WriteString(`<w:r><w:t xml:space="preserve">`)
	_ = xml.EscapeText(b, []byte(value))
	b.WriteString(`</w:t></w:r></w:p>`)
}

func validXMLText(value string) bool {
	if !utf8.ValidString(value) {
		return false
	}
	for _, r := range value {
		if r != '\t' && r != '\n' && r != '\r' && (r < 0x20 || r == 0xfffe || r == 0xffff) {
			return false
		}
	}
	return true
}
