package workspace

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/evidence"
	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	_ "modernc.org/sqlite"
)

type adapterProjectsStub struct {
	core.CapabilityAuthorizer
	denied     bool
	capability string
}

func (p *adapterProjectsStub) Authorize(_ context.Context, _ Actor, _ string, capability string) error {
	p.capability = capability
	if p.denied {
		return core.ErrNotFound
	}
	return nil
}

func (p *adapterProjectsStub) AuthorizeResourceBatch(_ context.Context, _ Actor, _ string, _ AuthorizationAction, resources []ResourceRef) ([]ResourceAuthorizationDecision, error) {
	decisions := make([]ResourceAuthorizationDecision, len(resources))
	for i, resource := range resources {
		decisions[i] = ResourceAuthorizationDecision{Resource: resource, Allowed: !p.denied, Reason: "allowed"}
		if p.denied {
			decisions[i].Reason = "not_authorized"
		}
	}
	return decisions, nil
}

type adapterSourcesStub struct {
	SourceApplicationService
	assets         []evidence.Asset
	retrievalCalls int
	retrieveErr    error
	input          RetrieveSourcesInput
	getSourceCalls int
}

func (s *adapterSourcesStub) ListAssets(context.Context, Actor, string) ([]evidence.Asset, error) {
	return s.assets, nil
}
func (s *adapterSourcesStub) RetrieveSources(_ context.Context, _ Actor, _ string, input RetrieveSourcesInput) ([]evidence.Source, error) {
	s.retrievalCalls++
	s.input = input
	if s.retrieveErr != nil {
		return nil, s.retrieveErr
	}
	return []evidence.Source{}, nil
}
func (s *adapterSourcesStub) GetSource(context.Context, Actor, string, string) (evidence.Source, error) {
	s.getSourceCalls++
	return evidence.Source{}, nil
}

type adapterWebStub struct {
	interfaces.WebSearchService
	calls      int
	tenantID   uint64
	providerID string
	config     *types.WebSearchConfig
}

func (s *adapterWebStub) Search(ctx context.Context, providerID string, config *types.WebSearchConfig, _ string) ([]*types.WebSearchResult, error) {
	s.calls++
	s.tenantID, _ = types.TenantIDFromContext(ctx)
	s.providerID = providerID
	s.config = config
	return []*types.WebSearchResult{{Title: "合成搜索标题", URL: "https://example.org/synthetic", Snippet: "只是一条搜索摘要", Source: "synthetic-provider"}}, nil
}

type adapterFetcherStub struct {
	calls int
	text  string
}

func (f *adapterFetcherStub) Fetch(context.Context, string) (string, error) {
	f.calls++
	return f.text, nil
}
func adapterTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Dialector{DriverName: "sqlite", DSN: filepath.Join(t.TempDir(), "adapter.db")}, &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	t.Cleanup(func() { _ = sqlDB.Close() })
	for _, ddl := range []string{"CREATE TABLE tenants (id INTEGER PRIMARY KEY,web_search_config BLOB,deleted_at DATETIME)", "CREATE TABLE web_search_providers (id TEXT PRIMARY KEY,tenant_id INTEGER NOT NULL,deleted_at DATETIME)"} {
		if err = db.Exec(ddl).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err = db.Exec("INSERT INTO tenants (id,web_search_config) VALUES (?,?)", 7, []byte(`{"max_results":3,"compression_method":"rag","blacklist":["restricted.example"]}`)).Error; err != nil {
		t.Fatal(err)
	}
	if err = db.Exec("INSERT INTO web_search_providers (id,tenant_id) VALUES (?,?)", "synthetic-provider", 7).Error; err != nil {
		t.Fatal(err)
	}
	return db
}
func TestResearchAdapterUsesExistingRetrievalScopeAndRejectsDeniedAsset(t *testing.T) {
	projects := &adapterProjectsStub{}
	sources := &adapterSourcesStub{assets: []evidence.Asset{{ID: "allowed-asset", AssetRevision: 3, ProcessingState: evidence.AssetStateReady}}}
	adapter := NewResearchAdapter(nil, projects, sources, nil, nil, nil)
	actor := core.Actor{TenantID: 7, UserID: "owner", Role: types.TenantRoleOwner}
	scope, err := adapter.CheckAssetScope(context.Background(), actor, "project", []string{"allowed-asset"})
	if err != nil || !reflect.DeepEqual(scope, []core.ProjectAssetVersion{{AssetID: "allowed-asset", AssetRevision: 3}}) {
		t.Fatalf("scope: %+v %v", scope, err)
	}
	if _, err = adapter.CheckAssetScope(context.Background(), actor, "project", []string{"allowed-asset", "revoked-asset"}); !errors.Is(err, core.ErrSourceUnavailable) {
		t.Fatalf("partial denied scope accepted: %v", err)
	}
	task := core.ResearchTask{ChapterID: "chapter-one", AssetIDs: []string{"allowed-asset"}}
	if _, _, err = adapter.SearchResearch(context.Background(), actor, "project", task, "当前查询"); err != nil || sources.retrievalCalls != 1 || !reflect.DeepEqual(sources.input.AssetIDs, []string{"allowed-asset"}) || sources.input.ChapterID != task.ChapterID || projects.capability != "research:chapter-one" {
		t.Fatalf("existing retrieval seam missed: %v calls=%d input=%+v", err, sources.retrievalCalls, sources.input)
	}
	if _, err = adapter.CheckAssetScopeForChapter(context.Background(), actor, "project", task.ChapterID, task.AssetIDs); err != nil {
		t.Fatalf("chapter-author asset scope check rejected an authorized chapter task: %v", err)
	}
	projects.denied = true
	if _, _, err = adapter.SearchResearch(context.Background(), actor, "project", task, "拒绝查询"); !errors.Is(err, core.ErrNotFound) || sources.retrievalCalls != 1 {
		t.Fatalf("authorization happened after retrieval: %v calls=%d", err, sources.retrievalCalls)
	}
}

