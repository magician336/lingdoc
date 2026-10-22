package workspace

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/application/access"
	"github.com/Tencent/WeKnora/internal/clienterror"
	"github.com/Tencent/WeKnora/internal/evidence"
	webfetch "github.com/Tencent/WeKnora/internal/infrastructure/web_fetch"
	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"gorm.io/gorm"
)

// ErrResearchProviderInvalid states that the requested provider_id cannot be
// used by the current tenant.
//
// 对调用方而言，「这个 provider 不存在」与「这个 provider 属于别的租户」是同一
// 件事：本租户用不了它。两种情况必须给同一个对外形态，否则响应就能被用来探测
// 他租户的检索来源目录。
//
// 错误自带对外形态（internal/clienterror），因此 transport 不需要为此再添一条
// 映射分支：只渲染错误自己声明的形状。
var ErrResearchProviderInvalid = clienterror.New(clienterror.Form{
	Status:    400,
	Code:      "invalid_provider",
	Message:   "provider_id 无效：当前租户没有可用的检索来源，请核对 provider_id 是否正确且属于本租户。",
	Retryable: false,
}, "lingdoc: research provider is not available for this tenant")

type originalFetcher interface {
	Fetch(context.Context, string) (string, error)
}

// ResearchAdapter delegates retrieval, URL processing, and source checks to
// the existing WeKnora services. It never creates a second RAG/index pipeline.
type ResearchAdapter struct {
	db        *gorm.DB
	projects  core.CapabilityAuthorizer
	sources   SourceApplicationService
	shares    interfaces.KBShareService
	knowledge interfaces.KnowledgeService
	web       interfaces.WebSearchService
	fetcher   originalFetcher
}

func NewResearchAdapter(db *gorm.DB, projects core.CapabilityAuthorizer, sources SourceApplicationService, shares interfaces.KBShareService, knowledge interfaces.KnowledgeService, web interfaces.WebSearchService) *ResearchAdapter {
	return &ResearchAdapter{db: db, projects: projects, sources: sources, shares: shares, knowledge: knowledge, web: web, fetcher: webfetch.NewFetcher()}
}
func researchCallerContext(ctx context.Context, actor core.Actor) context.Context {
	caller := types.CallerFromContext(ctx)
	// Keep authenticated caller AND all original context capabilities (including
	// TenantAPIKeyScope/Principal). Execution scope is not an authorization grant.
	if caller.UserID == "" && (caller.TenantID == 0 || caller.TenantID == actor.TenantID) {
		caller = types.Caller{TenantID: actor.TenantID, UserID: actor.UserID, Role: actor.Role}
	}
	ctx = types.WithCaller(ctx, caller)
	ctx = types.WithExecutionTenant(ctx, actor.TenantID)
	if caller.UserID != "" {
		ctx = context.WithValue(ctx, types.UserIDContextKey, caller.UserID)
	}
	return ctx
}

