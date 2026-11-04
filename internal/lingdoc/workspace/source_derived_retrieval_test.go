package workspace

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/Tencent/WeKnora/internal/evidence"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

type summaryFixtureBindings struct{ *sourceBindingsStub }

func (s summaryFixtureBindings) AssetScope(context.Context, string, string) (evidence.AssetScopeRef, error) {
	return evidence.AssetScopeRef{KnowledgeBaseID: "kb-1", OwnerTenantID: 7}, nil
}

type summaryFixtureSearch struct {
	interfaces.KnowledgeBaseService
	hits []*types.SearchResult
}

func (s summaryFixtureSearch) GetKnowledgeBaseByIDOnly(context.Context, string) (*types.KnowledgeBase, error) {
	return nil, nil
}
func (s summaryFixtureSearch) HybridSearch(context.Context, string, types.SearchParams) ([]*types.SearchResult, error) {
	return s.hits, nil
}

func TestSourceRetrievalDoesNotPresentDerivedSummaryAsOriginal(t *testing.T) {
	asset := evidence.Asset{ID: "asset-1", ProjectID: "project-1", KnowledgeID: "knowledge-1", Title: "paper.txt", AssetRevision: 1}
	bindings := summaryFixtureBindings{&sourceBindingsStub{asset: asset}}
	gateway := &assetGatewayStub{result: &evidence.ResolveResult{Allowed: []evidence.Asset{asset}}}
	hits := []*types.SearchResult{
		nil,
		{ID: "summary", KnowledgeID: "knowledge-1", ChunkType: types.ChunkTypeSummary, Content: "model summary", StartAt: 0, EndAt: 0},
		{ID: "text", KnowledgeID: "knowledge-1", ChunkType: types.ChunkTypeText, Content: "example", StartAt: 0, EndAt: 7},
		{ID: "legacy-unknown", KnowledgeID: "knowledge-1", Content: "different", StartAt: 0, EndAt: 0},
	}
	svc := NewSourceService(&projectAuthorizerStub{}, bindings, gateway, nil, originTextStub("example"), nil, summaryFixtureSearch{hits: hits})
	got, err := svc.RetrieveSources(context.Background(), Actor{TenantID: 7, UserID: "reader"}, "project-1", RetrieveSourcesInput{Query: "example", AssetIDs: []string{"asset-1"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].ID != "text" || got[0].Status != evidence.SourceAvailable || got[1].ID != "legacy-unknown" || got[1].Status != evidence.SourceUnavailable {
		t.Fatalf("sources = %#v; want original text and unavailable unknown; derived summary omitted", got)
	}
}

func TestSourceRetrievalHTTPExcludesDerivedSummary(t *testing.T) {
	f := newAssetsHTTPFixture(t)
	start, end := originSpan(4)
	testRuntime(f.handler).knowledge = summaryFixtureSearch{hits: []*types.SearchResult{
		{ID: "summary", KnowledgeID: "knowledge-txt", ChunkType: types.ChunkTypeSummary, Content: "derived summary"},
		{ID: chunkSourceID(4), KnowledgeID: "knowledge-txt", ChunkType: types.ChunkTypeText, ChunkIndex: 4, Content: originLines[4], StartAt: start, EndAt: end},
	}}
	body := fmt.Sprintf(`{"query":"建设目标","asset_ids":[%q]}`, f.txtAssetID)
	envelope := expectAssetStatus(t, callAssetEndpoint(f, http.MethodPost, assetRouteBase+"/retrieval", body, ""), http.StatusOK)
	var sources []evidence.Source
	decodeAssetData(t, envelope, &sources)
	if len(sources) != 1 || sources[0].ID != chunkSourceID(4) || sources[0].Status != evidence.SourceAvailable {
		t.Fatalf("sources = %#v; expected only the available text source", sources)
	}
	f.authorizer.denied[f.txtAssetID] = true
	denied := expectAssetStatus(t, callAssetEndpoint(f, http.MethodPost, assetRouteBase+"/retrieval", body, ""), http.StatusForbidden)
	expectAssetErrorCode(t, denied, "asset_not_authorized")
}
