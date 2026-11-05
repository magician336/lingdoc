package docx

import (
	"encoding/xml"
	"fmt"
	"github.com/Tencent/WeKnora/internal/lingdoc/sourcemarker"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
	"html"
	"strconv"
	"strings"
)

type runSpec struct {
	Text         string
	Bold, Italic bool
}
type listSpec struct {
	Ordered                    bool
	Start, Level, Group, NumID int
}

// Parse the Markdown AST, not HTML, so unsupported nodes cannot silently lose
// their semantics. Plain legacy bodies retain their original per-line layout.
func markdownParagraphs(markdown string, citation func(string) (string, error)) ([]paragraphSpec, error) {
	if !validXMLText(markdown) {
		return nil, fmt.Errorf("%w: invalid text", ErrUnsupportedMarkdown)
	}
	source := []byte(strings.ReplaceAll(markdown, "\r\n", "\n"))
	for _, line := range strings.Split(string(source), "\n") {
		probe := syntaxProbe(sourcemarker.Strip(line))
		if referenceDefinition.MatchString(strings.TrimSpace(probe)) || tableDelimiter.MatchString(strings.TrimSpace(probe)) || inlineMath.MatchString(probe) || strings.Contains(probe, "~~") || inlineLink.MatchString(probe) || setextOrRuleMarkup.MatchString(strings.TrimSpace(probe)) {
			return nil, fmt.Errorf("%w: table/reference/math/strike syntax", ErrUnsupportedMarkdown)
		}
	}
	// Protect source atoms from CommonMark's bracket splitting. Restore them
	// before collecting citations, never serialize the private placeholders.
	prefix := "\uE000LingDocCitation"
	for strings.Contains(string(source), prefix) {
		prefix += "_"
	}
	placeholders := map[string]string{}
	serial := 0
	safe := sourcemarker.ReplaceAll(string(source), func(token, _ string) string {
		serial++
		key := fmt.Sprintf("%s%d\uE001", prefix, serial)
		placeholders[key] = token
		return key
	})
	source = []byte(safe)
	document := goldmark.New().Parser().Parse(text.NewReader(source))
	rich := false
	err := ast.Walk(document, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch v := n.(type) {
		case *ast.Document, *ast.Paragraph, *ast.TextBlock, *ast.ListItem, *ast.Text, *ast.String:
		case *ast.Heading:
			if v.Level < 2 || v.Level > 4 {
				return ast.WalkStop, fmt.Errorf("%w: body heading must be level 2-4", ErrUnsupportedMarkdown)
			}
			rich = true
		case *ast.List, *ast.Emphasis:
			rich = true
		default:
			return ast.WalkStop, fmt.Errorf("%w: node %s", ErrUnsupportedMarkdown, n.Kind().String())
		}
		if v, ok := n.(*ast.Text); ok && v.HardLineBreak() {
			return ast.WalkStop, fmt.Errorf("%w: hard line break", ErrUnsupportedMarkdown)
		}
		return ast.WalkContinue, nil
	})
	if err != nil {
		return nil, err
	}
	replaceCitation := func(raw string) (string, error) {
		raw = markdownText(raw)
		for key, token := range placeholders {
			raw = strings.ReplaceAll(raw, key, token)
		}
		if err := sourcemarker.Validate(raw); err != nil {
			return "", fmt.Errorf("%w: malformed source marker", ErrUnsupportedMarkdown)
		}
		if citation == nil {
			return raw, nil
		}
		var failure error
		result := sourcemarker.ReplaceAll(raw, func(token, _ string) string {
			out, err := citation(token)
			if err != nil {
				failure = err
			}
			return out
		})
		return result, failure
	}
	if !rich {
		out := []paragraphSpec{}
		for _, line := range strings.Split(string(source), "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			line, err = replaceCitation(line)
			if err != nil {
				return nil, err
			}
			out = append(out, paragraphSpec{Text: line})
		}
		return out, nil
	}
	out := []paragraphSpec{}
	group := 0
	var blocks func(ast.Node, *listSpec) error
	blocks = func(parent ast.Node, list *listSpec) error {
		for n := parent.FirstChild(); n != nil; n = n.NextSibling() {
			switch v := n.(type) {
			case *ast.Paragraph, *ast.TextBlock, *ast.Heading:
				spec := paragraphSpec{}
				if h, ok := n.(*ast.Heading); ok {
					spec.Style = "Heading" + strconv.Itoa(h.Level)
				}
				if list != nil {
					value := *list
					spec.List = &value
					spec.ListContinuation = n.PreviousSibling() != nil
				}
				var inline func(ast.Node, bool, bool) error
				inline = func(node ast.Node, bold, italic bool) error {
					for child := node.FirstChild(); child != nil; child = child.NextSibling() {
						switch c := child.(type) {
						case *ast.Emphasis:
							if err := inline(child, bold || c.Level == 2, italic || c.Level == 1); err != nil {
								return err
							}
						case *ast.Text:
							value, err := replaceCitation(string(c.Segment.Value(source)))
							if err != nil {
								return err
							}
							if c.SoftLineBreak() {
								value += " "
							}
							spec.Runs = append(spec.Runs, runSpec{Text: value, Bold: bold, Italic: italic})
						case *ast.String:
							value, err := replaceCitation(string(c.Value))
							if err != nil {
								return err
							}
							spec.Runs = append(spec.Runs, runSpec{Text: value, Bold: bold, Italic: italic})
						default:
							return fmt.Errorf("%w: inline node", ErrUnsupportedMarkdown)
						}
					}
					return nil
				}
				if err := inline(n, false, false); err != nil {
					return err
				}
				for _, run := range spec.Runs {
					spec.Text += run.Text
				}
				out = append(out, spec)
			case *ast.List:
				group++
				level := 0
				if list != nil {
					level = list.Level + 1
				}
				if level > 8 {
					return fmt.Errorf("%w: list nesting", ErrUnsupportedMarkdown)
				}
				info := &listSpec{Ordered: v.IsOrdered(), Start: v.Start, Level: level, Group: group}
				for item := v.FirstChild(); item != nil; item = item.NextSibling() {
					if err := blocks(item, info); err != nil {
						return err
					}
				}
			default:
				return fmt.Errorf("%w: block node", ErrUnsupportedMarkdown)
			}
		}
		return nil
	}
	if err := blocks(document, nil); err != nil {
		return nil, err
	}
	return out, nil
}