func researchTextHash(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}
func (r *ResearchAdapter) ReadG2Source(ctx context.Context, actor core.Actor, projectID, sourceID string) (core.G2Source, error) {
	if r.db == nil || r.sources == nil || r.projects == nil {
		return core.G2Source{}, core.ErrG2Unavailable
	}
	ctx = researchCallerContext(ctx, actor)
	source, err := r.sources.GetSource(ctx, actor, projectID, sourceID)
	if err != nil {
		return core.G2Source{}, err
	}
	assets, err := r.sources.ListAssets(ctx, actor, projectID)
	if err != nil {
		return core.G2Source{}, err
	}
	var asset evidence.Asset
	for _, item := range assets {
		if item.ID == source.AssetID {
			asset = item
			break
		}
	}
	if asset.ID == "" || asset.AssetRevision != source.AssetRevision {
		return core.G2Source{}, core.ErrSourceUnavailable
	}
	var chunk types.Chunk
	if err = r.db.WithContext(ctx).Where("id = ? AND knowledge_id = ?", sourceID, asset.KnowledgeID).First(&chunk).Error; err != nil {
		return core.G2Source{}, err
	}
	stable := false
	if expandableChunkType(chunk.ChunkType) && source.Status == evidence.SourceAvailable {
		origins := NewGORMSourcePorts(r.db, r.shares).Origins
		original, available, originErr := origins.OriginText(ctx, asset.KnowledgeID)
		if originErr != nil {
			return core.G2Source{}, originErr
		}
		stable = available && evidence.VerbatimAt(original, chunk.StartAt, chunk.EndAt, source.QuotedText)
	}
	allowed, err := r.modelUseAllowed(ctx, actor, projectID, asset.ID, asset.KnowledgeID)
	if err != nil {
		return core.G2Source{}, err
	}
	return core.G2Source{SourceID: source.ID, AssetID: asset.ID, AssetRevision: asset.AssetRevision, KnowledgeID: asset.KnowledgeID, Title: asset.Title, Locator: source.Locator, QuotedText: source.QuotedText, QuotedTextHash: source.QuotedTextHash, Status: string(source.Status), ProcessingState: string(asset.ProcessingState), StableOriginal: stable, ModelUseAllowed: allowed}, nil
}
func (r *ResearchAdapter) modelUseAllowed(ctx context.Context, actor core.Actor, projectID, assetID, knowledgeID string) (bool, error) {
	policy, err := core.ReadProjectAssetPolicyFromDB(ctx, r.db, projectID, assetID)
	if err != nil {
		return false, err
	}
	allowed := policy.Scope == "project" || policy.Scope == "team" || policy.Scope == "personal" && policy.CreatedBy == actor.UserID
	if len(policy.AllowedUserIDs) > 0 {
		allowed = allowed && slices.Contains(policy.AllowedUserIDs, actor.UserID)
	}
	return allowed && policy.ModelUseAllowed, nil
}

