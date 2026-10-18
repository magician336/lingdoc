package docx

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"strconv"
)

type valAttr struct {
	Value string `xml:"val,attr"`
}
type xmlRun struct {
	Properties struct {
		Bold   *valAttr `xml:"b"`
		Italic *valAttr `xml:"i"`
	} `xml:"rPr"`
	Text []string `xml:"t"`
}
type xmlParagraph struct {
	Properties struct {
		Style     valAttr `xml:"pStyle"`
		Numbering *struct {
			ID    valAttr `xml:"numId"`
			Level valAttr `xml:"ilvl"`
		} `xml:"numPr"`
		Indent *struct {
			Left string `xml:"left,attr"`
		} `xml:"ind"`
	} `xml:"pPr"`
	Runs []xmlRun `xml:"r"`
}

func readPackagePart(file []byte, name string) ([]byte, error) {
	z, err := zip.NewReader(bytes.NewReader(file), int64(len(file)))
	if err != nil {
		return nil, err
	}
	for _, f := range z.File {
		if f.Name == name {
			r, err := f.Open()
			if err != nil {
				return nil, err
			}
			data, readErr := io.ReadAll(r)
			closeErr := r.Close()
			if readErr != nil {
				return nil, readErr
			}
			if closeErr != nil {
				return nil, closeErr
			}
			return data, nil
		}
	}
	return nil, fmt.Errorf("missing format part %s", name)
}
func enabledMark(p *valAttr) bool {
	return p != nil && p.Value != "0" && p.Value != "false" && p.Value != "off"
}

// Parse the actual OOXML independently. Matching text alone would miss a
// lost bold run, a heading downgraded to Normal, or a numbered list flattened.
func validateRichStructure(file []byte, expected []paragraphSpec) error {
	if err := validateFormatLink(file, "styles.xml", "styles", "styles+xml"); err != nil {
		return err
	}
	prepareNumbering(expected)
	var document struct {
		Paragraphs []xmlParagraph `xml:"body>p"`
	}
	raw, err := readPackagePart(file, documentPart)
	if err != nil {
		return err
	}
	if err := xml.Unmarshal(raw, &document); err != nil {
		return err
	}
	if len(document.Paragraphs) != len(expected) {
		return fmt.Errorf("rich paragraph structure changed")
	}
	var styles struct {
		Items []struct {
			ID string `xml:"styleId,attr"`
		} `xml:"style"`
	}
	raw, err = readPackagePart(file, "word/styles.xml")
	if err != nil {
		return err
	}
	if err := xml.Unmarshal(raw, &styles); err != nil {
		return err
	}
	declaredStyles := map[string]bool{}
	for _, style := range styles.Items {
		declaredStyles[style.ID] = true
	}
	lists := map[int]*listSpec{}
	for i, spec := range expected {
		actual := document.Paragraphs[i]
		if actual.Properties.Style.Value != spec.Style || (spec.Style != "" && !declaredStyles[spec.Style]) {
			return fmt.Errorf("paragraph %d heading/style lost", i+1)
		}
		runs := spec.Runs
		if len(runs) == 0 {
			runs = []runSpec{{Text: spec.Text}}
		}
		if len(actual.Runs) != len(runs) {
			return fmt.Errorf("paragraph %d run structure changed", i+1)
		}
		for j, run := range runs {
			var value string
			for _, piece := range actual.Runs[j].Text {
				value += piece
			}
			if value != run.Text || enabledMark(actual.Runs[j].Properties.Bold) != run.Bold || enabledMark(actual.Runs[j].Properties.Italic) != run.Italic {
				return fmt.Errorf("paragraph %d run %d text/emphasis lost", i+1, j+1)
			}
		}
		numbering := actual.Properties.Numbering
		if spec.List == nil || spec.ListContinuation {
			if numbering != nil {
				return fmt.Errorf("paragraph %d invented list item", i+1)
			}
			if spec.ListContinuation && (actual.Properties.Indent == nil || actual.Properties.Indent.Left != strconv.Itoa(720*(spec.List.Level+1))) {
				return fmt.Errorf("list continuation indentation lost")
			}
		} else {
			if numbering == nil || numbering.ID.Value != strconv.Itoa(spec.List.NumID) || numbering.Level.Value != strconv.Itoa(spec.List.Level) {
				return fmt.Errorf("paragraph %d list numbering lost", i+1)
			}
			lists[spec.List.NumID] = spec.List
		}
	}
	if len(lists) == 0 {
		return nil
	}
	if err := validateFormatLink(file, "numbering.xml", "numbering", "numbering+xml"); err != nil {
		return err
	}
	var numbering struct {
		Abstract []struct {
			ID     int `xml:"abstractNumId,attr"`
			Levels []struct {
				Level  int     `xml:"ilvl,attr"`
				Start  valAttr `xml:"start"`
				Format valAttr `xml:"numFmt"`
				Text   valAttr `xml:"lvlText"`
			} `xml:"lvl"`
		} `xml:"abstractNum"`
		Numbers []struct {
			ID       int     `xml:"numId,attr"`
			Abstract valAttr `xml:"abstractNumId"`
		} `xml:"num"`
	}
	raw, err = readPackagePart(file, "word/numbering.xml")
	if err != nil {
		return err
	}
	if err := validateNumberingOrder(raw); err != nil {
		return err
	}
	if err := xml.Unmarshal(raw, &numbering); err != nil {
		return err
	}
	for id, list := range lists {
		found := false
		for _, num := range numbering.Numbers {
			if num.ID != id {
				continue
			}
			abstractID, err := strconv.Atoi(num.Abstract.Value)
			if err != nil {
				return err
			}
			for _, abstract := range numbering.Abstract {
				if abstract.ID != abstractID {
					continue
				}
				for _, level := range abstract.Levels {
					if level.Level != list.Level {
						continue
					}
					format, label, start := "bullet", "•", 1
					if list.Ordered {
						format, label = "decimal", "%"+strconv.Itoa(list.Level+1)+"."
						if list.Start > 0 {
							start = list.Start
						}
					}
					if level.Format.Value == format && level.Text.Value == label && level.Start.Value == strconv.Itoa(start) {
						found = true
					}
				}
			}
		}
		if !found {
			return fmt.Errorf("list %d format/start lost", id)
		}
	}
	return nil
}

