package service

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/Tencent/WeKnora/internal/types"
)

// nativePDFPageSpans accepts only a complete, unmodified parser text snapshot.
// Coordinates are rune offsets so they match the document chunker. Metadata
// without proof, OCR, image output and malformed spans all remain weak sources.
func nativePDFPageSpans(result *types.ReadResult) ([]types.PDFPageSpan, string) {
	if result == nil || len(result.ImageRefs) != 0 || result.Metadata["image_source_type"] != "pdf_text_layer" || result.Metadata["scanned_page_count"] != "0" || strings.Contains(result.MarkdownContent, "\r") || !utf8.ValidString(result.MarkdownContent) {
		return nil, ""
	}
	text := result.MarkdownContent
	sum := sha256.Sum256([]byte(text))
	textHash := hex.EncodeToString(sum[:])
	if result.Metadata["lingdoc_pdf_text_sha256"] != textHash {
		return nil, ""
	}
	encoded := result.Metadata["lingdoc_pdf_pages_v1"]
	if len(encoded) == 0 || len(encoded) > 1<<20 {
		return nil, ""
	}
	var spans []types.PDFPageSpan
	if json.Unmarshal([]byte(encoded), &spans) != nil || len(spans) == 0 || len(spans) > 10000 {
		return nil, ""
	}
	runes := []rune(text)
	pageCount, err := strconv.Atoi(result.Metadata["page_count"])
	if err != nil || pageCount < 1 || pageCount > 10000 {
		return nil, ""
	}
	prevEnd, prevPage := 0, 0
	for i, span := range spans {
		if span.Page <= prevPage || span.Page > pageCount || span.Start < 0 || span.End <= span.Start || span.End > len(runes) {
			return nil, ""
		}
		if i == 0 && span.Start != 0 || i > 0 && (span.Start != prevEnd+2 || string(runes[prevEnd:span.Start]) != "\n\n") {
			return nil, ""
		}
		prevPage, prevEnd = span.Page, span.End
	}
	if prevEnd != len(runes) {
		return nil, ""
	}
	return spans, textHash
}

func pdfPageForChunk(spans []types.PDFPageSpan, indexed []rune, start, end int, content string) int {
	if start < 0 || end <= start || end > len(indexed) || string(indexed[start:end]) != content {
		return 0
	}
	for _, span := range spans {
		if start >= span.Start && end <= span.End {
			return span.Page
		}
	}
	return 0
}
