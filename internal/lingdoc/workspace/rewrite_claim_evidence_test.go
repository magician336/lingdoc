package workspace

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/lingdoc/candidateadoption"
	"github.com/stretchr/testify/require"
)

func TestRewriteCardClaimsPersistAndHistoricalMissingBindingFailsClosed(t *testing.T) {
	s, _, _, input := formalRewriteFixture(t)
	actor := Actor{TenantID: 1, UserID: "owner"}
	candidate, err := s.Create(context.Background(), actor, "project", "chapter", "claim-rewrite", input)
	require.NoError(t, err)
	require.Equal(t, candidateadoption.ValidityFresh, candidate.Validity)
	require.Len(t, candidate.Claims, 1)
	require.Equal(t, "page/1", candidate.Claims[0].References[0].Locator)
	restored, err := s.Get(context.Background(), actor, "project", candidate.CandidateID)
	require.NoError(t, err)
	require.Equal(t, candidate.Claims, restored.Claims)
	require.NoError(t, s.store.db.Model(&selectedRewriteRow{}).Where("id = ?", candidate.CandidateID).Update("claim_evidence_json", "[]").Error)
	_, err = s.Get(context.Background(), actor, "project", candidate.CandidateID)
	require.ErrorIs(t, err, ErrRewriteStale)
	_, _, err = s.Apply(context.Background(), actor, "project", "chapter", candidate.CandidateID, "claim-apply", ApplySelectedRewriteInput{ExpectedSpecRevision: 1, ExpectedWorkingCopyRevision: 1})
	require.ErrorIs(t, err, ErrRewriteStale)
}
