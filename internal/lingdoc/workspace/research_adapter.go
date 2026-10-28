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
	"github.com/Tencent/WeKnora/internal/lingdoc/academic/catalog"
	"github.com/Tencent/WeKnora/internal/lingdoc/academic/crossref"
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
	db                    *gorm.DB
	projects              core.AssetUseAuthorizer
	sources               SourceApplicationService
	shares                interfaces.KBShareService
	knowledge             interfaces.KnowledgeService
	web                   interfaces.WebSearchService
	academic              crossref.Client
	fetcher               originalFetcher
	globalAcademicCatalog catalog.Snapshot
	academicBudgetLedger  core.ProjectResearchBudgetLedger
}

type ResearchAdapterOption func(*ResearchAdapter)

func WithResearchBudgetLedger(ledger core.ProjectResearchBudgetLedger) ResearchAdapterOption {
	return func(adapter *ResearchAdapter) { adapter.academicBudgetLedger = ledger }
}

func NewResearchAdapter(db *gorm.DB, projects core.AssetUseAuthorizer, sources SourceApplicationService, shares interfaces.KBShareService, knowledge interfaces.KnowledgeService, web interfaces.WebSearchService, options ...ResearchAdapterOption) *ResearchAdapter {
	adapter := &ResearchAdapter{db: db, projects: projects, sources: sources, shares: shares, knowledge: knowledge, web: web, fetcher: webfetch.NewFetcher()}
	for _, option := range options {
		option(adapter)
	}
	return adapter
}

// ConfigureAcademicResearchPolicy installs the approved global academic
// source snapshot and the durable per-project quota ledger. Both are required
// before Crossref metadata can be queried or retained. The repository has no
// approved production allowlist or quota policy yet. The composition installs
// a durable ledger, but its absent policy and the absent catalog fail closed.
func (r *ResearchAdapter) ConfigureAcademicResearchPolicy(global catalog.Snapshot, budget core.ProjectResearchBudgetLedger) error {
	if strings.TrimSpace(global.Version) != core.AcademicGlobalCatalogVersion || len(global.Rules) == 0 || budget == nil {
		return core.ErrInvalidRequest
	}
	global.Rules = slices.Clone(global.Rules)
	seenRuleIDs := make(map[string]struct{}, len(global.Rules))
	for i := range global.Rules {
		rule := &global.Rules[i]
		if strings.TrimSpace(rule.ID) == "" || strings.TrimSpace(rule.Version) == "" || strings.TrimSpace(rule.SourceType) == "" || strings.TrimSpace(rule.Maintainer) == "" || rule.EffectiveAt.IsZero() || strings.TrimSpace(rule.VenueName) == "" && len(rule.VenueIDs) == 0 {
			return core.ErrInvalidRequest
		}
		if _, duplicate := seenRuleIDs[rule.ID]; duplicate {
			return core.ErrInvalidRequest
		}
		seenRuleIDs[rule.ID] = struct{}{}
		rule.VenueIDs = slices.Clone(rule.VenueIDs)
		rule.AllowedLicenseURLs = slices.Clone(rule.AllowedLicenseURLs)
	}
	r.globalAcademicCatalog = global
	r.academicBudgetLedger = budget
	return nil
}

func (r *ResearchAdapter) academicResearchPolicyReady() error {
	if strings.TrimSpace(r.globalAcademicCatalog.Version) != core.AcademicGlobalCatalogVersion || len(r.globalAcademicCatalog.Rules) == 0 {
		return core.ErrAcademicGlobalCatalogUnavailable
	}
	if r.academicBudgetLedger == nil {
		return core.ErrResearchBudgetUnavailable
	}
	return nil
}

func (r *ResearchAdapter) ReserveResearchRun(ctx context.Context, actor core.Actor, projectID, runID string, task core.ResearchTask) error {
	if task.ProviderID == core.AcademicCrossrefProviderID {
		if err := r.academicResearchPolicyReady(); err != nil {
			return err
		}
	}
	if r.academicBudgetLedger == nil {
		return core.ErrResearchBudgetUnavailable
	}
	return r.academicBudgetLedger.Reserve(ctx, actor, projectID, runID, task)
}

