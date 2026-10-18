package workspace

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/lingdoc/candidateadoption"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestCandidateSourceRecheckPreservesAuthenticatedCallerAndMachineScope(t *testing.T) {
	caller := types.Caller{TenantID: 7, UserID: "machine", Role: types.TenantRoleContributor}
	ctx := types.WithExecutionTenant(types.WithCaller(context.Background(), caller), 7)
	principal := types.Principal{Type: types.PrincipalAPITenant, ID: "api-principal"}
	ctx = types.WithPrincipal(ctx, principal)
	scope := types.TenantAPIKeyScope{KeyID: 24, ScopeType: types.APIKeyScopeTenant, KnowledgeBaseIDs: types.StringArray{"kb-allowed"}, Capabilities: types.StringArray{"retrieve", "ingest"}}
	ctx = types.WithTenantAPIKeyScope(ctx, scope)
	checked, actor, ok := recheckContext(ctx, "machine")
	require.True(t, ok)
	require.Equal(t, caller, types.CallerFromContext(checked))
	require.Equal(t, "machine", actor.UserID)
	require.Equal(t, "7", actor.TenantID)
	actual, exists := types.TenantAPIKeyScopeFromContext(checked)
	require.True(t, exists)
	require.Equal(t, scope, actual)
	actualPrincipal, exists := types.PrincipalFromContext(checked)
	require.True(t, exists)
	require.Equal(t, principal, actualPrincipal)
	_, _, ok = recheckContext(ctx, "another-user")
	require.False(t, ok)
	foreignExecution := types.WithExecutionTenant(ctx, 8)
	_, _, ok = recheckContext(foreignExecution, "machine")
	require.False(t, ok)
	conflictingUser := context.WithValue(ctx, types.UserIDContextKey, "another-user")
	_, _, ok = recheckContext(conflictingUser, "machine")
	require.False(t, ok)
}

func TestCandidateEvidencePolicyRequiresRealCurrentPrivateProofAndCurrentActor(t *testing.T) {
	f := newCandidateOneConnFixture(t)
	policy, ok := f.runtime.CandidateAdoptionSourcePolicy().(candidateadoption.CandidateEvidencePolicy)
	require.True(t, ok)
	prepared, selected, err := policy.PrepareCandidateEvidence(f.ctx, f.input.ProjectID, f.input.ChapterID, f.actor.UserID, f.candidate.Basis.EvidenceCardRefs, f.candidate.SourceIDs)
	require.NoError(t, err)
	require.Len(t, selected.Cards, 1)
	require.Equal(t, f.card.ID, selected.Cards[0].ID)
	require.NotNil(t, selected.Cards[0].Confirmation)
	require.Equal(t, f.actor.UserID, selected.Cards[0].Confirmation.ConfirmedBy)
	require.Equal(t, types.CallerFromContext(f.ctx), types.CallerFromContext(prepared))
	// A plain authenticated context cannot manufacture the server's source proof.
	err = f.db.WithContext(f.ctx).Transaction(func(tx *gorm.DB) error {
		_, e := policy.ValidateCandidateEvidenceInTransaction(f.ctx, tx, f.input.ProjectID, f.input.ChapterID, f.actor.UserID, f.candidate.Basis.EvidenceCardRefs, f.candidate.SourceIDs)
		require.ErrorIs(t, e, candidateadoption.ErrSourceAccessDenied)
		return nil
	})
	require.NoError(t, err)
	err = f.db.WithContext(prepared).Transaction(func(tx *gorm.DB) error {
		actual, e := policy.ValidateCandidateEvidenceInTransaction(prepared, tx, f.input.ProjectID, f.input.ChapterID, f.actor.UserID, f.candidate.Basis.EvidenceCardRefs, f.candidate.SourceIDs)
		require.NoError(t, e)
		require.Len(t, actual.Cards, 1)
		require.Equal(t, f.card.Version, actual.Cards[0].Version)
		return nil
	})
	require.NoError(t, err)
	_, _, err = policy.PrepareCandidateEvidence(f.ctx, f.input.ProjectID, f.input.ChapterID, "forged-user", f.candidate.Basis.EvidenceCardRefs, f.candidate.SourceIDs)
	require.ErrorIs(t, err, candidateadoption.ErrSourceAccessDenied)
	_, err = policy.ValidateCandidateEvidenceInTransaction(prepared, nil, f.input.ProjectID, f.input.ChapterID, f.actor.UserID, f.candidate.Basis.EvidenceCardRefs, f.candidate.SourceIDs)
	require.ErrorIs(t, err, candidateadoption.ErrDependencyUnavailable)
	missing := &candidateAdoptionSourcePolicy{}
	_, _, err = missing.PrepareCandidateEvidence(f.ctx, f.input.ProjectID, f.input.ChapterID, f.actor.UserID, f.candidate.Basis.EvidenceCardRefs, f.candidate.SourceIDs)
	require.ErrorIs(t, err, candidateadoption.ErrDependencyUnavailable)
}