// TestSourceReadChecksFunctionScopeBeforeReturningExcerpt pins the order of
// the research read: the function scope is decided before the excerpt is
// fetched, so a denied scope never reaches the source.
func TestSourceReadChecksFunctionScopeBeforeReturningExcerpt(t *testing.T) {
	db := adapterTestDB(t)
	if err := db.AutoMigrate(&types.Chunk{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&types.Chunk{ID: "scoped-source", KnowledgeID: "knowledge-one"}).Error; err != nil {
		t.Fatal(err)
	}
	projects := &adapterProjectsStub{denied: true}
	sources := &adapterSourcesStub{assets: []evidence.Asset{{ID: "asset-one", KnowledgeID: "knowledge-one", AssetRevision: 3, ProcessingState: evidence.AssetStateReady}}}
	adapter := NewResearchAdapter(db, projects, sources, nil, nil, nil)
	actor := core.Actor{TenantID: 7, UserID: "scoped-author", Role: types.TenantRoleContributor}
	if _, err := adapter.ReadResearchSourceForChapter(context.Background(), actor, "project", "chapter-one", "scoped-source"); !errors.Is(err, core.ErrSourceUnavailable) {
		t.Fatalf("unauthorized chapter source read error=%v, want source unavailable", err)
	}
	if sources.getSourceCalls != 0 {
		t.Fatalf("source excerpt was fetched %d times before function-scope authorization", sources.getSourceCalls)
	}
}

// TestSearchResearchKeepsTheExternalPathWhenMaterialRetrievalFails pins the
// first half of #335 at the adapter seam: the material path used to return
// early, so a project whose local index was unavailable never reached a
// configured external provider at all.  The provider result must still come
// back, and the material failure must remain visible as a partial failure
// rather than disappearing.
func TestSearchResearchKeepsTheExternalPathWhenMaterialRetrievalFails(t *testing.T) {
	db := adapterTestDB(t)
	sources := &adapterSourcesStub{
		assets:      []evidence.Asset{{ID: "asset", AssetRevision: 1, ProcessingState: evidence.AssetStateReady}},
		retrieveErr: errors.New("no retrievable indexing pipelines across 1 KBs"),
	}
	web := &adapterWebStub{}
	adapter := NewResearchAdapter(db, &adapterProjectsStub{}, sources, nil, nil, web)
	actor := core.Actor{TenantID: 7, UserID: "owner", Role: types.TenantRoleOwner}
	results, _, err := adapter.SearchResearch(context.Background(), actor, "project",
		core.ResearchTask{AssetIDs: []string{"asset"}, ProviderID: "synthetic-provider"}, "合成检索")
	if web.calls != 1 {
		t.Fatalf("external provider never reached: calls=%d err=%v", web.calls, err)
	}
	if len(results) != 1 || results[0].Level != "unread_search_summary" {
		t.Fatalf("external hits were dropped: %+v", results)
	}
	var degraded *core.ResearchSearchError
	if !errors.As(err, &degraded) || !degraded.Partial || !strings.Contains(degraded.Material, "indexing pipelines") {
		t.Fatalf("material degradation is invisible: %v", err)
	}
}

func TestWebSearchAdapterUsesTenantProviderAndKeepsSummaryLowerTier(t *testing.T) {
	db := adapterTestDB(t)
	projects := &adapterProjectsStub{}
	sources := &adapterSourcesStub{}
	web := &adapterWebStub{}
	adapter := NewResearchAdapter(db, projects, sources, nil, nil, web)
	results, _, err := adapter.SearchResearch(context.Background(), core.Actor{TenantID: 7, UserID: "owner", Role: types.TenantRoleOwner}, "project", core.ResearchTask{ProviderID: "synthetic-provider"}, "合成检索")
	if err != nil || web.calls != 1 || web.tenantID != 7 || web.providerID != "synthetic-provider" || web.config.CompressionMethod != "none" {
		t.Fatalf("tenant provider bypassed: %v %+v", err, web)
	}
	if len(results) != 1 || results[0].Level != "unread_search_summary" || results[0].QuotedTextHash != "" || results[0].SourceID != "" || results[0].Author != "" {
		t.Fatalf("search summary became evidence: %+v", results)
	}
}
func TestOriginalInspectionReusesFetcherAndRequiresCurrentProvider(t *testing.T) {
	db := adapterTestDB(t)
	fetch := &adapterFetcherStub{text: "合成原始页面正文，尚未进行学术核对。"}
	adapter := NewResearchAdapter(db, &adapterProjectsStub{}, &adapterSourcesStub{}, nil, nil, nil)
	adapter.fetcher = fetch
	task := core.ResearchTask{ProviderID: "synthetic-provider"}
	result := core.ResearchResult{ID: "result", URL: "https://example.org/synthetic", Level: "unread_search_summary"}
	inspected, err := adapter.InspectResearchOriginal(context.Background(), core.Actor{TenantID: 7, UserID: "owner", Role: types.TenantRoleOwner}, "project", task, result)
	if err != nil || fetch.calls != 1 || inspected.Status != "inspected_original" || inspected.Level != "background_lead" || inspected.QuotedTextHash != researchTextHash(fetch.text) {
		t.Fatalf("original inspection: %+v %v", inspected, err)
	}
	if err = db.Exec("DELETE FROM web_search_providers WHERE id = ?", "synthetic-provider").Error; err != nil {
		t.Fatal(err)
	}
	if _, err = adapter.InspectResearchOriginal(context.Background(), core.Actor{TenantID: 7, UserID: "owner", Role: types.TenantRoleOwner}, "project", task, result); !errors.Is(err, core.ErrSourceUnavailable) || fetch.calls != 1 {
		t.Fatalf("revoked provider fetched: %v calls=%d", err, fetch.calls)
	}
}

func TestProjectOnlyResearchRoundRecordsActualHybridIdentityWithoutInventedWebProvider(t *testing.T) {
	projects := &adapterProjectsStub{}
	sources := &adapterSourcesStub{assets: []evidence.Asset{{ID: "asset", AssetRevision: 1, ProcessingState: evidence.AssetStateReady}}}
	adapter := NewResearchAdapter(nil, projects, sources, nil, nil, nil)
	identity, err := adapter.ResearchRoundProvider(context.Background(), core.Actor{TenantID: 7, UserID: "owner"}, "project", core.ResearchTask{AssetIDs: []string{"asset"}})
	if err != nil || identity != "weknora_hybrid" {
		t.Fatalf("actual hybrid identity absent: %s %v", identity, err)
	}
	if _, err = adapter.ResearchRoundProvider(context.Background(), core.Actor{TenantID: 7, UserID: "owner"}, "project", core.ResearchTask{}); err == nil {
		t.Fatal("invented provider accepted for empty scope")
	}
	projects.denied = true
	if _, err = adapter.ResearchRoundProvider(context.Background(), core.Actor{TenantID: 7, UserID: "owner"}, "project", core.ResearchTask{AssetIDs: []string{"asset"}}); err == nil {
		t.Fatal("revoked scope supplied provider receipt")
	}
}