func (r *ResearchAdapter) CheckG2AssetScope(ctx context.Context, actor core.Actor, projectID string, assetIDs []string) ([]core.G2AssetVersion, error) {
	if r.projects == nil || r.sources == nil {
		return nil, core.ErrG2Unavailable
	}
	ctx = researchCallerContext(ctx, actor)
	if err := r.projects.Authorize(ctx, actor, projectID, "read"); err != nil {
		return nil, err
	}
	assets, err := r.sources.ListAssets(ctx, actor, projectID)
	if err != nil {
		return nil, err
	}
	allowed := map[string]evidence.Asset{}
	for _, asset := range assets {
		allowed[asset.ID] = asset
	}
	sorted := slices.Clone(assetIDs)
	sort.Strings(sorted)
	sorted = slices.Compact(sorted)
	out := make([]core.G2AssetVersion, 0, len(sorted))
	for _, id := range sorted {
		asset, ok := allowed[id]
		if !ok || asset.ProcessingState != evidence.AssetStateReady {
			return nil, core.ErrSourceUnavailable
		}
		out = append(out, core.G2AssetVersion{AssetID: id, AssetRevision: asset.AssetRevision})
	}
	return out, nil
}
func (r *ResearchAdapter) SearchResearch(ctx context.Context, actor core.Actor, projectID string, task core.ResearchTask, query string) ([]core.ResearchResult, string, error) {
	if r.projects == nil || r.sources == nil {
		return nil, "real", core.ErrG2Unavailable
	}
	ctx = researchCallerContext(ctx, actor)
	capability := "research"
	if task.ChapterID != "" {
		capability += ":" + task.ChapterID
	}
	if err := r.projects.Authorize(ctx, actor, projectID, capability); err != nil {
		return nil, "real", err
	}
	out := []core.ResearchResult{}
	if len(task.AssetIDs) > 0 {
		sources, err := r.sources.RetrieveSources(ctx, actor, projectID, RetrieveSourcesInput{Query: query, AssetIDs: task.AssetIDs})
		if err != nil {
			return out, "real", err
		}
		for _, source := range sources {
			current, err := r.ReadG2Source(ctx, actor, projectID, source.ID)
			if err != nil {
				return out, "real", err
			}
			level := "background_lead"
			if current.StableOriginal && current.Status == "available" {
				level = "candidate_evidence"
			}
			out = append(out, core.ResearchResult{ID: researchTextHash(source.ID + "|" + strconv.Itoa(source.AssetRevision) + "|" + source.QuotedTextHash), Level: level, SourceID: source.ID, AssetID: source.AssetID, AssetRevision: source.AssetRevision, Locator: source.Locator, QuotedTextHash: source.QuotedTextHash, Title: current.Title, RetrievedAt: time.Now().UTC(), Provider: "weknora_hybrid", SourceType: "project_asset", QualityNote: "检索命中；需用户打开原文核对，层级不表示学术正确", Uncertainty: "作者、发表日期和适用性未自动核实", Status: string(source.Status)})
		}
	}
	if task.ProviderID != "" {
		if r.web == nil {
			return out, "real", core.ErrG2Unavailable
		}
		var tenant types.Tenant
		if r.db == nil {
			return out, "real", core.ErrG2Unavailable
		}
		if err := r.db.WithContext(ctx).Where("id = ?", actor.TenantID).First(&tenant).Error; err != nil {
			return out, "real", err
		}
		config := types.EffectiveWebSearchConfig(tenant.WebSearchConfig)
		config.CompressionMethod = "none"
		if config.MaxResults > 20 {
			config.MaxResults = 20
		}
		results, err := r.web.Search(ctx, task.ProviderID, config, query)
		if err != nil {
			return out, "real", err
		}
		for _, result := range results {
			if result == nil {
				continue
			}
			parsed, err := url.Parse(result.URL)
			if err != nil || parsed.Host == "" || parsed.User != nil || parsed.Scheme != "https" && parsed.Scheme != "http" {
				continue
			}
			date := ""
			if result.PublishedAt != nil {
				date = result.PublishedAt.Format(time.RFC3339)
			}
			out = append(out, core.ResearchResult{ID: researchTextHash(strings.TrimRight(result.URL, "/")), Level: "unread_search_summary", Title: result.Title, URL: result.URL, PublishedAt: date, RetrievedAt: time.Now().UTC(), Provider: result.Source, SourceType: "web_search_summary", Summary: result.Snippet, QualityNote: "搜索引擎摘要，尚未读取原始页面", Uncertainty: "作者、原文和定位均待核对；搜索摘要不能作证据", Status: "unread"})
		}
	}
	return out, "real", nil
}
func (r *ResearchAdapter) InspectResearchOriginal(ctx context.Context, actor core.Actor, projectID string, task core.ResearchTask, result core.ResearchResult) (core.ResearchResult, error) {
	if r.projects == nil || r.fetcher == nil || task.ProviderID == "" || r.db == nil {
		return result, core.ErrG2Unavailable
	}
	ctx = researchCallerContext(ctx, actor)
	capability := "research"
	if task.ChapterID != "" {
		capability += ":" + task.ChapterID
	}
	if err := r.projects.Authorize(ctx, actor, projectID, capability); err != nil {
		return result, err
	}
	// Check the same tenant-owned provider remains configured before fetching.
	var count int64
	if err := r.db.WithContext(ctx).Table("web_search_providers").Where("id = ? AND tenant_id = ? AND deleted_at IS NULL", task.ProviderID, actor.TenantID).Count(&count).Error; err != nil {
		return result, err
	}
	if count != 1 {
		return result, core.ErrSourceUnavailable
	}
	text, err := r.fetcher.Fetch(ctx, result.URL)
	if err != nil {
		return result, err
	}
	if strings.TrimSpace(text) == "" {
		return result, core.ErrG2EvidenceIncomplete
	}
	result.Locator = fmt.Sprintf("原始页面读取快照 · 字符 0–%d", len([]rune(text)))
	result.QuotedTextHash = researchTextHash(text)
	result.Level = "background_lead"
	result.Status = "inspected_original"
	result.Summary = text
	result.QualityNote = "已读取公开页面快照；导入后仍须选择稳定原文片段建立锚点"
	result.Uncertainty = "网页转换、作者信息和适用范围仍需人工核对；不表示结论已证实"
	result.RetrievedAt = time.Now().UTC()
	return result, nil
}
func (r *ResearchAdapter) importKB(ctx context.Context, actor core.Actor, requested string) (string, error) {
	if r.db == nil || r.shares == nil {
		return "", core.ErrG2Unavailable
	}
	var bases []types.KnowledgeBase
	query := r.db.WithContext(ctx)
	if requested != "" {
		query = query.Where("id = ?", requested)
	} else {
		query = query.Where("tenant_id = ? AND is_temporary = ?", actor.TenantID, false)
	}
	if err := query.Find(&bases).Error; err != nil {
		return "", err
	}
	valid := []string{}
	for _, base := range bases {
		allowed, err := access.NewKBPermissions(ctx, r.shares).Check(base.ID, base.TenantID, types.OrgRoleEditor)
		if err != nil {
			return "", err
		}
		if allowed && base.Type != "faq" {
			valid = append(valid, base.ID)
		}
	}
	if len(valid) != 1 {
		return "", core.ErrInvalidRequest
	}
	return valid[0], nil
}
func (r *ResearchAdapter) ImportResearchCandidate(ctx context.Context, actor core.Actor, projectID, key string, candidate core.ImportCandidate) (core.G2ImportedAsset, error) {
	if r.projects == nil || r.sources == nil {
		return core.G2ImportedAsset{}, core.ErrG2Unavailable
	}
	ctx = researchCallerContext(ctx, actor)
	if err := r.projects.Authorize(ctx, actor, projectID, "evidence-write"); err != nil {
		return core.G2ImportedAsset{}, err
	}
	knowledgeID := candidate.KnowledgeID
	if knowledgeID == "" {
		if r.knowledge == nil || candidate.SelectionType != "original_url" || candidate.URL == "" {
			return core.G2ImportedAsset{}, core.ErrG2EvidenceIncomplete
		}
		kbID, err := r.importKB(ctx, actor, candidate.KnowledgeBaseID)
		if err != nil {
			return core.G2ImportedAsset{}, err
		}
		// The existing knowledge repository deduplicates the same imported source
		// snapshot. Different purpose/scope receipts remain distinct candidates.
		existing, findErr := r.knowledge.GetRepository().FindByMetadataKey(ctx, actor.TenantID, kbID, "lingdoc_import_dedup", candidate.DedupKey)
		if findErr != nil && !errors.Is(findErr, gorm.ErrRecordNotFound) {
			return core.G2ImportedAsset{}, findErr
		}
		if existing != nil {
			knowledgeID = existing.ID
		} else {
			// Existing URL parsing/indexing can call configured remote models. A full import
			// must therefore have explicit consent before any asynchronous worker starts.
			if !candidate.ModelUseAllowed {
				return core.G2ImportedAsset{}, core.ErrG2ConsentRequired
			}
			knowledge, err := r.knowledge.CreateKnowledgeFromURL(ctx, kbID, candidate.URL, "", "", nil, candidate.Title, nil, "lingdoc_research", nil)
			if err != nil {
				return core.G2ImportedAsset{}, err
			}
			if knowledge == nil {
				return core.G2ImportedAsset{}, core.ErrG2OutcomeUnknown
			}
			knowledgeID = knowledge.ID
			metadata := map[string]any{}
			if len(knowledge.Metadata) > 0 {
				if err = json.Unmarshal(knowledge.Metadata, &metadata); err != nil {
					return core.G2ImportedAsset{}, core.ErrG2OutcomeUnknown
				}
			}
			metadata["lingdoc_import_dedup"] = candidate.DedupKey
			metadata["lingdoc_import_original_url"] = candidate.URL
			metadata["lingdoc_import_snapshot_hash"] = candidate.QuotedTextHash
			metadata["lingdoc_import_license"] = candidate.License
			raw, err := json.Marshal(metadata)
			if err != nil {
				return core.G2ImportedAsset{}, core.ErrG2OutcomeUnknown
			}
			knowledge.Metadata = types.JSON(raw)
			if err = r.knowledge.UpdateKnowledge(ctx, knowledge); err != nil {
				return core.G2ImportedAsset{KnowledgeID: knowledgeID}, core.ErrG2OutcomeUnknown
			}
		}
	}
	asset, _, err := r.sources.BindAsset(ctx, actor, projectID, key, knowledgeID)
	if err != nil {
		return core.G2ImportedAsset{KnowledgeID: knowledgeID}, err
	}
	return core.G2ImportedAsset{AssetID: asset.ID, AssetRevision: asset.AssetRevision, ProcessingState: string(asset.ProcessingState), KnowledgeID: knowledgeID}, nil
}
func (r *ResearchAdapter) ResearchImportStatus(ctx context.Context, actor core.Actor, projectID string, candidate core.ImportCandidate) (core.G2ImportedAsset, error) {
	if r.knowledge == nil || r.sources == nil {
		return core.G2ImportedAsset{}, core.ErrG2Unavailable
	}
	ctx = researchCallerContext(ctx, actor)
	knowledge, err := r.knowledge.GetKnowledgeByID(ctx, candidate.KnowledgeID)
	if err != nil {
		return core.G2ImportedAsset{}, err
	}
	if knowledge == nil {
		return core.G2ImportedAsset{}, core.ErrSourceUnavailable
	}
	asset, _, err := r.sources.BindAsset(ctx, actor, projectID, "g2-status-"+candidate.ID, knowledge.ID)
	if err != nil {
		return core.G2ImportedAsset{}, err
	}
	return core.G2ImportedAsset{AssetID: asset.ID, AssetRevision: asset.AssetRevision, ProcessingState: string(asset.ProcessingState), KnowledgeID: knowledge.ID, FinalURL: knowledge.Source}, nil
}

