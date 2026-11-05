package workspace

import "github.com/Tencent/WeKnora/internal/lingdoc/generation"

// Only gong-authorized fields from the explicitly selected, already validated
// card snapshots may enter model JSON. Full cards, claims and templates do not.
func modelEvidencePayload(refs []generation.EvidenceCardRef, constraints []generation.EvidenceConstraint, sourceIDs []string) ([]generation.EvidenceConstraint, error) {
	if len(refs) == 0 && len(constraints) == 0 {
		return nil, nil
	}
	if len(refs) != len(constraints) {
		return nil, generation.ErrSourceAccessDenied
	}
	allowed := map[string]int64{}
	for _, ref := range refs {
		if ref.ID == "" || ref.Version < 1 || allowed[ref.ID] != 0 {
			return nil, generation.ErrSourceAccessDenied
		}
		allowed[ref.ID] = ref.Version
	}
	sources := map[string]bool{}
	for _, id := range sourceIDs {
		sources[id] = true
	}
	seen := map[string]bool{}
	out := make([]generation.EvidenceConstraint, 0, len(constraints))
	for _, card := range constraints {
		if allowed[card.CardRef.ID] != card.CardRef.Version || seen[card.CardRef.ID] || card.Scope == "" || len(card.SourceIDs) == 0 {
			return nil, generation.ErrSourceAccessDenied
		}
		if card.SupportType != "supports" && card.SupportType != "refutes" && card.SupportType != "qualifies" && card.SupportType != "background" {
			return nil, generation.ErrSourceAccessDenied
		}
		for _, id := range card.SourceIDs {
			if !sources[id] {
				return nil, generation.ErrSourceAccessDenied
			}
		}
		seen[card.CardRef.ID] = true
		out = append(out, generation.EvidenceConstraint{CardRef: card.CardRef, SupportType: card.SupportType, Scope: card.Scope, Limitations: card.Limitations, SourceIDs: append([]string(nil), card.SourceIDs...)})
	}
	return out, nil
}
