package workspace

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/evidence"
	"github.com/Tencent/WeKnora/internal/lingdoc/academic/crossref"
	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

type balancedResearchSources struct {
	SourceApplicationService
	asset evidence.Asset
	items []evidence.Source
}

type balancedWebResults struct{ interfaces.WebSearchService }

func (balancedWebResults) Search(context.Context, string, *types.WebSearchConfig, string) ([]*types.WebSearchResult, error) {
	results := make([]*types.WebSearchResult, 20)
	for i := range results {
		results[i] = &types.WebSearchResult{Title: fmt.Sprintf("web-%d", i), URL: fmt.Sprintf("https://example.org/%d", i), Source: "web"}
	}
	return results, nil
}

func (s *balancedResearchSources) ListAssets(context.Context, Actor, string) ([]evidence.Asset, error) {
	return []evidence.Asset{s.asset}, nil
}
func (s *balancedResearchSources) RetrieveSources(context.Context, Actor, string, RetrieveSourcesInput) ([]evidence.Source, error) {
	return s.items, nil
}
func (s *balancedResearchSources) GetSource(_ context.Context, _ Actor, _ string, id string) (evidence.Source, error) {
	for _, source := range s.items {
		if source.ID == id {
			return source, nil
		}
	}
	return evidence.Source{}, core.ErrSourceUnavailable
}

