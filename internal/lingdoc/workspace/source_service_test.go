package workspace

import (
	"context"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/evidence"
	"github.com/Tencent/WeKnora/internal/types"
)

type projectAuthorizerStub struct {
	err   error
	calls int
}

type sourceAuditSinkStub struct{ events []AuditEvent }

func (s *sourceAuditSinkStub) Record(_ context.Context, event AuditEvent) error {
	s.events = append(s.events, event)
	return nil
}

func (s *projectAuthorizerStub) Authorize(context.Context, Actor, string, string) error {
	s.calls++
	return s.err
}

type assetGatewayStub struct {
	result *evidence.ResolveResult
	err    error
	calls  int
}

func (s *assetGatewayStub) ResolveAllowed(context.Context, string, evidence.Actor, []string) (*evidence.ResolveResult, error) {
	s.calls++
	return s.result, s.err
}

type knowledgeSearchStub struct{ calls int }

func (*knowledgeSearchStub) GetKnowledgeBaseByIDOnly(context.Context, string) (*types.KnowledgeBase, error) {
	return nil, nil
}

func (s *knowledgeSearchStub) HybridSearch(context.Context, string, types.SearchParams) ([]*types.SearchResult, error) {
	s.calls++
	return nil, nil
}

type sourceCatalogStub struct{ chunk *SourceChunk }

func (s sourceCatalogStub) Knowledge(context.Context, string) (*SourceKnowledge, error) {
	return nil, nil
}
func (s sourceCatalogStub) Chunk(context.Context, string) (*SourceChunk, error) { return s.chunk, nil }

type sourceBindingsStub struct {
	BindingStore
	asset              evidence.Asset
	currentKnowledgeID string
	revisionReadCalls  int
}

func (s *sourceBindingsStub) AssetForKnowledge(context.Context, string, string) (evidence.Asset, error) {
	return s.asset, nil
}

func (s *sourceBindingsStub) KnowledgeOfRevision(context.Context, string, int) (string, bool, error) {
	s.revisionReadCalls++
	return s.currentKnowledgeID, s.currentKnowledgeID != "", nil
}

type originTextStub string

func (s originTextStub) OriginText(context.Context, string) (string, bool, error) {
	return string(s), true, nil
}

type assetGatewaySequence struct {
	results []*evidence.ResolveResult
	calls   int
}

func (s *assetGatewaySequence) ResolveAllowed(context.Context, string, evidence.Actor, []string) (*evidence.ResolveResult, error) {
	result := s.results[s.calls]
	s.calls++
	return result, nil
}

func TestSourceServiceChecksProjectAccessBeforeResolvingAssets(t *testing.T) {
	projects := &projectAuthorizerStub{err: ErrNotFound}
	gateway := &assetGatewayStub{}
	service := NewSourceService(projects, nil, gateway, nil, nil, nil, nil)

	_, err := service.RetrieveSources(context.Background(), Actor{TenantID: 7, UserID: "revoked"}, "project-1", RetrieveSourcesInput{
		Query: "budget", AssetIDs: []string{"asset-1"},
	})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("RetrieveSources() error = %v, want project access denial", err)
	}
	if projects.calls != 1 || gateway.calls != 0 {
		t.Fatalf("authorization/gateway calls = %d/%d, want 1/0", projects.calls, gateway.calls)
	}
}

