package service

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestNativePDFPageSpansAcceptOnlyExactTextSnapshot(t *testing.T) {
	text := "第一页 α\n\nsecond page"
	hash := sha256.Sum256([]byte(text))
	result := &types.ReadResult{MarkdownContent: text, Metadata: map[string]string{
		"image_source_type": "pdf_text_layer", "scanned_page_count": "0", "page_count": "2",
		"lingdoc_pdf_text_sha256": hex.EncodeToString(hash[:]),
		"lingdoc_pdf_pages_v1":    `[{"page":1,"start":0,"end":5},{"page":2,"start":7,"end":18}]`,
	}}
	spans, digest := nativePDFPageSpans(result)
	if len(spans) != 2 || digest == "" {
		t.Fatalf("valid PDF snapshot rejected: spans=%v", spans)
	}
	if got := pdfPageForChunk(spans, []rune(text), 0, 5, "第一页 α"); got != 1 {
		t.Fatalf("first page = %d", got)
	}
	if got := pdfPageForChunk(spans, []rune(text), 7, 18, "second page"); got != 2 {
		t.Fatalf("second page = %d", got)
	}
	if got := pdfPageForChunk(spans, []rune(text), 3, 9, " α\n\nse"); got != 0 {
		t.Fatalf("cross-page chunk was assigned page %d", got)
	}
	if got := pdfPageForChunk(spans, []rune(text), 0, 5, "wrong"); got != 0 {
		t.Fatalf("mismatched chunk was assigned page %d", got)
	}
	for name, mutate := range map[string]func(*types.ReadResult){
		"changed text": func(r *types.ReadResult) { r.MarkdownContent += "x" },
		"scanned":      func(r *types.ReadResult) { r.Metadata["scanned_page_count"] = "1" },
		"images":       func(r *types.ReadResult) { r.ImageRefs = []types.ImageRef{{Filename: "x"}} },
		"forged page":  func(r *types.ReadResult) { r.Metadata["lingdoc_pdf_pages_v1"] = `[{"page":3,"start":0,"end":18}]` },
		"gap": func(r *types.ReadResult) {
			r.Metadata["lingdoc_pdf_pages_v1"] = `[{"page":1,"start":0,"end":4},{"page":2,"start":7,"end":18}]`
		},
	} {
		t.Run(name, func(t *testing.T) {
			clone := *result
			clone.Metadata = make(map[string]string, len(result.Metadata))
			for k, v := range result.Metadata {
				clone.Metadata[k] = v
			}
			mutate(&clone)
			if spans, _ := nativePDFPageSpans(&clone); len(spans) != 0 {
				t.Fatal("unverified metadata accepted")
			}
		})
	}
}
