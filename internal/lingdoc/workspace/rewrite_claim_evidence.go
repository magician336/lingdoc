package workspace

import (
	"github.com/Tencent/WeKnora/internal/lingdoc/candidateadoption"
	"github.com/Tencent/WeKnora/internal/lingdoc/generation"
	"slices"
)

func resolveRewriteClaims(body string, claims []candidateadoption.ClaimEvidence, refs []generation.EvidenceCardRef, constraints []generation.EvidenceConstraint, sourceIDs []string) ([]candidateadoption.ClaimEvidence, error) {
	allowed := []candidateadoption.ClaimEvidenceReference{}
	for _, card := range constraints {
		for _, a := range card.Anchors {
			if slices.Contains(card.SourceIDs, a.SourceID) {
				allowed = append(allowed, candidateadoption.ClaimEvidenceReference{CardID: card.CardRef.ID, CardVersion: card.CardRef.Version, AnchorID: a.ID, AnchorVersion: a.Version, SourceID: a.SourceID, Locator: a.Locator, SelectionStart: a.SelectionStart, SelectionEnd: a.SelectionEnd, QuotedTextHash: a.QuotedTextHash})
			}
		}
	}
	return candidateadoption.ResolveClaimEvidence(body, claims, evidenceCardVersionRefs(refs), allowed, sourceIDs)
}
