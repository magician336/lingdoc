package candidateadoption

import (
	"slices"
	"strings"

	"github.com/Tencent/WeKnora/internal/lingdoc/sourcemarker"
	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
)

// ClaimEvidence is a model-declared consecutive portion of the draft. A
// verified reference records provenance; it does not assert scientific truth.
type ClaimEvidence struct {
	Text       string                   `json:"text"`
	References []ClaimEvidenceReference `json:"references"`
}

type ClaimEvidenceReference struct {
	CardID         string `json:"card_id"`
	CardVersion    int64  `json:"card_version"`
	AnchorID       string `json:"anchor_id"`
	AnchorVersion  int64  `json:"anchor_version"`
	SourceID       string `json:"source_id"`
	Locator        string `json:"locator,omitempty"`
	SelectionStart int    `json:"selection_start"`
	SelectionEnd   int    `json:"selection_end"`
	QuotedTextHash string `json:"quoted_text_hash,omitempty"`
}

// ResolveClaimEvidence uses only model-declared identities to look up exact
// preauthorized anchors; locator and offsets are always populated by server.
func ResolveClaimEvidence(body string, claims []ClaimEvidence, refs []core.VersionRef, allowed []ClaimEvidenceReference, sources []string) ([]ClaimEvidence, error) {
	if len(refs) == 0 {
		if len(claims) > 0 {
			return nil, ErrInvalidRequest
		}
		return nil, nil
	}
	if len(claims) == 0 || len(claims) > 256 || len(body) > 200000 {
		return nil, ErrCandidateStale
	}
	covered := strings.Builder{}
	usedCards := map[string]bool{}
	result := make([]ClaimEvidence, 0, len(claims))
	for _, claim := range claims {
		if strings.TrimSpace(claim.Text) == "" || len(claim.References) == 0 || len(claim.References) > 32 || sourcemarker.Validate(claim.Text) != nil {
			return nil, ErrCandidateStale
		}
		covered.WriteString(claim.Text)
		marked := sourcemarker.IDs(claim.Text)
		if len(marked) == 0 {
			return nil, ErrCandidateStale
		}
		seenSources := map[string]bool{}
		seenPairs := map[string]bool{}
		item := ClaimEvidence{Text: claim.Text}
		for _, proposed := range claim.References {
			index := slices.IndexFunc(allowed, func(a ClaimEvidenceReference) bool {
				return a.CardID == proposed.CardID && a.CardVersion == proposed.CardVersion && a.AnchorID == proposed.AnchorID && a.AnchorVersion == proposed.AnchorVersion && a.SourceID == proposed.SourceID
			})
			if index < 0 || !slices.Contains(marked, proposed.SourceID) || !slices.Contains(sources, proposed.SourceID) {
				return nil, ErrCandidateStale
			}
			actual := allowed[index]
			if actual.CardID == "" || actual.CardVersion < 1 || actual.AnchorID == "" || actual.AnchorVersion < 1 || actual.Locator == "" || actual.QuotedTextHash == "" || actual.SelectionStart < 0 || actual.SelectionEnd <= actual.SelectionStart {
				return nil, ErrCandidateStale
			}
			key := actual.CardID + "\x00" + actual.AnchorID
			if seenPairs[key] {
				return nil, ErrCandidateStale
			}
			seenPairs[key] = true
			if !slices.ContainsFunc(refs, func(r core.VersionRef) bool { return r.ID == actual.CardID && r.Version == actual.CardVersion }) {
				return nil, ErrCandidateStale
			}
			usedCards[actual.CardID] = true
			seenSources[actual.SourceID] = true
			item.References = append(item.References, actual)
		}
		for _, id := range marked {
			if !seenSources[id] {
				return nil, ErrCandidateStale
			}
		}
		result = append(result, item)
	}
	if covered.String() != body {
		return nil, ErrCandidateStale
	}
	for _, ref := range refs {
		if !usedCards[ref.ID] {
			return nil, ErrCandidateStale
		}
	}
	return result, nil
}

func selectionClaimReferences(selected core.EvidenceSelection) []ClaimEvidenceReference {
	out := []ClaimEvidenceReference{}
	for _, card := range selected.Cards {
		for _, ref := range card.AnchorRefs {
			for _, anchor := range selected.Anchors {
				if anchor.ID != ref.ID || anchor.Version != ref.Version || !slices.Contains(card.SourceIDs, anchor.SourceID) {
					continue
				}
				out = append(out, ClaimEvidenceReference{CardID: card.ID, CardVersion: card.Version, AnchorID: anchor.ID, AnchorVersion: anchor.Version, SourceID: anchor.SourceID, Locator: anchor.Locator, SelectionStart: anchor.SelectionStart, SelectionEnd: anchor.SelectionEnd, QuotedTextHash: anchor.QuotedTextHash})
			}
		}
	}
	return out
}

func validateClaimEvidence(candidate Candidate, selected core.EvidenceSelection) error {
	resolved, err := ResolveClaimEvidence(candidate.BodyMarkdown, candidate.Claims, candidate.Basis.EvidenceCardRefs, selectionClaimReferences(selected), candidate.SourceIDs)
	if err != nil {
		return err
	}
	if !slices.EqualFunc(resolved, candidate.Claims, func(a, b ClaimEvidence) bool { return a.Text == b.Text && slices.Equal(a.References, b.References) }) {
		return ErrCandidateStale
	}
	return nil
}
