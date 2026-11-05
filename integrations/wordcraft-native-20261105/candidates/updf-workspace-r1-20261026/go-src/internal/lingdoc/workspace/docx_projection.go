package workspace

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"strings"
)

const (
	DOCXTemplateID          = "nsfc2026-annual-application"
	DOCXAdapterVersion      = "nsfc2026-r4-adapter-v1"
	DOCXProjectionAlgorithm = "docxGoProjection/1"
)

type docxSectionSpec struct {
	ID       string
	Token    string
	Guidance string
}

var docxSectionSpecs = []docxSectionSpec{
	{ID: "topic-summary", Token: "[选题说明]", Guidance: "[选题说明]  选题所研究的具体问题、研究视角和核心概念（300字以内）。"},
	{ID: "topic-basis", Token: "[选题依据]", Guidance: "[选题依据]  国内外相关研究的学术史梳理及研究进展（略写）；相对于已有研究特别是国家社科基金同类项目的独到学术价值和应用价值。"},
	{ID: "research-content", Token: "[研究内容]", Guidance: "[研究内容]  课题研究的主要目标、重点难点、整体框架、研究计划及其可行性等。（框架思路要列出提纲或目录）"},
	{ID: "innovation", Token: "[创新之处]", Guidance: "[创新之处]  在学术观点、研究方法等方面的特色和创新。"},
	{ID: "expected-results", Token: "[预期成果]", Guidance: "[预期成果]  成果形式、宣传转化及预期学术价值和社会效益等。（略写）"},
	{ID: "references", Token: "[参考文献]", Guidance: "[参考文献]  开展本课题研究的主要中外参考文献。（略写）"},
}

type DOCXProjection struct {
	TemplateID           string                 `json:"templateId"`
	AdapterVersion       string                 `json:"adapterVersion"`
	AlgorithmVersion     string                 `json:"algorithmVersion"`
	ManifestHash         string                 `json:"manifestHash"`
	GlobalDependencyHash string                 `json:"globalDependencyHash"`
	Sections             []DOCXProjectedChapter `json:"sections"`
}

type DOCXProjectedChapter struct {
	SectionID           string   `json:"sectionId"`
	Order               int      `json:"order"`
	AnchorToken         string   `json:"anchorToken"`
	GuidanceText        string   `json:"guidanceText"`
	Body                string   `json:"bodyText"`
	BodySHA256          string   `json:"bodyTextSha256"`
	Paragraphs          []string `json:"paragraphs"`
	SectionDeliveryHash string   `json:"sectionDeliveryHash"`
}

type xmlNode struct {
	XMLName  xml.Name
	Attrs    []xml.Attr `xml:",any,attr"`
	Children []xmlNode  `xml:",any"`
	Text     string     `xml:",chardata"`
}

func (n *xmlNode) local() string { return n.XMLName.Local }
func (n *xmlNode) text() string {
	var out strings.Builder
	var walk func(*xmlNode)
	walk = func(node *xmlNode) {
		if node.local() == "t" || node.local() == "tab" || node.local() == "br" {
			if node.local() == "t" {
				out.WriteString(node.Text)
			} else if node.local() == "tab" {
				out.WriteByte('\t')
			} else {
				out.WriteByte('\n')
			}
		}
		for i := range node.Children {
			walk(&node.Children[i])
		}
	}
	walk(n)
	return out.String()
}
func (n *xmlNode) descendants(local string) []*xmlNode {
	var out []*xmlNode
	var walk func(*xmlNode)
	walk = func(node *xmlNode) {
		if node.local() == local {
			out = append(out, node)
		}
		for i := range node.Children {
			walk(&node.Children[i])
		}
	}
	walk(n)
	return out
}
func (n *xmlNode) direct(local string) []*xmlNode {
	out := []*xmlNode{}
	for i := range n.Children {
		if n.Children[i].local() == local {
			out = append(out, &n.Children[i])
		}
	}
	return out
}

