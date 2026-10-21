package workspace

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
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

type g2AdapterProjects struct {
	core.CapabilityAuthorizer
	denied     bool
	capability string
}

func (p *g2AdapterProjects) Authorize(_ context.Context, _ Actor, _ string, capability string) error {
	p.capability = capability
	if p.denied {
		return core.ErrNotFound
	}
	return nil
}

type g2AdapterSources struct {
	SourceApplicationService
	assets         []evidence.Asset
	retrievalCalls int
	input          RetrieveSourcesInput
}

func (s *g2AdapterSources) ListAssets(context.Context, Actor, string) ([]evidence.Asset, error) {
	return s.assets, nil
}
func (s *g2AdapterSources) RetrieveSources(_ context.Context, _ Actor, _ string, input RetrieveSourcesInput) ([]evidence.Source, error) {
	s.retrievalCalls++
	s.input = input
	return []evidence.Source{}, nil
}

type g2AdapterWeb struct {
	interfaces.WebSearchService
	calls      int
	tenantID   uint64
	providerID string
	config     *types.WebSearchConfig
}

func (s *g2AdapterWeb) Search(ctx context.Context, providerID string, config *types.WebSearchConfig, _ string) ([]*types.WebSearchResult, error) {
	s.calls++
	s.tenantID, _ = types.TenantIDFromContext(ctx)
	s.providerID = providerID
	s.config = config
	return []*types.WebSearchResult{{Title: "合成搜索标题", URL: "https://example.org/synthetic", Snippet: "只是一条搜索摘要", Source: "synthetic-provider"}}, nil
}

type g2AdapterFetcher struct {
	calls int
	text  string
}

func (f *g2AdapterFetcher) Fetch(context.Context, string) (string, error) {
	f.calls++
	return f.text, nil
}
func g2AdapterDB(t *testing.T) *gorm.DB {
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
func TestG2ResearchAdapterUsesExistingRetrievalScopeAndRejectsDeniedAsset(t *testing.T) {
	projects := &g2AdapterProjects{}
	sources := &g2AdapterSources{assets: []evidence.Asset{{ID: "allowed-asset", AssetRevision: 3, ProcessingState: evidence.AssetStateReady}}}
	adapter := NewG2ResearchAdapter(nil, projects, sources, nil, nil, nil)
	actor := core.Actor{TenantID: 7, UserID: "owner", Role: types.TenantRoleOwner}
	scope, err := adapter.CheckG2AssetScope(context.Background(), actor, "project", []string{"allowed-asset"})
	if err != nil || !reflect.DeepEqual(scope, []core.G2AssetVersion{{AssetID: "allowed-asset", AssetRevision: 3}}) {
		t.Fatalf("scope: %+v %v", scope, err)
	}
	if _, err = adapter.CheckG2AssetScope(context.Background(), actor, "project", []string{"allowed-asset", "revoked-asset"}); !errors.Is(err, core.ErrSourceUnavailable) {
		t.Fatalf("partial denied scope accepted: %v", err)
	}
	if _, _, err = adapter.SearchResearch(context.Background(), actor, "project", core.ResearchTask{AssetIDs: []string{"allowed-asset"}}, "当前查询"); err != nil || sources.retrievalCalls != 1 || !reflect.DeepEqual(sources.input.AssetIDs, []string{"allowed-asset"}) || projects.capability != "research" {
		t.Fatalf("existing retrieval seam missed: %v calls=%d input=%+v", err, sources.retrievalCalls, sources.input)
	}
	projects.denied = true
	if _, _, err = adapter.SearchResearch(context.Background(), actor, "project", core.ResearchTask{AssetIDs: []string{"allowed-asset"}}, "拒绝查询"); !errors.Is(err, core.ErrNotFound) || sources.retrievalCalls != 1 {
		t.Fatalf("authorization happened after retrieval: %v calls=%d", err, sources.retrievalCalls)
	}
}
func TestG2WebSearchAdapterUsesTenantProviderAndKeepsSummaryLowerTier(t *testing.T) {
	db := g2AdapterDB(t)
	projects := &g2AdapterProjects{}
	sources := &g2AdapterSources{}
	web := &g2AdapterWeb{}
	adapter := NewG2ResearchAdapter(db, projects, sources, nil, nil, web)
	results, _, err := adapter.SearchResearch(context.Background(), core.Actor{TenantID: 7, UserID: "owner", Role: types.TenantRoleOwner}, "project", core.ResearchTask{ProviderID: "synthetic-provider"}, "合成检索")
	if err != nil || web.calls != 1 || web.tenantID != 7 || web.providerID != "synthetic-provider" || web.config.CompressionMethod != "none" {
		t.Fatalf("tenant provider bypassed: %v %+v", err, web)
	}
	if len(results) != 1 || results[0].Level != "unread_search_summary" || results[0].QuotedTextHash != "" || results[0].SourceID != "" || results[0].Author != "" {
		t.Fatalf("search summary became evidence: %+v", results)
	}
}
func TestG2OriginalInspectionReusesFetcherAndRequiresCurrentProvider(t *testing.T) {
	db := g2AdapterDB(t)
	fetch := &g2AdapterFetcher{text: "合成原始页面正文，尚未进行学术核对。"}
	adapter := NewG2ResearchAdapter(db, &g2AdapterProjects{}, &g2AdapterSources{}, nil, nil, nil)
	adapter.fetcher = fetch
	task := core.ResearchTask{ProviderID: "synthetic-provider"}
	result := core.ResearchResult{ID: "result", URL: "https://example.org/synthetic", Level: "unread_search_summary"}
	inspected, err := adapter.InspectResearchOriginal(context.Background(), core.Actor{TenantID: 7, UserID: "owner", Role: types.TenantRoleOwner}, "project", task, result)
	if err != nil || fetch.calls != 1 || inspected.Status != "inspected_original" || inspected.Level != "background_lead" || inspected.QuotedTextHash != g2AdapterHash(fetch.text) {
		t.Fatalf("original inspection: %+v %v", inspected, err)
	}
	if err = db.Exec("DELETE FROM web_search_providers WHERE id = ?", "synthetic-provider").Error; err != nil {
		t.Fatal(err)
	}
	if _, err = adapter.InspectResearchOriginal(context.Background(), core.Actor{TenantID: 7, UserID: "owner", Role: types.TenantRoleOwner}, "project", task, result); !errors.Is(err, core.ErrSourceUnavailable) || fetch.calls != 1 {
		t.Fatalf("revoked provider fetched: %v calls=%d", err, fetch.calls)
	}
}

func TestG2ProjectOnlyResearchRoundRecordsActualHybridIdentityWithoutInventedWebProvider(t *testing.T) {
	projects := &g2AdapterProjects{}
	sources := &g2AdapterSources{assets: []evidence.Asset{{ID: "asset", AssetRevision: 1, ProcessingState: evidence.AssetStateReady}}}
	adapter := NewG2ResearchAdapter(nil, projects, sources, nil, nil, nil)
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