func validateFormatLink(file []byte, target, kind, content string) error {
	var rels struct {
		Items []struct {
			Type   string `xml:"Type,attr"`
			Target string `xml:"Target,attr"`
			Mode   string `xml:"TargetMode,attr"`
		} `xml:"Relationship"`
	}
	raw, err := readPackagePart(file, "word/_rels/document.xml.rels")
	if err != nil {
		return err
	}
	if err := xml.Unmarshal(raw, &rels); err != nil {
		return err
	}
	linked := 0
	for _, rel := range rels.Items {
		if rel.Type == "http://schemas.openxmlformats.org/officeDocument/2006/relationships/"+kind && rel.Target == target && rel.Mode == "" {
			linked++
		}
	}
	if linked != 1 {
		return fmt.Errorf("missing/ambiguous Word %s relationship", kind)
	}
	var types struct {
		Items []struct {
			Part string `xml:"PartName,attr"`
			Type string `xml:"ContentType,attr"`
		} `xml:"Override"`
	}
	raw, err = readPackagePart(file, "[Content_Types].xml")
	if err != nil {
		return err
	}
	if err := xml.Unmarshal(raw, &types); err != nil {
		return err
	}
	registered := 0
	for _, entry := range types.Items {
		if entry.Part == "/word/"+target && entry.Type == "application/vnd.openxmlformats-officedocument.wordprocessingml."+content {
			registered++
		}
	}
	if registered != 1 {
		return fmt.Errorf("missing/ambiguous Word %s content type", kind)
	}
	return nil
}

// A part with correct IDs but interleaved definitions/instances is not valid
// WordprocessingML. Word can silently merge the lists during its repair.
func validateNumberingOrder(raw []byte) error {
	decoder := xml.NewDecoder(bytes.NewReader(raw))
	depth, seenInstance := 0, false
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		switch t := token.(type) {
		case xml.StartElement:
			depth++
			if depth == 2 {
				if t.Name.Local == "num" {
					seenInstance = true
				}
				if t.Name.Local == "abstractNum" && seenInstance {
					return fmt.Errorf("numbering definition appears after an instance")
				}
			}
		case xml.EndElement:
			depth--
		}
	}
}