func TestSourceServiceRejectsPartiallyDeniedAssetsBeforeSearch(t *testing.T) {
	projects := &projectAuthorizerStub{}
	gateway := &assetGatewayStub{result: &evidence.ResolveResult{
		Requested: []string{"allowed-1", "revoked-2"},
		Allowed:   []evidence.Asset{{ID: "allowed-1", KnowledgeID: "knowledge-1"}},
		Denied:    []evidence.DeniedAsset{{AssetID: "revoked-2", Reason: evidence.DenyNotAuthorized}},
	}}
	search := &knowledgeSearchStub{}
	audit := &sourceAuditSinkStub{}
	service := NewSourceService(projects, nil, gateway, nil, nil, nil, search, audit)

	_, err := service.RetrieveSources(context.Background(), Actor{TenantID: 7, UserID: "writer"}, "project-1", RetrieveSourcesInput{
		Query: "budget", AssetIDs: []string{"allowed-1", "revoked-2"},
	})
	var denied *DeniedAssetsError
	if !errors.As(err, &denied) {
		t.Fatalf("RetrieveSources() error = %v, want DeniedAssetsError", err)
	}
	if len(denied.Denied) != 1 || denied.Denied[0].AssetID != "revoked-2" || denied.Denied[0].Reason != evidence.DenyNotAuthorized {
		t.Fatalf("denied assets = %#v, want the exact refused ID and reason", denied.Denied)
	}
	if projects.calls != 1 || gateway.calls != 1 || search.calls != 0 {
		t.Fatalf("project/gateway/search calls = %d/%d/%d, want 1/1/0", projects.calls, gateway.calls, search.calls)
	}
	if len(audit.events) != 1 || audit.events[0].Capability != "source.retrieve" || audit.events[0].Decision != "deny" {
		t.Fatalf("source audit = %+v", audit.events)
	}
}

func TestSourceServiceRechecksAuthorizationAndAssetVersion(t *testing.T) {
	current := evidence.Asset{
		ID: "asset-1", ProjectID: "project-1", KnowledgeID: "knowledge-old", Title: "methods",
		AssetRevision: 1, ProcessingState: evidence.AssetStateReady,
	}
	chunk := &SourceChunk{ID: "source-1", KnowledgeID: "knowledge-old", ChunkIndex: 1,
		StartAt: 0, EndAt: 6, Content: "budget"}
	firstRead := &evidence.ResolveResult{Requested: []string{"asset-1"}, Allowed: []evidence.Asset{current}, Denied: []evidence.DeniedAsset{}}
	cases := []struct {
		name            string
		secondRead      *evidence.ResolveResult
		wantStatus      evidence.SourceStatus
		wantErr         error
		wantRevReadCall int
	}{
		{
			name: "authorization revoked between read and recheck",
			secondRead: &evidence.ResolveResult{
				Requested: []string{"asset-1"},
				Allowed:   []evidence.Asset{},
				Denied:    []evidence.DeniedAsset{{AssetID: "asset-1", Reason: evidence.DenyNotAuthorized}},
			},
			wantErr: ErrSourceUnavailable,
		},
		{
			name: "reparsed source is marked stale",
			secondRead: &evidence.ResolveResult{
				Requested: []string{"asset-1"},
				Allowed: []evidence.Asset{{
					ID: "asset-1", ProjectID: "project-1", KnowledgeID: "knowledge-new", Title: "methods",
					AssetRevision: 2, ProcessingState: evidence.AssetStateReady,
				}},
				Denied: []evidence.DeniedAsset{},
			},
			wantStatus: evidence.SourceStale,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			bindings := &sourceBindingsStub{asset: current, currentKnowledgeID: "knowledge-old"}
			gateway := &assetGatewaySequence{results: []*evidence.ResolveResult{firstRead, tc.secondRead}}
			service := NewSourceService(&projectAuthorizerStub{}, bindings, gateway, sourceCatalogStub{chunk: chunk}, originTextStub("budget"), nil, nil)
			got, err := service.GetSource(context.Background(), Actor{TenantID: 7, UserID: "reader"}, "project-1", "source-1")
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("GetSource() error = %v, want %v", err, tc.wantErr)
			}
			if got.Status != tc.wantStatus {
				t.Fatalf("GetSource() status = %q, want %q", got.Status, tc.wantStatus)
			}
			if gateway.calls != 2 || bindings.revisionReadCalls != tc.wantRevReadCall {
				t.Fatalf("gateway/revision reads = %d/%d, want 2/%d", gateway.calls, bindings.revisionReadCalls, tc.wantRevReadCall)
			}
		})
	}
}