// A full local batch must not crowd Crossref out when the real run applies
// its per-round (20) or remaining-result budget to the adapter result.
func TestSearchResearchInterleavesLocalAndCrossrefBeforeRunLimits(t *testing.T) {
	_, db, assetID := seedBoundSource(t)
	seedStorageBackedKnowledge(t, db)
	configureDirectTextStorageKnowledge(t, db)
	storage := &adapterStorageStub{data: []byte(sourcePolicyQuote)}
	setFixtureUploadHash(t, db, storage.data)
	asset := refreshFixtureStorageAsset(t, db, assetID, storage)
	sources := &balancedResearchSources{asset: asset}
	for i := 0; i < 20; i++ {
		id := fmt.Sprintf("source-%d", i+1)
		if i > 0 {
			seedChunk(t, db, id, "knowledge-1", sourcePolicyQuote, 0, 0, len([]rune(sourcePolicyQuote)))
		}
		sources.items = append(sources.items, evidence.Source{ID: id, AssetID: assetID, AssetRevision: asset.AssetRevision, Status: evidence.SourceAvailable, QuotedText: sourcePolicyQuote, QuotedTextHash: researchTextHash(sourcePolicyQuote), Locator: "synthetic-location"})
	}
	projectCatalog := core.AcademicSourceCatalog{ProjectID: "project-1", Version: 1, GlobalVersion: core.AcademicGlobalCatalogVersion, Rules: []core.AcademicSourceRule{{ID: "journal", Version: 1, SourceType: "journal", VenueName: "Synthetic Global Journal", Maintainer: "owner", EffectiveAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}}}
	projectCatalog.SourceCatalogVersion = core.AcademicSourceCatalogVersion(projectCatalog.Rules)
	encoded, err := json.Marshal(projectCatalog)
	require.NoError(t, err)
	require.NoError(t, db.Exec("INSERT INTO lingdoc_g2_objects(project_id,kind,id,version,payload_json) VALUES(?,?,?,?,?)", "project-1", core.AcademicSourceCatalogKind, "project-1", 1, string(encoded)).Error)
	items := make([]map[string]any, 20)
	for i := range items {
		items[i] = map[string]any{"DOI": fmt.Sprintf("10.5555/test-%d", i), "title": []string{fmt.Sprintf("Test %d", i)}, "type": "journal-article", "container-title": []string{"Synthetic Global Journal"}}
	}
	body, err := json.Marshal(map[string]any{"message": map[string]any{"items": items}})
	require.NoError(t, err)
	emptyCatalog := core.AcademicSourceCatalog{ProjectID: "project-1", GlobalVersion: core.AcademicGlobalCatalogVersion, Rules: []core.AcademicSourceRule{}}
	emptyCatalog.SourceCatalogVersion = core.AcademicSourceCatalogVersion(emptyCatalog.Rules)
	adapter := NewResearchAdapter(db, &adapterProjectsStub{}, sources, nil, storage, nil, WithAcademicSourceCatalogReader(&academicCatalogReaderStub{catalog: emptyCatalog}))
	providerFailed := false
	adapter.academic = crossref.Client{HTTPClient: &http.Client{Transport: adapterRoundTripper(func(*http.Request) (*http.Response, error) {
		if providerFailed {
			return &http.Response{StatusCode: http.StatusInternalServerError, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("provider unavailable"))}, nil
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(string(body)))}, nil
	})}}
	require.NoError(t, adapter.ConfigureAcademicResearchPolicy(syntheticGlobalAcademicCatalog(), &syntheticResearchBudgetLedger{}))
	actor := core.Actor{TenantID: 7, UserID: "reader", Role: types.TenantRoleOwner}
	result, mode, err := adapter.SearchResearch(policyContext(), actor, "project-1", core.ResearchTask{AssetIDs: []string{assetID}, ProviderID: core.AcademicCrossrefProviderID}, "software")
	require.NoError(t, err)
	require.Equal(t, "real", mode)
	require.Len(t, result, 40, "the adapter must not change the run's accounting boundary")
	for i := 0; i < 20; i++ {
		if i%2 == 0 {
			require.Equal(t, "weknora_hybrid", result[i].Provider)
		} else {
			require.Equal(t, "crossref", result[i].Provider)
		}
	}
	for i := 0; i < 20; i++ {
		require.Equal(t, sources.items[i].ID, result[i*2].SourceID)
		require.Equal(t, fmt.Sprintf("10.5555/test-%d", i), result[i*2+1].DOI)
	}
	// A remaining budget of two still leaves one result from each source;
	// with one slot, preserving the local-first policy is deterministic.
	require.Equal(t, "weknora_hybrid", result[:1][0].Provider)
	require.Equal(t, "crossref", result[:2][1].Provider)

	localOnly, _, err := adapter.SearchResearch(policyContext(), actor, "project-1", core.ResearchTask{AssetIDs: []string{assetID}}, "software")
	require.NoError(t, err)
	require.Len(t, localOnly, 20)
	for i := range localOnly {
		require.Equal(t, sources.items[i].ID, localOnly[i].SourceID)
	}
	externalOnly, _, err := adapter.SearchResearch(policyContext(), actor, "project-1", core.ResearchTask{ProviderID: core.AcademicCrossrefProviderID}, "software")
	require.NoError(t, err)
	require.Len(t, externalOnly, 20)
	for i := range externalOnly {
		require.Equal(t, fmt.Sprintf("10.5555/test-%d", i), externalOnly[i].DOI)
	}

	providerFailed = true
	partial, _, err := adapter.SearchResearch(policyContext(), actor, "project-1", core.ResearchTask{AssetIDs: []string{assetID}, ProviderID: core.AcademicCrossrefProviderID}, "software")
	require.Error(t, err)
	require.Len(t, partial, 20, "provider failures retain the original local batch and error")
	for i := range partial {
		require.Equal(t, sources.items[i].ID, partial[i].SourceID)
	}

	applyCreateTablesFromMigration(t, db, "000000_init.up.sql", "tenants")
	require.NoError(t, db.Exec("INSERT INTO tenants(id,name,business,web_search_config) VALUES(?,?,?,?)", 7, "test", "test", `{"max_results":20}`).Error)
	adapter.web = balancedWebResults{}
	webMixed, _, err := adapter.SearchResearch(policyContext(), actor, "project-1", core.ResearchTask{AssetIDs: []string{assetID}, ProviderID: "web"}, "software")
	require.NoError(t, err)
	require.Len(t, webMixed, 40)
	for i := 0; i < 20; i++ {
		require.Equal(t, sources.items[i].ID, webMixed[i*2].SourceID)
		require.Equal(t, fmt.Sprintf("web-%d", i), webMixed[i*2+1].Title)
	}
}

func TestInterleaveResearchResultsKeepsSingleAndUnevenInputs(t *testing.T) {
	local := []core.ResearchResult{{ID: "l1"}, {ID: "l2"}, {ID: "l3"}}
	external := []core.ResearchResult{{ID: "e1"}, {ID: "e2"}}
	require.Equal(t, local, interleaveResearchResults(local, nil))
	require.Equal(t, external, interleaveResearchResults(nil, external))
	require.Equal(t, []core.ResearchResult{{ID: "l1"}, {ID: "e1"}, {ID: "l2"}, {ID: "e2"}, {ID: "l3"}}, interleaveResearchResults(local, external))
	require.Equal(t, []core.ResearchResult{{ID: "l1"}, {ID: "l2"}, {ID: "l3"}}, local, "merge must not mutate source ordering")
}