func (r *ResearchAdapter) SettleResearchRun(ctx context.Context, actor core.Actor, projectID, runID string, usage core.ResearchUsageLedger) error {
	if r.academicBudgetLedger == nil {
		return core.ErrResearchBudgetUnavailable
	}
	return r.academicBudgetLedger.Settle(ctx, actor, projectID, runID, usage)
}

func (r *ResearchAdapter) ReserveResearchModelCall(ctx context.Context, actor core.Actor, projectID, turnID string) error {
	if r.academicBudgetLedger == nil {
		return core.ErrResearchBudgetUnavailable
	}
	return r.academicBudgetLedger.ReserveModelCall(ctx, actor, projectID, turnID)
}

func (r *ResearchAdapter) SettleResearchModelCall(ctx context.Context, actor core.Actor, projectID, turnID string, usage core.ResearchModelCallUsage) error {
	if r.academicBudgetLedger == nil {
		return core.ErrResearchBudgetUnavailable
	}
	return r.academicBudgetLedger.SettleModelCall(ctx, actor, projectID, turnID, usage)
}

func (r *ResearchAdapter) ValidateAcademicResearchResult(ctx context.Context, actor core.Actor, projectID string, result core.ResearchResult, projectCatalog core.AcademicSourceCatalog) error {
	if result.Provider != "crossref" {
		return nil
	}
	if err := r.academicResearchPolicyReady(); err != nil {
		return err
	}
	// The caller supplies the project catalog read by the active transaction.
	// Reopening the shared pool here would deadlock a single-connection SQLite
	// deployment and would validate against a different transaction snapshot.
	if projectCatalog.ProjectID != projectID {
		return core.ErrInvalidState
	}
	currentPolicySnapshot := academicCatalogSnapshot(r.globalAcademicCatalog, projectCatalog)
	if strings.TrimSpace(result.PolicySnapshotVersion) == "" || result.PolicySnapshotVersion != currentPolicySnapshot.Version {
		return core.ErrVersionConflict
	}
	return nil
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
func (r *ResearchAdapter) ReadResearchSource(ctx context.Context, actor core.Actor, projectID, sourceID string) (core.ResearchSource, error) {
	return r.readResearchSource(ctx, actor, projectID, "", sourceID)
}

// ReadResearchSourceForChapter reads one research source under an explicit
// chapter scope. The function scope is checked before the excerpt is fetched.
func (r *ResearchAdapter) ReadResearchSourceForChapter(ctx context.Context, actor core.Actor, projectID, chapterID, sourceID string) (core.ResearchSource, error) {
	if strings.TrimSpace(chapterID) == "" || strings.TrimSpace(chapterID) != chapterID {
		return core.ResearchSource{}, core.ErrInvalidRequest
	}
	return r.readResearchSource(ctx, actor, projectID, chapterID, sourceID)
}

func (r *ResearchAdapter) readResearchSource(ctx context.Context, actor core.Actor, projectID, chapterID, sourceID string) (core.ResearchSource, error) {
	if r.db == nil || r.sources == nil || r.projects == nil {
		return core.ResearchSource{}, core.ErrDependencyUnavailable
	}
	ctx = researchCallerContext(ctx, actor)
	assets, err := r.sources.ListAssets(ctx, actor, projectID)
	if err != nil {
		return core.ResearchSource{}, err
	}
	var chunk types.Chunk
	if err = r.db.WithContext(ctx).Where("id = ?", sourceID).First(&chunk).Error; err != nil {
		return core.ResearchSource{}, core.ErrSourceUnavailable
	}
	var asset evidence.Asset
	for _, item := range assets {
		if item.KnowledgeID == chunk.KnowledgeID {
			asset = item
			break
		}
	}
	if asset.ID == "" {
		return core.ResearchSource{}, core.ErrSourceUnavailable
	}
	if err = r.authorizeAssetScope(ctx, actor, projectID, chapterID, []string{asset.ID}); err != nil {
		return core.ResearchSource{}, err
	}
	source, err := r.sources.GetSource(ctx, actor, projectID, sourceID)
	if err != nil {
		return core.ResearchSource{}, err
	}
	if source.AssetID != asset.ID || asset.AssetRevision != source.AssetRevision {
		return core.ResearchSource{}, core.ErrSourceUnavailable
	}
	stable := false
	if expandableChunkType(chunk.ChunkType) && source.Status == evidence.SourceAvailable {
		original, available, originErr := r.originReader().OriginText(ctx, asset.KnowledgeID)
		if originErr != nil {
			return core.ResearchSource{}, originErr
		}
		stable = available && evidence.VerbatimAt(original, chunk.StartAt, chunk.EndAt, source.QuotedText)
	}
	allowed, err := r.modelUseAllowed(ctx, actor, projectID, asset.ID, asset.KnowledgeID)
	if err != nil {
		return core.ResearchSource{}, err
	}
	return core.ResearchSource{SourceID: source.ID, AssetID: asset.ID, AssetRevision: asset.AssetRevision, KnowledgeID: asset.KnowledgeID, Title: asset.Title, Locator: source.Locator, QuotedText: source.QuotedText, QuotedTextHash: source.QuotedTextHash, Status: string(source.Status), ProcessingState: string(asset.ProcessingState), StableOriginal: stable, ModelUseAllowed: allowed}, nil
}

// originReader 组装本适配器的原文读取器，构造与 SourceRuntime.origins() 同款：
// dbKnowledgeReader 携带存储字节能力（KnowledgeFileOriginReader）。
//
// 上传资料的 file_path 是存储引用（resource://…），不是文件系统路径；不接
// 存储端口，每份上传资料都读不到原文，StableOriginal 恒为 false，真实
// source-anchor 只能答 evidence_incomplete（#623）。knowledge 服务已满足
// KnowledgeFileSource 端口（GetKnowledgeFile）；它缺席时读取器退回本地
// 路径直读，与接端口之前的行为一致。
func (r *ResearchAdapter) originReader() evidence.OriginReader {
	return evidence.NewOriginReader(dbKnowledgeReader{db: r.db, files: KnowledgeFileOriginReader{Service: r.knowledge}})
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

func (r *ResearchAdapter) CheckAssetScope(ctx context.Context, actor core.Actor, projectID string, assetIDs []string) ([]core.ProjectAssetVersion, error) {
	return r.checkAssetScope(ctx, actor, projectID, "", assetIDs)
}

// CheckAssetScopeForChapter checks the asset scope under an explicit chapter.
func (r *ResearchAdapter) CheckAssetScopeForChapter(ctx context.Context, actor core.Actor, projectID, chapterID string, assetIDs []string) ([]core.ProjectAssetVersion, error) {
	if strings.TrimSpace(chapterID) == "" || strings.TrimSpace(chapterID) != chapterID {
		return nil, core.ErrInvalidRequest
	}
	return r.checkAssetScope(ctx, actor, projectID, chapterID, assetIDs)
}

func (r *ResearchAdapter) checkAssetScope(ctx context.Context, actor core.Actor, projectID, chapterID string, assetIDs []string) ([]core.ProjectAssetVersion, error) {
	if r.projects == nil || r.sources == nil {
		return nil, core.ErrDependencyUnavailable
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
	out := make([]core.ProjectAssetVersion, 0, len(sorted))
	for _, id := range sorted {
		asset, ok := allowed[id]
		if !ok {
			return nil, core.ErrSourceUnavailable
		}
		out = append(out, core.ProjectAssetVersion{AssetID: id, AssetRevision: asset.AssetRevision})
	}
	if err = r.authorizeAssetScope(ctx, actor, projectID, chapterID, sorted); err != nil {
		return nil, err
	}
	for _, id := range sorted {
		if allowed[id].ProcessingState != evidence.AssetStateReady {
			return nil, core.ErrSourceUnavailable
		}
	}
	return out, nil
}

// authorizeAssetScope asks the project's own resource batch authorization
// whether these assets may be used in this scope, and with which chapter.
//
// The batch is one decision for the whole set: a caller that checked them one
// by one would learn the same answer several times and could disagree with
// itself halfway through.
func (r *ResearchAdapter) authorizeAssetScope(ctx context.Context, actor core.Actor, projectID, chapterID string, assetIDs []string) error {
	if r.projects == nil {
		return core.ErrPermissionSchemaUnavailable
	}
	refs := make([]ResourceRef, 0, len(assetIDs))
	for _, id := range assetIDs {
		var ref ResourceRef
		var err error
		if chapterID == "" {
			ref, err = NewAssetResourceRef(id)
		} else {
			ref, err = NewAssetResourceRefForChapter(id, chapterID)
		}
		if err != nil {
			return core.ErrInvalidRequest
		}
		refs = append(refs, ref)
	}
	if len(refs) == 0 {
		return core.ErrInvalidRequest
	}
	decisions, err := r.projects.AuthorizeResourceBatch(ctx, actor, projectID, AuthorizationResearch, refs)
	if err != nil {
		return err
	}
	if len(decisions) != len(refs) {
		return core.ErrPermissionSchemaUnavailable
	}
	for i, decision := range decisions {
		if decision.Resource != refs[i] {
			return core.ErrPermissionSchemaUnavailable
		}
		if !decision.Allowed {
			return core.ErrSourceUnavailable
		}
	}
	return nil
}
func (r *ResearchAdapter) SearchResearch(ctx context.Context, actor core.Actor, projectID string, task core.ResearchTask, query string) ([]core.ResearchResult, string, error) {
	if r.projects == nil || r.sources == nil {
		return nil, "real", core.ErrDependencyUnavailable
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
	materialFailure := ""
	if len(task.AssetIDs) > 0 {
		sources, err := r.sources.RetrieveSources(ctx, actor, projectID, RetrieveSourcesInput{Query: query, AssetIDs: task.AssetIDs, ChapterID: task.ChapterID})
		if err != nil {
			// The project's own index being degraded must not hide a
			// configured external provider (#335): record the failure and
			// keep going, then report it as a partial failure if the
			// external path produced hits.
			materialFailure = core.ResearchFailureDetail(err)
		} else {
			for _, source := range sources {
				current, err := r.readResearchSource(ctx, actor, projectID, task.ChapterID, source.ID)
				if err != nil {
					materialFailure = core.ResearchFailureDetail(err)
					break
				}
				level := "background_lead"
				if current.StableOriginal && current.Status == string(evidence.SourceAvailable) {
					level = "candidate_evidence"
				}
				out = append(out, core.ResearchResult{ID: researchTextHash(source.ID + "|" + strconv.Itoa(source.AssetRevision) + "|" + source.QuotedTextHash), Level: level, SourceID: source.ID, AssetID: source.AssetID, AssetRevision: source.AssetRevision, Locator: source.Locator, QuotedTextHash: source.QuotedTextHash, Title: current.Title, RetrievedAt: time.Now().UTC(), Provider: "weknora_hybrid", SourceType: "project_asset", QualityNote: "检索命中；需用户打开原文核对，层级不表示学术正确", Uncertainty: "作者、发表日期和适用性未自动核实", Status: string(source.Status)})
			}
		}
	}
	if task.ProviderID != "" {
		if task.ProviderID == core.AcademicCrossrefProviderID {
			results, err := r.searchCrossref(ctx, actor, projectID, task, query)
			if err != nil {
				return out, "real", err
			}
			out = append(out, results...)
			return out, "real", nil
		}
		if r.web == nil {
			return out, "real", core.ErrDependencyUnavailable
		}
		var tenant types.Tenant
		if r.db == nil {
			return out, "real", core.ErrDependencyUnavailable
		}
		if err := r.db.WithContext(ctx).Where("id = ?", actor.TenantID).First(&tenant).Error; err != nil {
			return out, "real", &core.ResearchSearchError{Material: materialFailure, Provider: core.ResearchFailureDetail(err)}
		}
		config := types.EffectiveWebSearchConfig(tenant.WebSearchConfig)
		config.CompressionMethod = "none"
		if config.MaxResults > 20 {
			config.MaxResults = 20
		}
		results, err := r.web.Search(ctx, task.ProviderID, config, query)
		if err != nil {
			return out, "real", &core.ResearchSearchError{Material: materialFailure, Provider: core.ResearchFailureDetail(err)}
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
	if materialFailure != "" {
		return out, "real", &core.ResearchSearchError{Material: materialFailure, Partial: len(out) > 0}
	}
	return out, "real", nil
}

func (r *ResearchAdapter) searchCrossref(ctx context.Context, actor core.Actor, projectID string, task core.ResearchTask, query string) ([]core.ResearchResult, error) {
	if r.db == nil {
		return nil, core.ErrDependencyUnavailable
	}
	if err := r.academicResearchPolicyReady(); err != nil {
		return nil, err
	}
	projectCatalog, err := r.loadAcademicSourceCatalog(ctx, projectID)
	if err != nil {
		return nil, err
	}
	snapshot := academicCatalogSnapshot(r.globalAcademicCatalog, projectCatalog)
	if task.Basis.SourceCatalogVersion != "" {
		currentCatalogVersion, versionErr := r.ResearchCatalogVersion(ctx, actor, projectID, task)
		if versionErr != nil {
			return nil, versionErr
		}
		if currentCatalogVersion != task.Basis.SourceCatalogVersion {
			return nil, core.ErrVersionConflict
		}
	}
	works, err := r.academic.SearchWithDateRange(ctx, query, 20, task.DateFrom, task.DateTo)
	if err != nil {
		return nil, err
	}
	// A Crossref update-to edge points from the notice to the updated DOI.
	// Use only notices already present in this response: this does not add
	// unbudgeted requests, and absence of an edge never proves non-retraction.
	reportedRetractions := crossrefRetractionTargets(works)
	now := time.Now().UTC()
	results := make([]core.ResearchResult, 0, len(works))
	seen := make(map[string]string, len(works))
	for index, work := range works {
		key := academicWorkDedupKey(work)
		if key == "" {
			continue
		}
		id := researchTextHash("crossref|" + key)
		duplicateOf := ""
		duplicateStatus := "unique"
		if firstID, exists := seen[key]; exists {
			duplicateStatus = "duplicate"
			duplicateOf = firstID
			id = researchTextHash("crossref-duplicate|" + key + "|" + strconv.Itoa(index))
		} else {
			seen[key] = id
		}
		sourceType := crossrefSourceType(work.Type)
		licenseURLs := make([]string, 0, len(work.Licenses))
		for _, license := range work.Licenses {
			if strings.TrimSpace(license.URL) != "" {
				licenseURLs = append(licenseURLs, license.URL)
			}
		}
		retractionReported := reportedRetractions[strings.ToLower(strings.TrimSpace(work.DOI))]
		evaluation := catalog.EvaluateAt(snapshot, catalog.Work{
			DOI: work.DOI, SourceType: sourceType, Journal: work.Journal,
			ISSNs: work.ISSN, VenueIDs: work.ISSN, LicenseURLs: licenseURLs,
			// update-to describes an update *by* this record and does not prove
			// this record itself was retracted.
			Retracted: retractionReported,
		}, now)
		retractionStatus := "unknown"
		status := "metadata_only"
		level := "discovery_entry"
		if work.IsRetractionNotice() {
			retractionStatus = "retraction_notice"
			status = "retraction_notice"
		} else if retractionReported {
			retractionStatus = "retracted"
			status = "retracted"
		} else if evaluation.Status == catalog.StatusApproved {
			level = "background_lead"
		} else {
			status = "out_of_policy"
		}
		authors := crossrefAuthors(work.Authors)
		uncertainty := "Crossref 元数据记录；未获取原文或摘要，不可作为证据。未检出撤稿记录不代表已核实未撤稿。"
		if retractionStatus == "retraction_notice" {
			uncertainty = "该记录包含指向其他论文的撤稿更新；它是通知而非被撤稿论文。未获取原文，不可作为证据。"
		} else if retractionStatus == "retracted" {
			uncertainty = "本次检索结果中有撤稿通知指向该论文 DOI；请核对原始公告，不能作为资料候选。"
		}
		if duplicateStatus == "duplicate" {
			status = "duplicate"
			level = "discovery_entry"
		}
		if strings.TrimSpace(work.DOI) == "" {
			status = "missing_identifier"
			level = "discovery_entry"
			uncertainty = "Crossref 记录缺少 DOI，无法稳定追溯；已阻止作为资料候选。"
		}
		if strings.TrimSpace(work.Title) == "" {
			status = "missing_title"
			level = "discovery_entry"
			uncertainty = "Crossref 记录缺少标题，不能作为资料候选。"
		}
		results = append(results, core.ResearchResult{
			ID: id, Level: level, Title: work.Title, Author: authors,
			PublishedAt: work.PublishedDate, DOI: work.DOI, Journal: work.Journal,
			ISSNs: slices.Clone(work.ISSN), LicenseURLs: licenseURLs,
			LicenseStatus: string(evaluation.LicenseStatus), CatalogVersion: projectCatalog.SourceCatalogVersion,
			PolicySnapshotVersion: snapshot.Version,
			PolicyStatus:          string(evaluation.Status), PolicyRuleID: evaluation.RuleID,
			RetractionStatus: retractionStatus, DuplicateStatus: duplicateStatus, DuplicateOf: duplicateOf,
			AcquisitionStatus: "metadata_only", RetrievedAt: now, Provider: "crossref",
			SourceType: sourceType, QualityNote: "Crossref 只提供书目元数据；目录命中仅表示项目策略允许保留元数据，不代表期刊质量或原文许可。",
			Uncertainty: uncertainty, Status: status,
		})
	}
	return results, nil
}

func crossrefRetractionTargets(works []crossref.Work) map[string]bool {
	targets := make(map[string]bool)
	for _, notice := range works {
		noticeDOI := strings.TrimSpace(notice.DOI)
		if noticeDOI == "" {
			continue
		}
		for _, update := range notice.UpdateTo {
			target := strings.TrimSpace(update.DOI)
			if target != "" && !strings.EqualFold(target, noticeDOI) && strings.EqualFold(strings.TrimSpace(update.Type), "retraction") {
				targets[strings.ToLower(target)] = true
			}
		}
	}
	return targets
}

func (r *ResearchAdapter) loadAcademicSourceCatalog(ctx context.Context, projectID string) (core.AcademicSourceCatalog, error) {
	if r.db == nil {
		return core.AcademicSourceCatalog{}, core.ErrDependencyUnavailable
	}
	out := core.AcademicSourceCatalog{ProjectID: projectID, GlobalVersion: core.AcademicGlobalCatalogVersion, Rules: []core.AcademicSourceRule{}}
	var row struct {
		Version     int64
		PayloadJSON string
	}
	err := r.db.WithContext(ctx).Table("lingdoc_g2_objects").
		Select("version", "payload_json").
		Where("project_id = ? AND kind = ? AND id = ?", projectID, core.AcademicSourceCatalogKind, "project").Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		out.SourceCatalogVersion = core.AcademicSourceCatalogVersion(out.Rules)
		return out, nil
	}
	if err != nil {
		return core.AcademicSourceCatalog{}, err
	}
	if row.Version < 1 || json.Unmarshal([]byte(row.PayloadJSON), &out) != nil {
		return core.AcademicSourceCatalog{}, core.ErrInvalidState
	}
	out.ProjectID = projectID
	out.Version = row.Version
	out.GlobalVersion = core.AcademicGlobalCatalogVersion
	out.SourceCatalogVersion = core.AcademicSourceCatalogVersion(out.Rules)
	return out, nil
}

func academicCatalogSnapshot(globalCatalog catalog.Snapshot, sourceCatalog core.AcademicSourceCatalog) catalog.Snapshot {
	rules := make([]catalog.Rule, 0, len(sourceCatalog.Rules))
	for _, rule := range sourceCatalog.Rules {
		rules = append(rules, catalog.Rule{
			ID: rule.ID, Version: strconv.FormatInt(rule.Version, 10), SourceType: rule.SourceType,
			VenueName: rule.VenueName, VenueIDs: slices.Clone(rule.VenueIDs),
			AllowedLicenseURLs: slices.Clone(rule.AllowedLicenseURLs), Maintainer: rule.Maintainer,
			EffectiveAt: rule.EffectiveAt,
		})
	}
	return catalog.Combine(globalCatalog, rules)
}

func crossrefSourceType(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "journal-article":
		return "journal"
	case "proceedings-article":
		return "proceedings"
	default:
		return "other"
	}
}

func crossrefAuthors(authors []crossref.Author) string {
	names := make([]string, 0, len(authors))
	for _, author := range authors {
		name := strings.TrimSpace(author.Name)
		if name == "" {
			name = strings.TrimSpace(strings.TrimSpace(author.Given) + " " + strings.TrimSpace(author.Family))
		}
		if name != "" {
			names = append(names, name)
		}
	}
	return strings.Join(names, "; ")
}

func academicWorkDedupKey(work crossref.Work) string {
	if doi := strings.ToLower(strings.TrimSpace(work.DOI)); doi != "" {
		return "doi:" + doi
	}
	parts := []string{strings.ToLower(strings.Join(strings.Fields(work.Title), " ")), strings.ToLower(strings.Join(strings.Fields(work.Journal), " ")), work.PublishedDate, strings.ToLower(strings.Join(strings.Fields(crossrefAuthors(work.Authors)), " "))}
	if parts[0] == "" {
		return ""
	}
	return "metadata:" + strings.Join(parts, "|")
}

func (r *ResearchAdapter) InspectResearchOriginal(ctx context.Context, actor core.Actor, projectID string, task core.ResearchTask, result core.ResearchResult) (core.ResearchResult, error) {
	if task.ProviderID == core.AcademicCrossrefProviderID || result.Provider == "crossref" {
		return result, core.ErrEvidenceIncomplete
	}
	if r.projects == nil || r.fetcher == nil || task.ProviderID == "" || r.db == nil {
		return result, core.ErrDependencyUnavailable
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
		return result, core.ErrEvidenceIncomplete
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
		return "", core.ErrDependencyUnavailable
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
func (r *ResearchAdapter) ImportResearchCandidate(ctx context.Context, actor core.Actor, projectID, key string, candidate core.ImportCandidate) (core.ImportedAsset, error) {
	if r.projects == nil || r.sources == nil {
		return core.ImportedAsset{}, core.ErrDependencyUnavailable
	}
	ctx = researchCallerContext(ctx, actor)
	if err := r.projects.Authorize(ctx, actor, projectID, "evidence-write"); err != nil {
		return core.ImportedAsset{}, err
	}
	knowledgeID := candidate.KnowledgeID
	if knowledgeID == "" {
		if r.knowledge == nil || candidate.SelectionType != "original_url" || candidate.URL == "" {
			return core.ImportedAsset{}, core.ErrEvidenceIncomplete
		}
		kbID, err := r.importKB(ctx, actor, candidate.KnowledgeBaseID)
		if err != nil {
			return core.ImportedAsset{}, err
		}
		// The existing knowledge repository deduplicates the same imported source
		// snapshot. Different purpose/scope receipts remain distinct candidates.
		existing, findErr := r.knowledge.GetRepository().FindByMetadataKey(ctx, actor.TenantID, kbID, "lingdoc_import_dedup", candidate.DedupKey)
		if findErr != nil && !errors.Is(findErr, gorm.ErrRecordNotFound) {
			return core.ImportedAsset{}, findErr
		}
		if existing != nil {
			knowledgeID = existing.ID
		} else {
			// Existing URL parsing/indexing can call configured remote models. A full import
			// must therefore have explicit consent before any asynchronous worker starts.
			if !candidate.ModelUseAllowed {
				return core.ImportedAsset{}, core.ErrConsentRequired
			}
			knowledge, err := r.knowledge.CreateKnowledgeFromURL(ctx, kbID, candidate.URL, "", "", nil, candidate.Title, nil, "lingdoc_research", nil)
			if err != nil {
				return core.ImportedAsset{}, err
			}
			if knowledge == nil {
				return core.ImportedAsset{}, core.ErrOutcomeUnknown
			}
			knowledgeID = knowledge.ID
			metadata := map[string]any{}
			if len(knowledge.Metadata) > 0 {
				if err = json.Unmarshal(knowledge.Metadata, &metadata); err != nil {
					return core.ImportedAsset{}, core.ErrOutcomeUnknown
				}
			}
			metadata["lingdoc_import_dedup"] = candidate.DedupKey
			metadata["lingdoc_import_original_url"] = candidate.URL
			metadata["lingdoc_import_snapshot_hash"] = candidate.QuotedTextHash
			metadata["lingdoc_import_license"] = candidate.License
			raw, err := json.Marshal(metadata)
			if err != nil {
				return core.ImportedAsset{}, core.ErrOutcomeUnknown
			}
			knowledge.Metadata = types.JSON(raw)
			if err = r.knowledge.UpdateKnowledge(ctx, knowledge); err != nil {
				return core.ImportedAsset{KnowledgeID: knowledgeID}, core.ErrOutcomeUnknown
			}
		}
	}
	asset, _, err := r.sources.BindAsset(ctx, actor, projectID, key, knowledgeID)
	if err != nil {
		return core.ImportedAsset{KnowledgeID: knowledgeID}, err
	}
	return core.ImportedAsset{AssetID: asset.ID, AssetRevision: asset.AssetRevision, ProcessingState: string(asset.ProcessingState), KnowledgeID: knowledgeID}, nil
}
func (r *ResearchAdapter) ResearchImportStatus(ctx context.Context, actor core.Actor, projectID string, candidate core.ImportCandidate) (core.ImportedAsset, error) {
	if r.knowledge == nil || r.sources == nil {
		return core.ImportedAsset{}, core.ErrDependencyUnavailable
	}
	ctx = researchCallerContext(ctx, actor)
	knowledge, err := r.knowledge.GetKnowledgeByID(ctx, candidate.KnowledgeID)
	if err != nil {
		return core.ImportedAsset{}, err
	}
	if knowledge == nil {
		return core.ImportedAsset{}, core.ErrSourceUnavailable
	}
	asset, _, err := r.sources.BindAsset(ctx, actor, projectID, "g2-status-"+candidate.ID, knowledge.ID)
	if err != nil {
		return core.ImportedAsset{}, err
	}
	return core.ImportedAsset{AssetID: asset.ID, AssetRevision: asset.AssetRevision, ProcessingState: string(asset.ProcessingState), KnowledgeID: knowledge.ID, FinalURL: knowledge.Source}, nil
}

var _ core.SourceReader = (*ResearchAdapter)(nil)
var _ core.ResearchSearcher = (*ResearchAdapter)(nil)
var _ core.ResearchInspector = (*ResearchAdapter)(nil)
var _ core.ImportProcessor = (*ResearchAdapter)(nil)

func (r *ResearchAdapter) ResearchCatalogVersion(ctx context.Context, actor core.Actor, projectID string, task core.ResearchTask) (string, error) {
	ctx = researchCallerContext(ctx, actor)
	if r.projects == nil {
		return "", core.ErrDependencyUnavailable
	}
	capability := "research"
	if task.ChapterID != "" {
		capability += ":" + task.ChapterID
	}
	if err := r.projects.Authorize(ctx, actor, projectID, capability); err != nil {
		return "", err
	}
	scope := []core.ProjectAssetVersion{}
	if len(task.AssetIDs) > 0 {
		var err error
		scope, err = r.CheckAssetScope(ctx, actor, projectID, task.AssetIDs)
		if err != nil {
			return "", err
		}
	}
	providerVersion := ""
	if task.ProviderID != "" {
		if task.ProviderID == core.AcademicCrossrefProviderID {
			academic, err := r.loadAcademicSourceCatalog(ctx, projectID)
			if err != nil {
				return "", err
			}
			policySnapshot := academicCatalogSnapshot(r.globalAcademicCatalog, academic)
			providerVersion = "academic:crossref|rest-works-v1|" + policySnapshot.Version
		} else {
			if r.db == nil {
				return "", core.ErrDependencyUnavailable
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
					return "", core.ErrDependencyUnavailable
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
	}
	raw, _ := json.Marshal(struct {
		Assets   []core.ProjectAssetVersion
		Provider string
	}{scope, providerVersion})
	return researchTextHash(string(raw)), nil
}

var _ core.ResearchCatalog = (*ResearchAdapter)(nil)

// NewProjectAssetPolicyGateway always asks the existing gateway first, then
// only removes assets. A agent scope change cannot grant underlying KB access.
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
		return nil, core.ErrDependencyUnavailable
	}
	resolved, err := g.base.ResolveAllowed(ctx, projectID, actor, ids)
	if err != nil {
		return nil, err
	}
	if resolved == nil {
		return nil, core.ErrDependencyUnavailable
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
		return "", core.ErrDependencyUnavailable
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
		if _, err := r.CheckAssetScope(ctx, actor, projectID, task.AssetIDs); err != nil {
			return "", err
		}
		providers = append(providers, "weknora_hybrid")
	}
	if task.ProviderID != "" {
		if task.ProviderID == core.AcademicCrossrefProviderID {
			providers = append(providers, core.AcademicCrossrefProviderID)
		} else {
			if r.db == nil || r.web == nil {
				return "", core.ErrDependencyUnavailable
			}
			var row struct{ ID, Provider string }
			if err := r.db.WithContext(ctx).Table("web_search_providers").Select("id", "provider").Where("id = ? AND tenant_id = ? AND deleted_at IS NULL", task.ProviderID, actor.TenantID).Take(&row).Error; err != nil {
				return "", err
			}
			providers = append(providers, "web_search:"+row.ID+":"+row.Provider)
		}
	}
	if len(providers) == 0 {
		return "", core.ErrInvalidRequest
	}
	return strings.Join(providers, ";"), nil
}

var _ core.ResearchRoundIdentity = (*ResearchAdapter)(nil)
