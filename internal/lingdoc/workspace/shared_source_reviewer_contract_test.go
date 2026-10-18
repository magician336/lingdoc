package workspace

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/Tencent/WeKnora/internal/evidence"
	"github.com/Tencent/WeKnora/internal/lingdoc/candidateadoption"
	"github.com/Tencent/WeKnora/internal/lingdoc/delivery"
	"github.com/Tencent/WeKnora/internal/lingdoc/generation"
	"github.com/Tencent/WeKnora/internal/lingdoc/sourcepolicy"
	"github.com/Tencent/WeKnora/internal/types"
)

type closeoutSourceReviewer struct {
	t         *testing.T
	verdict   sourcepolicy.Verdict
	calls     int
	caller    types.Caller
	principal types.Principal
	scope     types.TenantAPIKeyScope
}

func (r *closeoutSourceReviewer) Review(ctx context.Context, projectID string, actor evidence.Actor, ids []string) ([]sourcepolicy.Verdict, error) {
	r.t.Helper()
	r.calls++
	principal, _ := types.PrincipalFromContext(ctx)
	scope, _ := types.TenantAPIKeyScopeFromContext(ctx)
	if types.CallerFromContext(ctx) != r.caller || !reflect.DeepEqual(principal, r.principal) || !reflect.DeepEqual(scope, r.scope) {
		r.t.Fatal("source consumer replaced caller, principal, or API key scope")
	}
	if projectID != "project-1" || actor.UserID != r.caller.UserID || actor.TenantID != "7" || !reflect.DeepEqual(ids, []string{"source-1"}) {
		r.t.Fatalf("source reviewer received wrong scope: project=%s actor=%+v ids=%v", projectID, actor, ids)
	}
	return []sourcepolicy.Verdict{r.verdict}, nil
}

func TestSharedSourceReviewerPreservesScopedContextAcrossConsumers(t *testing.T) {
	for _, test := range []struct {
		name           string
		usable         bool
		assetDeny      evidence.DenyReason
		want           error
		generationWant error
	}{
		{name: "available", usable: true},
		{name: "coordinate stale", want: candidateadoption.ErrStaleInput, generationWant: generation.ErrSourceStale},
		{name: "revoked", assetDeny: evidence.DenyNotAuthorized, want: candidateadoption.ErrSourceAccessDenied, generationWant: generation.ErrSourceAccessDenied},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(7))
			ctx = context.WithValue(ctx, types.UserIDContextKey, "scoped-reader")
			caller := types.Caller{TenantID: 7, UserID: "scoped-reader", Role: types.TenantRoleOwner}
			principal := types.Principal{Type: types.PrincipalAPITenant, ID: "scope-key-17"}
			ctx = types.WithCaller(ctx, caller)
			ctx = types.WithPrincipal(ctx, principal)
			ctx = types.WithTenantAPIKeyScope(ctx, types.TenantAPIKeyScope{KeyID: 17, ScopeType: types.APIKeyScopeTenant, KnowledgeBaseIDs: types.StringArray{"kb-1"}, Capabilities: types.StringArray{string(types.APIKeyCapabilityRetrieve)}})
			scope, _ := types.TenantAPIKeyScopeFromContext(ctx)
			reviewer := &closeoutSourceReviewer{t: t, caller: caller, principal: principal, scope: scope, verdict: sourcepolicy.Verdict{SourceID: "source-1", Source: evidence.Source{ID: "source-1", AssetID: "asset-1", AssetRevision: 3}, Usable: test.usable, AssetDeny: test.assetDeny, Status: evidence.SourceStale}}
			policy := &candidateAdoptionSourcePolicy{reviewer: reviewer}
			adoption := candidateadoption.NewCandidateAdoptionService(nil, policy, nil)
			if adoption.Reviewer != policy {
				t.Fatal("candidate adoption did not retain the shared reviewer")
			}
			for name, consume := range map[string]func() error{
				"candidate/confirmation policy": func() error { return policy.Validate(ctx, "project-1", caller.UserID, []string{"source-1"}) },
				"export": func() error {
					return (&DeliveryExportService{reviewer: reviewer}).reviewCurrentSources(ctx, "project-1", caller.UserID, []string{"source-1"})
				},
				"historical release excerpt": func() error {
					return (&DeliveryReleaseService{reviewer: reviewer}).authorizeSnapshotSources(ctx, caller.UserID, "project-1", delivery.ReleaseSnapshot{FrozenInput: delivery.DeliveryInput{Sources: []delivery.FrozenSource{{ID: "source-1"}}}})
				},
			} {
				err := consume()
				if !errors.Is(err, test.want) {
					t.Fatalf("%s error=%v want=%v", name, err, test.want)
				}
			}
			err := (generationSourceValidator{reviewer: reviewer}).ValidateGenerationSources(ctx, generation.Actor{TenantID: 7, UserID: caller.UserID, Role: caller.Role}, "project-1", []generation.Source{generationSourceFromEvidence(reviewer.verdict.Source)})
			if !errors.Is(err, test.generationWant) {
				t.Fatalf("generation error=%v want=%v", err, test.generationWant)
			}
			if reviewer.calls != 4 {
				t.Fatalf("review calls=%d want=4", reviewer.calls)
			}
			mismatched := types.WithCaller(ctx, types.Caller{TenantID: 7, UserID: "other-reader", Role: types.TenantRoleOwner})
			if err := policy.Validate(mismatched, "project-1", caller.UserID, []string{"source-1"}); !errors.Is(err, candidateadoption.ErrSourceAccessDenied) || reviewer.calls != 4 {
				t.Fatalf("mismatched authenticated caller reached shared review: err=%v calls=%d", err, reviewer.calls)
			}
		})
	}
}