func richParagraph(b *strings.Builder, spec paragraphSpec) {
	if len(spec.Runs) == 0 && spec.List == nil {
		paragraph(b, spec.Style, spec.Text)
		return
	}
	b.WriteString(`<w:p>`)
	if spec.Style != "" || spec.List != nil {
		b.WriteString(`<w:pPr>`)
		if spec.Style != "" {
			b.WriteString(`<w:pStyle w:val="` + spec.Style + `"/>`)
		}
		if spec.List != nil {
			if spec.ListContinuation {
				_, _ = fmt.Fprintf(b, `<w:ind w:left="%d"/>`, 720*(spec.List.Level+1)) // in-memory strings.Builder has no I/O errors
			} else {
				_, _ = fmt.Fprintf(b, `<w:numPr><w:ilvl w:val="%d"/><w:numId w:val="%d"/></w:numPr>`, spec.List.Level, spec.List.NumID) // in-memory strings.Builder has no I/O errors
			}
		}
		b.WriteString(`</w:pPr>`)
	}
	for _, run := range spec.Runs {
		b.WriteString(`<w:r>`)
		if run.Bold || run.Italic {
			b.WriteString(`<w:rPr>`)
			if run.Bold {
				b.WriteString(`<w:b/>`)
			}
			if run.Italic {
				b.WriteString(`<w:i/>`)
			}
			b.WriteString(`</w:rPr>`)
		}
		b.WriteString(`<w:t xml:space="preserve">`)
		_ = xml.EscapeText(b, []byte(run.Text))
		b.WriteString(`</w:t></w:r>`)
	}
	b.WriteString(`</w:p>`)
}

