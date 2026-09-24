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
var blockMarkup = regexp.MustCompile(`^(#{1,6}\s|[-*+]\s|[0-9]+[.)]\s|\||>)`)
var setextOrRuleMarkup = regexp.MustCompile(`^(?:-{3,}|={3,})\s*$`)
var inlineEmphasis = regexp.MustCompile(`(?:\*[^*\n]+\*|_[^_\n]+_|~~[^~\n]+~~)`)
var inlineCode = regexp.MustCompile("`+[^\\`\\n]+`+")
var inlineLink = regexp.MustCompile(`!?\[[^\]\n]*\](?:\([^\)\n]*\)|\[[^\]\n]*\])`)
var referenceDefinition = regexp.MustCompile(`^\[[^\]\n]+\]:\s*\S+`)
var inlineHTMLOrAutolink = regexp.MustCompile(`<(?:(?:https?://|mailto:)[^>\n]+|/?[A-Za-z][^>\n]*)>`)
var inlineMath = regexp.MustCompile(`\$[^$\n]+\$`)

// Render creates a standalone WordprocessingML package. The caller remains
// responsible for authorization, snapshot/currentness checks and file storage.
func Render(in Input) ([]byte, error) {
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

	var body strings.Builder
	paragraph(&body, "Title", in.ProjectName)
	paragraph(&body, "Subtitle", "仅供内部演示；并非正式申报材料")
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
		paragraph(&body, "Heading1", chapter.Title)
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
			paragraph(&body, "", line)
		}
		if len(used) != len(declared) {
			return nil, fmt.Errorf("chapter %d source list does not match body markers", i+1)
		}
		for _, item := range chapter.ReviewItems {
			if item.ID == "" || item.Statement == "" || item.Reason == "" || !validXMLText(item.Statement) || !validXMLText(item.Reason) || seenReviews[item.ID] {
				return nil, fmt.Errorf("chapter %d has incomplete or duplicate review item", i+1)
			}
			if item.Disposition != "resolved" && item.Disposition != "retained_warning" {
				return nil, fmt.Errorf("chapter %d has unsupported review disposition %q", i+1, item.Disposition)
			}
			seenReviews[item.ID] = true
		}
	}
	if len(orderedRefs) > 0 {
		paragraph(&body, "Heading1", "引用资料")
		for _, id := range orderedRefs {
			s := sources[id]
			paragraph(&body, "", fmt.Sprintf("[%d] %s；定位：%s；原文：%s", refNumbers[id], s.DisplayTitle, s.Locator, s.QuotedText))
		}
	}
	paragraph(&body, "Heading1", "待核事项与处理结论")
	if len(seenReviews) == 0 {
		paragraph(&body, "", "无待核事项。")
	} else {
		for _, chapter := range in.Chapters {
			for _, item := range chapter.ReviewItems {
				label := "已解决（人工处置）"
				if item.Disposition == "retained_warning" {
					label = "保留警示"
				}
				paragraph(&body, "", fmt.Sprintf("%s｜%s｜处理：%s｜理由：%s", chapter.Title, item.Statement, label, item.Reason))
			}
		}
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
	return inlineEmphasis.MatchString(probe) ||
		inlineCode.MatchString(probe) ||
		inlineLink.MatchString(probe) ||
		referenceDefinition.MatchString(probe) ||
		inlineHTMLOrAutolink.MatchString(probe) ||
		inlineMath.MatchString(probe)
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
