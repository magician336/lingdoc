package workspacecore

import (
	"context"
	"errors"
	"testing"
)

func TestPDFAnchorUsesVerifiedPageAndRejectsInventedLocation(t *testing.T) {
	f := newAgentFixture(t, false)
	source := f.source.sources["source-1"]
	source.VerifiedPDFPage = 3
	f.source.sources["source-1"] = source
	input := SaveSourceAnchorInput{VersionExpectation: f.expectation(0), SourceID: source.SourceID, SelectionEnd: len([]rune(source.QuotedText)), ExtractionType: "native_text", Confirmed: true}
	for name, change := range map[string]func(*SaveSourceAnchorInput){
		"wrong page":           func(i *SaveSourceAnchorInput) { i.Page = 2 },
		"invented section":     func(i *SaveSourceAnchorInput) { i.Section = "Unverified section" },
		"invented paragraph":   func(i *SaveSourceAnchorInput) { i.Paragraph = 1 },
		"invented coordinates": func(i *SaveSourceAnchorInput) { i.Coordinates = []float64{1, 2, 3, 4} },
		"invented OCR":         func(i *SaveSourceAnchorInput) { i.ExtractionType = "ocr" },
	} {
		t.Run(name, func(t *testing.T) {
			bad := input
			change(&bad)
			if _, _, _, err := f.svc.SaveSourceAnchor(context.Background(), f.owner, f.project.ID, "bad-"+name, bad); !errors.Is(err, ErrInvalidRequest) {
				t.Fatalf("expected invalid request, got %v", err)
			}
		})
	}
	raw, _, _, err := f.svc.SaveSourceAnchor(context.Background(), f.owner, f.project.ID, "verified-page", input)
	anchor := decodeAgentJSON[SourceAnchor](t, raw, err)
	if anchor.Page != 3 || len(anchor.Coordinates) != 0 {
		t.Fatalf("unexpected PDF locator: %+v", anchor)
	}
}