var _ core.G2SourceReader = (*ResearchAdapter)(nil)
var _ core.G2ResearchSearcher = (*ResearchAdapter)(nil)
var _ core.G2ResearchInspector = (*ResearchAdapter)(nil)
var _ core.G2ImportProcessor = (*ResearchAdapter)(nil)

func (r *ResearchAdapter) ResearchCatalogVersion(ctx context.Context, actor core.Actor, projectID string, task core.ResearchTask) (string, error) {
	ctx = researchCallerContext(ctx, actor)
	if r.projects == nil {
		return "", core.ErrG2Unavailable
	}
	capability := "research"
	if task.ChapterID != "" {
		capability += ":" + task.ChapterID
	}
	if err := r.projects.Authorize(ctx, actor, projectID, capability); err != nil {
		return "", err
	}
	scope := []core.G2AssetVersion{}
	if len(task.AssetIDs) > 0 {
		var err error
		scope, err = r.CheckG2AssetScope(ctx, actor, projectID, task.AssetIDs)
		if err != nil {
			return "", err
		}
	}
	providerVersion := ""
	if task.ProviderID != "" {
		if r.db == nil {
			return "", core.ErrG2Unavailable
		}
		var provider struct {
			ID        string
			Provider  string
			UpdatedAt time.Time
		}
		if err := r.db.WithContext(ctx).Table("web_search_providers").Select("id", "provider", "updated_at").Where("id = ? AND tenant_id = ? AND deleted_at IS NULL", task.ProviderID, actor.TenantID).Take(&provider).Error; err != nil {
			// 查不到行有两种成因：provider 根本不存在，或它属于别的租户（查询
			// 已按 tenant_id 过滤）。两者对调用方是同一件事，所以给同一个对外
			// 形态，不泄露他租户的目录。
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return "", ErrResearchProviderInvalid
			}
			return "", err
		}
		providerVersion = provider.ID + "|" + provider.Provider + "|" + provider.UpdatedAt.UTC().Format(time.RFC3339Nano)
		var tenant types.Tenant
		if err := r.db.WithContext(ctx).Where("id = ?", actor.TenantID).First(&tenant).Error; err != nil {
			// 租户配置行缺失同样不是调用方的错：检索策略读不出来，能力不可用。
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return "", core.ErrG2Unavailable
			}
			return "", err
		}
		config := types.EffectiveWebSearchConfig(tenant.WebSearchConfig)
		// Hash policy fields only; credentials never enter audit or version strings.
		policy, _ := json.Marshal(struct {
			MaxResults  int
			IncludeDate bool
			Blacklist   []string
		}{config.MaxResults, config.IncludeDate, config.Blacklist})
		providerVersion += "|" + string(policy)
	}
	raw, _ := json.Marshal(struct {
		Assets   []core.G2AssetVersion
		Provider string
	}{scope, providerVersion})
	return researchTextHash(string(raw)), nil
}

