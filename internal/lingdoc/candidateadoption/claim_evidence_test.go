package candidateadoption

import (
	"context"
	"testing"

	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
	"github.com/stretchr/testify/require"
)

func TestResolveClaimEvidenceRequiresExplicitCardAnchorAndCompleteCoverage(t *testing.T) {
	refs := []core.VersionRef{{ID: "card-a", Version: 2}, {ID: "card-b", Version: 3}}
	allowed := []ClaimEvidenceReference{{CardID: "card-a", CardVersion: 2, AnchorID: "anchor-a", AnchorVersion: 1, SourceID: "same", Locator: "page 1", SelectionEnd: 4, QuotedTextHash: "hash-a"}, {CardID: "card-b", CardVersion: 3, AnchorID: "anchor-b", AnchorVersion: 2, SourceID: "same", Locator: "page 2", SelectionStart: 5, SelectionEnd: 9, QuotedTextHash: "hash-b"}}
	body := "甲 [[source:same]]。乙 [[source:same]]。"
	declared := []ClaimEvidence{{Text: "甲 [[source:same]]。", References: []ClaimEvidenceReference{{CardID: "card-a", CardVersion: 2, AnchorID: "anchor-a", AnchorVersion: 1, SourceID: "same", Locator: "forged"}}}, {Text: "乙 [[source:same]]。", References: []ClaimEvidenceReference{{CardID: "card-b", CardVersion: 3, AnchorID: "anchor-b", AnchorVersion: 2, SourceID: "same"}}}}
	resolved, err := ResolveClaimEvidence(body, declared, refs, allowed, []string{"same"})
	require.NoError(t, err)
	require.Equal(t, "page 1", resolved[0].References[0].Locator)
	require.Equal(t, "hash-b", resolved[1].References[0].QuotedTextHash)
	cases := map[string]func([]ClaimEvidence){
		"missing reference":             func(c []ClaimEvidence) { c[0].References = nil },
		"same source wrong card anchor": func(c []ClaimEvidence) { c[0].References[0].AnchorID = "anchor-b" },
		"stale card":                    func(c []ClaimEvidence) { c[0].References[0].CardVersion++ },
		"stale anchor":                  func(c []ClaimEvidence) { c[0].References[0].AnchorVersion++ },
		"wrong source":                  func(c []ClaimEvidence) { c[0].References[0].SourceID = "forged" },
		"uncovered text":                func(c []ClaimEvidence) { c[0].Text = "甲 [[source:same]]" },
		"unmarked claim":                func(c []ClaimEvidence) { c[0].Text = "甲。" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			copy := make([]ClaimEvidence, len(declared))
			for i := range declared {
				copy[i] = declared[i]
				copy[i].References = append([]ClaimEvidenceReference(nil), declared[i].References...)
			}
			mutate(copy)
			_, err := ResolveClaimEvidence(body, copy, refs, allowed, []string{"same"})
			require.ErrorIs(t, err, ErrCandidateStale)
		})
	}
	_, err = ResolveClaimEvidence(body, nil, refs, allowed, []string{"same"})
	require.ErrorIs(t, err, ErrCandidateStale)
	_, err = ResolveClaimEvidence(body, nil, nil, nil, []string{"same"})
	require.NoError(t, err)
}

func TestLegacyCardCandidateFailsAdoptionAndNoCardStillWorks(t *testing.T) {
	store, c, in, policy := newBoundCandidateFixture(t, true)
	c.Claims = nil
	require.NoError(t, store.UpsertCandidate(context.Background(), c))
	persisted, err := store.GetCandidate(context.Background(), c.ProjectID, c.ID)
	require.NoError(t, err)
	require.Empty(t, persisted.Claims)
	_, err = NewCandidateAdoptionService(store, policy, allowAuthorizer{}).AcceptCandidate(context.Background(), in)
	require.ErrorIs(t, err, ErrCandidateStale)
	var count int64
	require.NoError(t, store.DB().Model(&chapterVersionRow{}).Count(&count).Error)
	require.Zero(t, count)
	store, _, in, policy = newBoundCandidateFixture(t, false)
	_, err = NewCandidateAdoptionService(store, policy, allowAuthorizer{}).AcceptCandidate(context.Background(), in)
	require.NoError(t, err)
}

func TestPersistedClaimsAreRecheckedAgainstCurrentAnchor(t *testing.T) {
	store, c, in, policy := newBoundCandidateFixture(t, true)
	persisted, err := store.GetCandidate(context.Background(), c.ProjectID, c.ID)
	require.NoError(t, err)
	require.Equal(t, c.Claims, persisted.Claims)
	policy.selected.Anchors[0].Locator = "changed"
	_, err = NewCandidateAdoptionService(store, policy, allowAuthorizer{}).AcceptCandidate(context.Background(), in)
	require.ErrorIs(t, err, ErrCandidateStale)
	var count int64
	require.NoError(t, store.DB().Model(&chapterVersionRow{}).Count(&count).Error)
	require.Zero(t, count)
}
