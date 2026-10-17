package candidateadoption

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/Tencent/WeKnora/internal/evidence"
	"github.com/Tencent/WeKnora/internal/lingdoc/sourcepolicy"
	"github.com/Tencent/WeKnora/internal/types"
)

type scopedReviewerPolicy struct {
	t         *testing.T
	calls     int
	caller    types.Caller
	principal types.Principal
	scope     *types.TenantAPIKeyScope
	stale     bool
}

func (p *scopedReviewerPolicy) Validate(context.Context, string, string, []string) error {
	p.t.Fatal("candidate adoption called the legacy policy after receiving a shared reviewer")
	return ErrSourceAccessDenied
}

func (p *scopedReviewerPolicy) Review(ctx context.Context, projectID string, actor evidence.Actor, ids []string) ([]sourcepolicy.Verdict, error) {
	p.t.Helper()
	p.calls++
	principal, _ := types.PrincipalFromContext(ctx)
	if types.CallerFromContext(ctx) != p.caller || !reflect.DeepEqual(principal, p.principal) || projectID != "project-1" || actor.UserID != "reader-1" || actor.TenantID != "7" || !reflect.DeepEqual(ids, []string{"source-1"}) {
		p.t.Fatal("candidate adoption changed authenticated identity or the requested source scope")
	}
	scope, hasScope := types.TenantAPIKeyScopeFromContext(ctx)
	if hasScope != (p.scope != nil) || (hasScope && !reflect.DeepEqual(scope, *p.scope)) {
		p.t.Fatal("candidate adoption replaced the authenticated API key restriction")
	}
	v := sourcepolicy.Verdict{SourceID: "source-1", Usable: !p.stale, Status: evidence.SourceStale}
	if hasScope && (!scope.HasCapability(types.APIKeyCapabilityRetrieve) || !scope.AllowsKnowledgeBase("kb-1")) {
		v.Usable, v.AssetDeny = false, evidence.DenyNotAuthorized
	}
	return []sourcepolicy.Verdict{v}, nil
}

func TestCandidateSharedReviewerRetainsContextAndDenial(t *testing.T) {
	for _, test := range []struct {
		name                                                                string
		absent, wrongCaller, wrongUser, machine, retrieve, allowedKB, stale bool
		want                                                                error
		calls                                                               int
	}{
		{name: "authenticated allow", calls: 1},
		{name: "no identity", absent: true, want: ErrSourceAccessDenied},
		{name: "different authenticated caller", wrongCaller: true, want: ErrSourceAccessDenied},
		{name: "different user context", wrongUser: true, want: ErrSourceAccessDenied},
		{name: "restricted API key allow", machine: true, retrieve: true, allowedKB: true, calls: 1},
		{name: "API key capability denied", machine: true, allowedKB: true, want: ErrSourceAccessDenied, calls: 1},
		{name: "API key KB denied", machine: true, retrieve: true, want: ErrSourceAccessDenied, calls: 1},
		{name: "shared stale verdict", stale: true, want: ErrStaleInput, calls: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			caller := types.Caller{TenantID: 7, UserID: "reader-1", Role: types.TenantRoleOwner}
			principal := types.Principal{Type: types.PrincipalWebUser, ID: "reader-1"}
			if test.machine {
				principal = types.Principal{Type: types.PrincipalAPITenant, ID: "17"}
			}
			if !test.absent {
				ctx = context.WithValue(ctx, types.TenantIDContextKey, uint64(7))
				ctx = context.WithValue(ctx, types.UserIDContextKey, "reader-1")
				ctx = types.WithCaller(ctx, caller)
				ctx = types.WithPrincipal(ctx, principal)
			}
			policy := &scopedReviewerPolicy{t: t, caller: caller, principal: principal, stale: test.stale}
			if test.machine {
				scope := types.TenantAPIKeyScope{KeyID: 17, ScopeType: types.APIKeyScopeTenant, KnowledgeBaseIDs: types.StringArray{"kb-other"}, Capabilities: types.StringArray{string(types.APIKeyCapabilityChat)}}
				if test.retrieve {
					scope.Capabilities = types.StringArray{string(types.APIKeyCapabilityRetrieve)}
				}
				if test.allowedKB {
					scope.KnowledgeBaseIDs = types.StringArray{"kb-1"}
				}
				ctx = types.WithTenantAPIKeyScope(ctx, scope)
				normalized, _ := types.TenantAPIKeyScopeFromContext(ctx)
				policy.scope = &normalized
			}
			if test.wrongCaller {
				ctx = types.WithCaller(ctx, types.Caller{TenantID: 7, UserID: "different-reader", Role: types.TenantRoleOwner})
			}
			if test.wrongUser {
				ctx = context.WithValue(ctx, types.UserIDContextKey, "different-reader")
			}
			service := NewCandidateAdoptionService(nil, policy, nil)
			if service.Reviewer != policy {
				t.Fatal("shared reviewer was not connected by the constructor")
			}
			err := service.validateSources(ctx, "project-1", "reader-1", []string{"source-1"})
			if !errors.Is(err, test.want) || policy.calls != test.calls {
				t.Fatalf("error=%v want=%v reviewer calls=%d want=%d", err, test.want, policy.calls, test.calls)
			}
		})
	}
}

type candidateVerdictReviewer []sourcepolicy.Verdict

func (r candidateVerdictReviewer) Review(context.Context, string, evidence.Actor, []string) ([]sourcepolicy.Verdict, error) {
	return r, nil
}

func TestCandidateMixedSourceFailuresKeepStalePrecedence(t *testing.T) {
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(7))
	ctx = context.WithValue(ctx, types.UserIDContextKey, "reader-1")
	ctx = types.WithCaller(ctx, types.Caller{TenantID: 7, UserID: "reader-1", Role: types.TenantRoleOwner})
	denied := sourcepolicy.Verdict{SourceID: "denied", AssetDeny: evidence.DenyNotAuthorized}
	stale := sourcepolicy.Verdict{SourceID: "stale", Status: evidence.SourceStale}
	for _, verdicts := range []candidateVerdictReviewer{{denied, stale}, {stale, denied}} {
		service := &CandidateAdoptionService{Reviewer: verdicts}
		err := service.validateSources(ctx, "project-1", "reader-1", []string{"denied", "stale"})
		if !errors.Is(err, ErrStaleInput) {
			t.Fatalf("mixed verdicts=%v err=%v want stale", verdicts, err)
		}
	}
}