var _ core.G2ResearchCatalog = (*ResearchAdapter)(nil)

// NewProjectAssetPolicyGateway always asks the existing gateway first, then
// only removes assets. A research scope change cannot grant underlying KB access.
func NewProjectAssetPolicyGateway(db *gorm.DB, base evidence.AssetGateway) evidence.AssetGateway {
	return &researchPolicyGateway{db: db, base: base}
}

// NewModelAssetPolicyGateway adds explicit current-version model consent.
// Use it at generation/rewrite/model inputs; ordinary human source reads use
// NewProjectAssetPolicyGateway.
func NewModelAssetPolicyGateway(db *gorm.DB, base evidence.AssetGateway) evidence.AssetGateway {
	return &researchPolicyGateway{db: db, base: base, forModel: true}
}

type researchPolicyGateway struct {
	db       *gorm.DB
	base     evidence.AssetGateway
	forModel bool
}

func (g *researchPolicyGateway) ResolveAllowed(ctx context.Context, projectID string, actor evidence.Actor, ids []string) (*evidence.ResolveResult, error) {
	if g.db == nil || g.base == nil {
		return nil, core.ErrG2Unavailable
	}
	resolved, err := g.base.ResolveAllowed(ctx, projectID, actor, ids)
	if err != nil {
		return nil, err
	}
	if resolved == nil {
		return nil, core.ErrG2Unavailable
	}
	out := &evidence.ResolveResult{Requested: slices.Clone(resolved.Requested), Denied: slices.Clone(resolved.Denied), Allowed: []evidence.Asset{}}
	for _, asset := range resolved.Allowed {
		policy, err := core.ReadProjectAssetPolicyFromDB(ctx, g.db, projectID, asset.ID)
		if err != nil {
			return nil, err
		}
		allowed := policy.Scope == "project" || policy.Scope == "team" || policy.Scope == "personal" && policy.CreatedBy == actor.UserID
		if len(policy.AllowedUserIDs) > 0 {
			allowed = allowed && slices.Contains(policy.AllowedUserIDs, actor.UserID)
		}
		if g.forModel {
			allowed = allowed && policy.ModelUseAllowed && policy.AssetRevision == int64(asset.AssetRevision)
		}
		if !allowed {
			out.Denied = append(out.Denied, evidence.DeniedAsset{AssetID: asset.ID, Reason: evidence.DenyNotAuthorized})
			continue
		}
		out.Allowed = append(out.Allowed, asset)
	}
	return out, nil
}

