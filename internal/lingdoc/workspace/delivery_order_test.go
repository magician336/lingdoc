package workspace

import (
	"errors"
	"github.com/Tencent/WeKnora/internal/lingdoc/candidateadoption"
	"github.com/Tencent/WeKnora/internal/lingdoc/delivery"
	lingdoctemplate "github.com/Tencent/WeKnora/internal/lingdoc/template"
	"reflect"
	"testing"
)

func TestFrozenChaptersUseExplicitTemplateOrderWithoutMutatingInput(t *testing.T) {
	input := []candidateadoption.WorkspaceDeliveryChapter{{ChapterID: "m", SectionID: "method", Title: "方法", BodyMarkdown: "方法正文"}, {ChapterID: "q", SectionID: "question", Title: "问题", BodyMarkdown: "问题正文"}}
	template := lingdoctemplate.Template{Sections: []lingdoctemplate.Section{{ID: "method", Order: 20}, {ID: "question", Order: 10}}}
	out, err := frozenChaptersInTemplateOrder(input, template)
	if err != nil {
		t.Fatal(err)
	}
	if out[0].SectionID != "question" || out[1].SectionID != "method" {
		t.Fatalf("Order is not template Order: %#v", out)
	}
	if input[0].SectionID != "method" || template.Sections[0].ID != "method" {
		t.Fatal("Original input or template was mutated")
	}
	template.Sections[0].Order = 1
	out, err = frozenChaptersInTemplateOrder(input, template)
	if err != nil || out[0].SectionID != "method" {
		t.Fatalf("Custom order not respected: %#v, %v", out, err)
	}
}

func TestFrozenChapterLegacyTieUsesTemplateArrayOrderAndMissingChapterRemainsDiagnostic(t *testing.T) {
	input := []candidateadoption.WorkspaceDeliveryChapter{{ChapterID: "m", SectionID: "method"}, {ChapterID: "q", SectionID: "question"}}
	template := lingdoctemplate.Template{Sections: []lingdoctemplate.Section{{ID: "question"}, {ID: "method"}}}
	out, err := frozenChaptersInTemplateOrder(input, template)
	if err != nil || out[0].SectionID != "question" {
		t.Fatalf("Legacy stable template order lost: %#v %v", out, err)
	}
	out, err = frozenChaptersInTemplateOrder(input[:1], template)
	if err != nil || len(out) != 1 {
		t.Fatalf("Missing required chapter must reach existing check diagnostics: %#v %v", out, err)
	}
}

func TestFrozenChapterUnknownSectionIsRefusedWithoutDiscardingBody(t *testing.T) {
	input := []candidateadoption.WorkspaceDeliveryChapter{{ChapterID: "unknown", SectionID: "unknown", BodyMarkdown: "保留正文"}}
	_, err := frozenChaptersInTemplateOrder(input, lingdoctemplate.Template{Sections: []lingdoctemplate.Section{{ID: "question"}}})
	if !errors.Is(err, candidateadoption.ErrInvalidState) {
		t.Fatalf("Unknown section not refused: %v", err)
	}
	if input[0].BodyMarkdown != "保留正文" {
		t.Fatal("Unknown source body discarded")
	}
}

func TestExistingFrozenDocumentDoesNotResortOldChapterOrder(t *testing.T) {
	frozen := delivery.DeliveryInput{ProjectName: "历史稿", DeliveryKind: delivery.DeliveryKindInternalDemo, Template: lingdoctemplate.Template{Sections: []lingdoctemplate.Section{{ID: "question", Order: 1}, {ID: "method", Order: 2}}}, Chapters: []delivery.SnapshotChapter{{ChapterID: "m", SectionID: "method", Title: "方法", BodyMarkdown: "旧方法", Confirmation: &delivery.Confirmation{}}, {ChapterID: "q", SectionID: "question", Title: "问题", BodyMarkdown: "旧问题", Confirmation: &delivery.Confirmation{}}}}
	before := frozen.Chapters[0].SectionID
	document, err := documentOf(frozen)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual([]string{document.Chapters[0].Title, document.Chapters[1].Title}, []string{"方法", "问题"}) || frozen.Chapters[0].SectionID != before {
		t.Fatal("Historical frozen chapter order changed")
	}
}