func richerStyles(base string) string {
	extra := ""
	for level := 2; level <= 4; level++ {
		extra += fmt.Sprintf(`<w:style w:type="paragraph" w:styleId="Heading%d"><w:name w:val="heading %d"/><w:basedOn w:val="Normal"/><w:pPr><w:outlineLvl w:val="%d"/></w:pPr><w:rPr><w:b/><w:sz w:val="%d"/></w:rPr></w:style>`, level, level, level-1, 28-level*2)
	}
	return strings.Replace(base, "</w:styles>", extra+"</w:styles>", 1)
}

func prepareNumbering(paragraphs []paragraphSpec) string {
	// OOXML requires all abstract definitions before the numbering instances.
	// Word repairs an interleaved part by conflating otherwise distinct lists.
	var abstract, instances strings.Builder
	serial := 0
	groups := map[int]int{}
	for i := range paragraphs {
		list := paragraphs[i].List
		if list == nil {
			if paragraphs[i].Style == "Heading1" {
				groups = map[int]int{}
			}
			continue
		}
		// Group indices restart per chapter; a chapter heading separates instances.
		if groups[list.Group] == 0 {
			serial++
			format, label := "bullet", "•"
			start := 1
			if list.Ordered {
				format, label = "decimal", "%"+strconv.Itoa(list.Level+1)+"."
				if list.Start > 0 {
					start = list.Start
				}
			}
			_, _ = fmt.Fprintf(&abstract, `<w:abstractNum w:abstractNumId="%d"><w:multiLevelType w:val="multilevel"/><w:lvl w:ilvl="%d"><w:start w:val="%d"/><w:numFmt w:val="%s"/><w:lvlText w:val="%s"/><w:lvlJc w:val="left"/><w:pPr><w:tabs><w:tab w:val="num" w:pos="%d"/></w:tabs><w:ind w:left="%d" w:hanging="360"/></w:pPr></w:lvl></w:abstractNum>`, serial, list.Level, start, format, label, 720*(list.Level+1), 720*(list.Level+1)) // in-memory strings.Builder has no I/O errors
			_, _ = fmt.Fprintf(&instances, `<w:num w:numId="%d"><w:abstractNumId w:val="%d"/></w:num>`, serial, serial)                                                                                                                                                                                                                                                                                                                          // in-memory strings.Builder has no I/O errors
			groups[list.Group] = serial
		}
		list.NumID = groups[list.Group]
	}
	if serial == 0 {
		return ""
	}
	return `<?xml version="1.0" encoding="UTF-8"?><w:numbering xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">` + abstract.String() + instances.String() + `</w:numbering>`
}

// Decode punctuation escapes and entities in one pass. Resolving entities after
// a blanket unescape would incorrectly turn \&amp; into & or double-decode.
func markdownText(raw string) string {
	var out strings.Builder
	for i := 0; i < len(raw); {
		if raw[i] == '\\' && i+1 < len(raw) && util.IsPunct(raw[i+1]) {
			out.WriteByte(raw[i+1])
			i += 2
			continue
		}
		if raw[i] == '&' {
			end := strings.IndexByte(raw[i:], ';')
			if end > 0 && end < 40 {
				token := raw[i : i+end+1]
				decoded := html.UnescapeString(token)
				if decoded != token {
					out.WriteString(decoded)
					i += end + 1
					continue
				}
			}
		}
		out.WriteByte(raw[i])
		i++
	}
	return out.String()
}

// Syntax guards distinguish unsupported markup from escaped literal typing.
func syntaxProbe(raw string) string {
	var out strings.Builder
	for i := 0; i < len(raw); i++ {
		if raw[i] == '\\' && i+1 < len(raw) && util.IsPunct(raw[i+1]) {
			out.WriteString("  ")
			i++
			continue
		}
		out.WriteByte(raw[i])
	}
	return out.String()
}
