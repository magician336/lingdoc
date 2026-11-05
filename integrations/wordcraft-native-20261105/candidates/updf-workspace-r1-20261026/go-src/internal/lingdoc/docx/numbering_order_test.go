package docx

import (
	"strings"
	"testing"
)

func TestNumberingDefinitionsPrecedeInstances(t *testing.T) {
	in := demoInput()
	in.Chapters[0].BodyMarkdown = "- bullet one\n- bullet two\n\n3. ordered three\n4. ordered four\n\n[[source:s-demo]]"
	file, err := Render(in)
	if err != nil {
		t.Fatal(err)
	}
	if err := Validate(in, file); err != nil {
		t.Fatal(err)
	}
	numbering := part(t, file, "word/numbering.xml")
	if strings.Count(numbering, "<w:abstractNum ") != 2 || strings.Count(numbering, "<w:num ") != 2 {
		t.Fatal("fixture must include two distinct lists")
	}
	firstInstance := strings.Index(numbering, "<w:num ")
	lastDefinition := strings.LastIndex(numbering, "<w:abstractNum ")
	if firstInstance < lastDefinition {
		t.Fatal("Word repairs interleaved lists and changes their numbering")
	}
	// Correct text and IDs are not enough: move a complete instance in front of
	// the definitions while leaving every paragraph and list property intact.
	end := strings.Index(numbering[firstInstance:], "</w:num>") + firstInstance + len("</w:num>")
	instance := numbering[firstInstance:end]
	rest := numbering[:firstInstance] + numbering[end:]
	insertion := strings.Index(rest, "<w:abstractNum ")
	invalid := rest[:insertion] + instance + rest[insertion:]
	tampered := repackage(t, file, map[string]string{"word/numbering.xml": invalid}, nil)
	if Validate(in, tampered) == nil {
		t.Fatal("validator accepted interleaved definitions and instances")
	}
}