func (r *ResearchAdapter) ResearchRoundProvider(ctx context.Context, actor core.Actor, projectID string, task core.ResearchTask) (string, error) {
	if r.projects == nil {
		return "", core.ErrG2Unavailable
	}
	ctx = researchCallerContext(ctx, actor)
	capability := "research"
	if task.ChapterID != "" {
		capability += ":" + task.ChapterID
	}
	if err := r.projects.Authorize(ctx, actor, projectID, capability); err != nil {
		return "", err
	}
	providers := []string{}
	if len(task.AssetIDs) > 0 {
		if _, err := r.CheckG2AssetScope(ctx, actor, projectID, task.AssetIDs); err != nil {
			return "", err
		}
		providers = append(providers, "weknora_hybrid")
	}
	if task.ProviderID != "" {
		if r.db == nil || r.web == nil {
			return "", core.ErrG2Unavailable
		}
		var row struct{ ID, Provider string }
		if err := r.db.WithContext(ctx).Table("web_search_providers").Select("id", "provider").Where("id = ? AND tenant_id = ? AND deleted_at IS NULL", task.ProviderID, actor.TenantID).Take(&row).Error; err != nil {
			return "", err
		}
		providers = append(providers, "web_search:"+row.ID+":"+row.Provider)
	}
	if len(providers) == 0 {
		return "", core.ErrInvalidRequest
	}
	return strings.Join(providers, ";"), nil
}

var _ core.G2ResearchRoundIdentity = (*ResearchAdapter)(nil)