func parseDOCXProjection(data []byte) (DOCXProjection, error) {
	var empty DOCXProjection
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return empty, fmt.Errorf("STRUCTURE_INVALID: invalid DOCX zip: %w", err)
	}
	parts := map[string][]byte{}
	for _, file := range reader.File {
		if file.FileInfo().IsDir() || file.UncompressedSize64 > 32<<20 {
			continue
		}
		switch file.Name {
		case "word/document.xml", "word/styles.xml", "word/numbering.xml", "word/settings.xml":
			stream, err := file.Open()
			if err != nil {
				return empty, err
			}
			content, err := io.ReadAll(io.LimitReader(stream, 32<<20))
			closeErr := stream.Close()
			if err != nil {
				return empty, err
			}
			if closeErr != nil {
				return empty, closeErr
			}
			parts[file.Name] = content
		}
	}
	if len(parts["word/document.xml"]) == 0 {
		return empty, errors.New("STRUCTURE_INVALID: word/document.xml missing")
	}
	var root xmlNode
	if err := xml.Unmarshal(parts["word/document.xml"], &root); err != nil {
		return empty, fmt.Errorf("STRUCTURE_INVALID: %w", err)
	}
	var module *xmlNode
	for _, table := range root.descendants("tbl") {
		rows, cells := table.direct("tr"), []*xmlNode{}
		if len(rows) == 1 {
			cells = rows[0].direct("tc")
		}
		if len(rows) != 1 || len(cells) != 1 {
			continue
		}
		text := cells[0].text()
		valid := true
		for _, spec := range docxSectionSpecs {
			if strings.Count(text, spec.Token) != 1 {
				valid = false
				break
			}
		}
		if valid {
			if module != nil {
				return empty, errors.New("STRUCTURE_INVALID:anchor_ambiguous")
			}
			module = cells[0]
		}
	}
	if module == nil {
		return empty, errors.New("STRUCTURE_INVALID:anchor_missing")
	}
	paragraphs := module.descendants("p")
	texts := make([]string, len(paragraphs))
	anchors := make([]int, len(docxSectionSpecs))
	for i := range paragraphs {
		texts[i] = paragraphs[i].text()
	}
	for i, spec := range docxSectionSpecs {
		anchors[i] = -1
		for p, text := range texts {
			if strings.Contains(text, spec.Token) {
				if !strings.HasPrefix(text, spec.Token) {
					return empty, errors.New("STRUCTURE_INVALID:anchor_ambiguous")
				}
				if anchors[i] >= 0 {
					return empty, errors.New("STRUCTURE_INVALID:anchor_duplicate")
				}
				anchors[i] = p
			}
		}
		if anchors[i] < 0 {
			return empty, errors.New("STRUCTURE_INVALID:anchor_missing")
		}
		if i > 0 && anchors[i] <= anchors[i-1] {
			return empty, errors.New("STRUCTURE_INVALID:anchor_order_invalid")
		}
		if !strings.HasPrefix(texts[anchors[i]], spec.Guidance) {
			return empty, errors.New("STRUCTURE_INVALID:unsupported_template")
		}
	}
	projection := DOCXProjection{TemplateID: DOCXTemplateID, AdapterVersion: DOCXAdapterVersion, AlgorithmVersion: DOCXProjectionAlgorithm, Sections: []DOCXProjectedChapter{}}
	dependency := sha256.New()
	for _, name := range []string{"word/styles.xml", "word/numbering.xml", "word/settings.xml"} {
		_, _ = dependency.Write(parts[name])
	}
	projection.GlobalDependencyHash = hex.EncodeToString(dependency.Sum(nil))
	for i, spec := range docxSectionSpecs {
		start, end := anchors[i], len(paragraphs)
		if i+1 < len(anchors) {
			end = anchors[i+1]
		}
		bodyParagraphs := []string{strings.TrimPrefix(texts[start], spec.Guidance)}
		bodyParagraphs = append(bodyParagraphs, texts[start+1:end]...)
		body := strings.Join(bodyParagraphs, "\n")
		bodySum := sha256.Sum256([]byte(body))
		chapter := DOCXProjectedChapter{SectionID: spec.ID, Order: i + 1, AnchorToken: spec.Token, GuidanceText: spec.Guidance, Body: body, BodySHA256: hex.EncodeToString(bodySum[:]), Paragraphs: bodyParagraphs}
		canonical, err := json.Marshal(struct {
			Algorithm, Template, Adapter, Section, Guidance, Dependency string
			Paragraphs                                                  []string
		}{DOCXProjectionAlgorithm, DOCXTemplateID, DOCXAdapterVersion, spec.ID, spec.Guidance, projection.GlobalDependencyHash, bodyParagraphs})
		if err != nil {
			return empty, err
		}
		sum := sha256.Sum256(canonical)
		chapter.SectionDeliveryHash = hex.EncodeToString(sum[:])
		projection.Sections = append(projection.Sections, chapter)
	}
	manifest, err := json.Marshal(struct {
		Template, Adapter, Algorithm string
		Sections                     []DOCXProjectedChapter
	}{DOCXTemplateID, DOCXAdapterVersion, DOCXProjectionAlgorithm, projection.Sections})
	if err != nil {
		return empty, err
	}
	// The structural hash deliberately excludes all body content and delivery hashes.
	var structure []any
	for _, section := range projection.Sections {
		structure = append(structure, []any{section.SectionID, section.Order, section.AnchorToken, section.GuidanceText})
	}
	manifest, err = json.Marshal(struct {
		Template, Adapter, Algorithm string
		Sections                     []any
	}{DOCXTemplateID, DOCXAdapterVersion, DOCXProjectionAlgorithm, structure})
	if err != nil {
		return empty, err
	}
	manifestSum := sha256.Sum256(manifest)
	projection.ManifestHash = hex.EncodeToString(manifestSum[:])
	return projection, nil
}
