package generation

import (
	"encoding/json"
	"sort"
	"unicode/utf8"

	"github.com/Tencent/WeKnora/internal/lingdoc/operationcore"
	"github.com/Tencent/WeKnora/internal/lingdoc/writingaction"
)

// RunInputSummary is the readable summary of the input a run was started from.
//
// A run keeps its whole input in the store (generation_runs.InputJSON) so a
// retry can be executed from the same basis. This is the part a caller may
// read back: which chapter, which assets and cards, which revision. The
// instruction text itself is not echoed -- only its length and a digest, so
// two runs can be compared without the raw text leaving the service.
type RunInputSummary struct {
	// ChapterID is the chapter the run was asked to write.
	ChapterID string `json:"chapter_id"`
	// AssetIDs are the assets the run was allowed to read.
	AssetIDs []string `json:"asset_ids"`
	// EvidenceCardIDs are the evidence cards the run was allowed to read.
	EvidenceCardIDs []string `json:"evidence_card_ids"`
	// SpecRevision is the project specification revision the run was bound to.
	SpecRevision int `json:"spec_revision"`
	// ChapterVersionID is the chapter version the run was bound to.
	ChapterVersionID *string `json:"chapter_version_id"`
	// InstructionRunes is the length of the instruction, in runes.
	InstructionRunes int `json:"instruction_runes"`
	// InstructionHash identifies the instruction without revealing it.
	InstructionHash string `json:"instruction_hash"`
	// WritingAction is the action this run was started with.
	//
	// It is absent on the legacy path: that path is driven by a free text
	// instruction and records no action.
	WritingAction writingaction.Action `json:"writing_action,omitempty"`
	// Scope is the target range of that action. It is absent on the legacy
	// path, and absent when the action declares the whole chapter by default.
	Scope writingaction.Scope `json:"scope,omitempty"`
}

// inputSummaryOf builds the summary of one captured run input.
//
// It reports what the run was started from, never the instruction text: a
// failed run is read back by people, and the instruction can carry the
// research question the project has not published yet.
func inputSummaryOf(input Input) *RunInputSummary {
	summary := &RunInputSummary{
		ChapterID:        input.Request.ChapterID,
		SpecRevision:     input.Request.ExpectedSpecRevision,
		ChapterVersionID: input.Request.ExpectedChapterVersionID,
		InstructionRunes: utf8.RuneCountInString(input.Request.Instruction),
		WritingAction:    candidateAction(input.Request),
	}
	if input.Request.Scope != nil {
		summary.Scope = *input.Request.Scope
	}
	assets := make([]string, 0, len(input.Request.AssetIDs))
	assets = append(assets, input.Request.AssetIDs...)
	summary.AssetIDs = sortedUnique(assets)
	cards := make([]string, 0, len(input.Request.EvidenceCardRefs))
	for _, ref := range input.Request.EvidenceCardRefs {
		cards = append(cards, ref.ID)
	}
	summary.EvidenceCardIDs = sortedUnique(cards)
	if digest, err := operationcore.RequestHash(input.Request.Instruction); err == nil {
		summary.InstructionHash = digest
	}
	return summary
}

// inputSummaryFromJSON reads the summary back out of a stored run input.
//
// A row whose input cannot be read reports no summary rather than a partial
// one: half an input summary is worse than none, because the caller would
// read it as "this run only used these assets".
func inputSummaryFromJSON(raw string) *RunInputSummary {
	if raw == "" {
		return nil
	}
	var input Input
	if err := json.Unmarshal([]byte(raw), &input); err != nil {
		return nil
	}
	return inputSummaryOf(input)
}

// sortedUnique returns the sorted, de-duplicated copy of the given ids.
func sortedUnique(ids []string) []string {
	result := append([]string(nil), ids...)
	sort.Strings(result)
	unique := make([]string, 0, len(result))
	for _, id := range result {
		if id == "" || (len(unique) > 0 && unique[len(unique)-1] == id) {
			continue
		}
		unique = append(unique, id)
	}
	return unique
}
