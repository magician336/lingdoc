package workspace

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/Tencent/WeKnora/internal/evidence"
	"github.com/Tencent/WeKnora/internal/lingdoc/candidateadoption"
	"github.com/Tencent/WeKnora/internal/lingdoc/delivery"
	"github.com/Tencent/WeKnora/internal/lingdoc/sourcepolicy"
	lingdoctemplate "github.com/Tencent/WeKnora/internal/lingdoc/template"
	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
	"github.com/Tencent/WeKnora/internal/types"
)

// DeliveryInputBuilder 是 T12→T13 那一段：把工作区的交付输入补成 T13 的冻结输入。
//
// 同一份内容在仓里曾有四套平行类型（delivery.DeliveryInput、
// candidateadoption.WorkspaceDeliveryInput、docx.Input、以及 docx 演示命令自带的一份）
// 而零条适配器——交付链就是在这一跳断的：T13 的检查与冻结谁都不会被调到。
//
// 它比一次字段搬运多做一件事：把章节引用的 source_id 变成**冻结来源**。
// T13 问的是「这条引用此刻是不是一条完整、冻结且已授权的记录」，只有拿到坐标、
// 引文与所属资料版本才谈得上；而 T12 的交付输入里只有一串 ID。ID 出了进程就丢了
// 失效信息（evidence.Source.Anchor 标了 json:"-"，见 ADR-0001），所以这一跳必须在
// 进程内、在冻结之前完成——这与 T11/T12 的复核是同一个理由，也共用同一台判定器。
//
// 与复核相反的是处置：T11/T12 有一条不可用就整批打回，人改完再说；这里把不可用的
// 那条留在原地（章节仍引用它）而不放进冻结来源，让 T13 的 source_invalid 规则把结论
// 落成一份可读的报告。冻结输入是**诊断的输入**，不是准入的门——把不合格的内容挡在
// 门外，操作者就只看到「不通过」，看不到是哪一条引用坏了。
type DeliveryInputBuilder struct {
	reviewer  sourcepolicy.Reviewer
	gateway   evidence.AssetGateway
	templates delivery.TemplateReader
	// provenance is the optional project-provenance capability. It is nil when
	// the assembly has no full-workflow core, and the provenance checks are
	// then skipped instead of failing to compile. It used to be found with a
	// run-time type assertion on the project port, which let the transport
	// reach any aggregate operation the assertion's interface happened to
	// name; an explicit nullable port keeps the reach at three operations.
	provenance core.ProjectProvenanceApplication
}

func (b *DeliveryInputBuilder) Current(ctx context.Context, actorID string, frozen delivery.DeliveryInput) (bool, error) {
	if b == nil {
		return false, candidateadoption.ErrInvalidState
	}
	checked, actor, ok := recheckContext(ctx, actorID)
	if !ok {
		return false, candidateadoption.ErrSourceAccessDenied
	}
	if err := b.AuthorizeFrozenProvenance(checked, actorID, frozen.ProjectID, frozen); err != nil {
		return false, err
	}
	// Revalidate the declared frozen asset basis even when no source record
	// references it. Source locators alone are not a complete revision check.
	if len(frozen.AssetVersions) > 0 {
		if b.gateway == nil {
			return false, candidateadoption.ErrDependencyUnavailable
		}
		requested := make([]string, 0, len(frozen.AssetVersions))
		seen := make(map[string]bool, len(frozen.AssetVersions))
		for _, asset := range frozen.AssetVersions {
			if asset.AssetID == "" || asset.Revision < 1 || seen[asset.AssetID] {
				return false, nil
			}
			seen[asset.AssetID] = true
			requested = append(requested, asset.AssetID)
		}
		resolved, err := b.gateway.ResolveAllowed(checked, frozen.ProjectID, actor, requested)
		if err != nil {
			return false, err
		}
		if resolved == nil || len(resolved.Denied) > 0 {
			return false, candidateadoption.ErrSourceAccessDenied
		}
		if len(resolved.Allowed) != len(frozen.AssetVersions) {
			return false, nil
		}
		versions := make(map[string]int, len(resolved.Allowed))
		for _, asset := range resolved.Allowed {
			versions[asset.ID] = asset.AssetRevision
		}
		for _, asset := range frozen.AssetVersions {
			if versions[asset.AssetID] != asset.Revision {
				return false, nil
			}
		}
	}
	ids := make([]string, 0, len(frozen.Sources))
	for _, source := range frozen.Sources {
		ids = append(ids, source.ID)
	}
	if b.reviewer == nil {
		return false, candidateadoption.ErrDependencyUnavailable
	}
	verdicts, err := b.reviewer.Review(checked, frozen.ProjectID, actor, ids)
	if err != nil {
		return false, err
	}
	byID := make(map[string]sourcepolicy.Verdict, len(verdicts))
	for _, verdict := range verdicts {
		byID[verdict.SourceID] = verdict
	}
	for _, frozenSource := range frozen.Sources {
		verdict, ok := byID[frozenSource.ID]
		if !ok || !verdict.Usable {
			return false, nil
		}
		current := verdict.Source
		if current.ProjectID != frozenSource.ProjectID || current.AssetID != frozenSource.AssetID ||
			current.AssetRevision != frozenSource.AssetRevision || current.Locator != frozenSource.Locator ||
			current.QuotedTextHash != frozenSource.QuotedTextHash {
			return false, nil
		}
	}
	return true, nil
}

// DeliveryInputBuilder 交出装配好的构建器；依赖不齐时返回 nil，与同一文件里
// CandidateAdoptionSourcePolicy 的守卫同一条：宁可让调用方拿到 nil，
// 也不要交出一个会在运行时静默少一层校验的对象。
func (h *SourceRuntime) DeliveryInputBuilder() DeliveryInputAssembler {
	if h == nil || h.db == nil || h.bindings == nil || h.gateway == nil {
		return nil
	}
	// 交付侧另有一份带 rules 的模板（lingdoctemplate.Template）。工作区自己那份
	// ContractDemoTemplate 是 core.Template，**没有 Rules**——拿它来冻结，
	// T13 会因为没有规则声明把每一次检查都判成 not_evaluated。
	if h.templates == nil {
		return nil
	}
	return &DeliveryInputBuilder{reviewer: h.sourceRevalidator(), gateway: h.gateway, templates: h.templates, provenance: h.provenance}
}

// Build 把 T12 的交付输入补成 T13 的冻结输入。
//
// 身份不全时拒绝构建，不产出一份「来源全被略去」的输入：那样得到的是一份
// 看似诊断完成、实则什么都测不到的 blocked 快照，比直接失败更误导。
func (b *DeliveryInputBuilder) Build(ctx context.Context, actorID string, input candidateadoption.WorkspaceDeliveryInput) (delivery.DeliveryInput, error) {
	if b == nil || b.templates == nil {
		return delivery.DeliveryInput{}, candidateadoption.ErrInvalidState
	}
	if strings.TrimSpace(input.ProjectID) == "" {
		return delivery.DeliveryInput{}, candidateadoption.ErrInvalidRequest
	}
	checked, actor, ok := recheckContext(ctx, actorID)
	if !ok {
		return delivery.DeliveryInput{}, candidateadoption.ErrSourceAccessDenied
	}
	var specProvenance *core.ProjectProvenanceSnapshot
	if b.provenance != nil {
		projects := b.provenance
		caller := types.CallerFromContext(checked)
		a := core.Actor{TenantID: caller.TenantID, UserID: caller.UserID, Role: caller.Role}
		proof, prepared := core.ProjectProvenanceSnapshotFromContext(checked, input.ProjectID)
		if !prepared {
			var err error
			checked, proof, err = projects.PrepareProjectProvenance(checked, a, input.ProjectID, false)
			if err != nil {
				return delivery.DeliveryInput{}, err
			}
		}
		if proof.SpecRevision != int64(input.SpecRevision) || proof.TemplateCopyVersion != input.TemplateCopyVersion {
			return delivery.DeliveryInput{}, candidateadoption.ErrVersionConflict
		}
		if err := projects.ValidateProjectProvenanceSnapshot(checked, a, input.ProjectID, input.Spec, proof, false); err != nil {
			return delivery.DeliveryInput{}, err
		}
		specProvenance = &proof
	}
	var template lingdoctemplate.Template
	var binding *lingdoctemplate.VerifiedRuleSetBinding
	if input.TemplateDefinition != nil {
		template = *input.TemplateDefinition
		if template.RuleSetSnapshot != nil || template.RuleSetRef != nil {
			verified, err := lingdoctemplate.VerifyRuleSetBinding(template)
			if err != nil {
				return delivery.DeliveryInput{}, fmt.Errorf("%w: project template RuleSet binding is not verified", candidateadoption.ErrInvalidState)
			}
			binding = &verified
		}
		contentHash, rulesetHash, hashErr := lingdoctemplate.Hashes(template)
		if hashErr != nil || input.TemplateCopyID == "" || input.TemplateCopyVersion < 1 ||
			contentHash != input.TemplateCopyContentHash || rulesetHash != template.RulesetHash ||
			((template.RuleSetSnapshot != nil || template.RuleSetRef != nil || input.RuleSetSnapshot == nil) && rulesetHash != input.TemplateCopyRulesetHash) ||
			template.ID != input.TemplateID || template.Version != input.TemplateVersion {
			return delivery.DeliveryInput{}, fmt.Errorf("%w: project template copy is invalid or stale", candidateadoption.ErrInvalidState)
		}
	} else {
		var err error
		template, err = b.templates.Get(input.TemplateID, input.TemplateVersion)
		if err != nil {
			// 模板取不到不是「检查不通过」，是装配或数据错了：冻结输入离开模板没有意义
			// （ruleset_hash 与规则声明都由它来）。如实报错，别冻一份没有判据的快照。
			return delivery.DeliveryInput{}, fmt.Errorf("%w: read frozen template %q version %q: %v", candidateadoption.ErrInvalidState, input.TemplateID, input.TemplateVersion, err)
		}
		if template.RuleSetSnapshot != nil || template.RuleSetRef != nil {
			verified, err := lingdoctemplate.VerifyRuleSetBinding(template)
			if err != nil {
				return delivery.DeliveryInput{}, fmt.Errorf("%w: frozen template RuleSet binding is not verified", candidateadoption.ErrInvalidState)
			}
			binding = &verified
		}
	}

	if b.reviewer == nil {
		return delivery.DeliveryInput{}, candidateadoption.ErrDependencyUnavailable
	}
	verdicts, err := b.reviewer.Review(checked, input.ProjectID, actor, referencedSourceIDs(input.Chapters))
	if err != nil {
		return delivery.DeliveryInput{}, err
	}
	sources := frozenSources(verdicts)
	versions := assetVersionsOfSources(sources)
	orderedChapters, err := frozenChaptersInTemplateOrder(input.Chapters, template)
	if err != nil {
		return delivery.DeliveryInput{}, err
	}
	frozen := delivery.DeliveryInput{
		RuleSetSnapshot: input.RuleSetSnapshot, TemplateCopyRulesetHash: input.TemplateCopyRulesetHash, SourceRuleSetID: input.SourceRuleSetID, SourceRuleSetVersion: input.SourceRuleSetVersion, SourceRuleSetHash: input.SourceRuleSetHash, BindingPolicies: input.BindingPolicies, SourcePolicyHash: input.SourcePolicyHash,
		ProjectID:               input.ProjectID,
		ProjectName:             input.ProjectName,
		ProjectVersion:          int(input.ProjectVersion),
		SpecRevision:            input.SpecRevision,
		ContextRevision:         input.ContextRevision,
		Spec:                    cloneStringMap(input.Spec),
		SpecProvenance:          specProvenance,
		Template:                template,
		RuleSetBinding:          binding,
		TemplateCopyID:          input.TemplateCopyID,
		TemplateCopyVersion:     input.TemplateCopyVersion,
		TemplateCopyContentHash: input.TemplateCopyContentHash,
		Chapters:                orderedChapters,
		Sources:                 sources,
		AssetVersions:           versions,
		PolicyAssetIDs:          policyAssetIDsOf(versions),
		// 契约里 prepareRelease 的请求体只有 expected_project_version，没有交付种类；
		// 而这套演示只放行一种交付（delivery.DeliveryKindInternalDemo）。
		DeliveryKind: delivery.DeliveryKindInternalDemo,
	}
	if err := delivery.ValidateFrozenRuleSnapshot(frozen); err != nil {
		return delivery.DeliveryInput{}, fmt.Errorf("%w: %v", candidateadoption.ErrInvalidState, err)
	}
	return frozen, nil
}

// referencedSourceIDs 按章节顺序收集全部引用（去空白、去重）。
//
// 冻结来源是项目级的一份集合，所以要先并起来再复核：同一条引用被两章引用时，
// 只该判一次权、只该冻一条——T13 把重复的冻结来源算作 invalid_frozen_source。
func referencedSourceIDs(chapters []candidateadoption.WorkspaceDeliveryChapter) []string {
	ids := make([]string, 0, len(chapters))
	for _, chapter := range chapters {
		ids = append(ids, chapter.SourceIDs...)
	}
	return normalizeSourceIDs(ids)
}

// frozenSources 只收放行的那批，顺序沿用复核顺序（即引用出现的顺序），
// 逐次构建得到同一份输入、同一枚摘要。
func frozenSources(verdicts []sourcepolicy.Verdict) []delivery.FrozenSource {
	out := make([]delivery.FrozenSource, 0, len(verdicts))
	for _, verdict := range verdicts {
		if !verdict.Usable {
			continue
		}
		source := verdict.Source
		out = append(out, delivery.FrozenSource{
			ID: source.ID, ProjectID: source.ProjectID, AssetID: source.AssetID,
			AssetRevision: source.AssetRevision, Locator: source.Locator,
			QuotedText: source.QuotedText, QuotedTextHash: source.QuotedTextHash,
			DisplayTitle: verdict.DisplayTitle,
		})
	}
	return out
}

// assetVersionsOfSources 按资料去重、按 AssetID 排序。排序不只是好看：它让摘要
// 与确认记录里的 asset_versions 可比（生成侧同样按 AssetID 排序），否则同一份内容
// 换个引用顺序就成了另一份输入。
func assetVersionsOfSources(sources []delivery.FrozenSource) []delivery.AssetVersion {
	seen := make(map[string]struct{}, len(sources))
	out := make([]delivery.AssetVersion, 0, len(sources))
	for _, source := range sources {
		if _, ok := seen[source.AssetID]; ok {
			continue
		}
		seen[source.AssetID] = struct{}{}
		out = append(out, delivery.AssetVersion{AssetID: source.AssetID, Revision: source.AssetRevision})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].AssetID < out[j].AssetID })
	return out
}

// policyAssetIDsOf 是「本次冻结时来源复核放行的那批资料」。它与 asset_versions 同源，
// 所以直接从后者取——两处各算一遍，迟早在去重或排序上分叉。
func policyAssetIDsOf(versions []delivery.AssetVersion) []string {
	out := make([]string, 0, len(versions))
	for _, version := range versions {
		out = append(out, version.AssetID)
	}
	return out
}

func frozenChapters(chapters []candidateadoption.WorkspaceDeliveryChapter) []delivery.SnapshotChapter {
	out := make([]delivery.SnapshotChapter, 0, len(chapters))
	for _, chapter := range chapters {
		out = append(out, delivery.SnapshotChapter{
			ChapterID:        chapter.ChapterID,
			SectionID:        chapter.SectionID,
			Title:            chapter.Title,
			ChapterVersionID: cloneString(chapter.ChapterVersionID),
			BodyMarkdown:     chapter.BodyMarkdown,
			// 与复核同一套归一政策：正文里的 [[source:x]] 标记是原样的，
			// 这里若留着空白或重复，T13 会判 citation_mismatch 或 invalid_source_ids
			// ——那是在报一个由我们自己引入的毛病。
			SourceIDs:      normalizeSourceIDs(chapter.SourceIDs),
			CitationUsages: frozenCitationUsages(chapter.CitationUsages),
			ReviewItems:    frozenReviewItems(chapter.ReviewItems),
			// 没有确认就留 nil：T13 据此报 confirmation_stale，而不是当成「已确认」。
			Confirmation: frozenConfirmation(chapter.Confirmation),
		})
	}
	return out
}

func frozenCitationUsages(values []candidateadoption.CitationUsage) []delivery.CitationUsage {
	out := make([]delivery.CitationUsage, 0, len(values))
	for _, value := range values {
		out = append(out, delivery.CitationUsage{SourceID: value.SourceID, Purpose: value.Purpose, Limitation: value.Limitation})
	}
	return out
}

func frozenReviewItems(items []candidateadoption.ReviewItem) []delivery.ReviewItem {
	out := make([]delivery.ReviewItem, 0, len(items))
	for _, item := range items {
		out = append(out, delivery.ReviewItem{ID: item.ID, Statement: item.Statement, OriginCandidateID: item.OriginCandidateID, Anchor: item.Anchor})
	}
	return out
}

// frozenConfirmation 原样搬运 T12 的确认记录。
//
// 这里**不再判一次有效性**：T12 的读取侧只交出 valid、且与章节版本、研究条件、
// 模板版本都对得上的那一条，判据已经有一份；在这里再写一份，两处迟早会分叉，
// 而分叉的那一天没人知道该信谁。
//
// 但「有效性」这个**事实**要一并搬过去：冻结出来的快照若看不到它，下游就得
// 拿周边字段重新推一遍——那是同一判据的第二份、更弱的副本。搬运事实不改变
// 任何现有判定结果；本票不引入任何新的判定。
func frozenConfirmation(confirmation *candidateadoption.Confirmation) *delivery.Confirmation {
	if confirmation == nil {
		return nil
	}
	valid := confirmation.Valid
	return &delivery.Confirmation{
		ID:               confirmation.ID,
		ChapterID:        confirmation.ChapterID,
		ChapterVersionID: confirmation.ChapterVersionID,
		SpecRevision:     confirmation.SpecRevision,
		AssetVersions:    frozenAssetVersions(confirmation.AssetVersions),
		TemplateVersion:  confirmation.TemplateVersion,
		ActorUserID:      confirmation.ActorUserID,
		CreatedAt:        confirmation.CreatedAt,
		Decisions:        frozenReviewDecisions(confirmation.ReviewDecisions),
		Valid:            &valid,
	}
}

func frozenAssetVersions(versions []candidateadoption.AssetVersion) []delivery.AssetVersion {
	out := make([]delivery.AssetVersion, 0, len(versions))
	for _, version := range versions {
		out = append(out, delivery.AssetVersion{AssetID: version.AssetID, Revision: version.AssetRevision})
	}
	return out
}

func frozenReviewDecisions(decisions []candidateadoption.ReviewDecision) []delivery.ReviewDecision {
	out := make([]delivery.ReviewDecision, 0, len(decisions))
	for _, decision := range decisions {
		out = append(out, delivery.ReviewDecision{ReviewItemID: decision.ReviewItemID, Disposition: decision.Disposition, Reason: decision.Reason})
	}
	return out
}

// cloneString 复制指针本身：构建器交出去的值不该与调用方共享同一个可写目标。
func cloneString(value *string) *string {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

// Only newly assembled inputs are ordered. Existing FrozenInput and export
// records retain their original order and digest.
func frozenChaptersInTemplateOrder(chapters []candidateadoption.WorkspaceDeliveryChapter, template lingdoctemplate.Template) ([]delivery.SnapshotChapter, error) {
	sections := append([]lingdoctemplate.Section(nil), template.Sections...)
	sort.SliceStable(sections, func(i, j int) bool { return sections[i].Order < sections[j].Order })
	ranks := make(map[string]int, len(sections))
	for i, section := range sections {
		ranks[section.ID] = i
	}
	for _, chapter := range chapters {
		if _, ok := ranks[chapter.SectionID]; !ok {
			return nil, fmt.Errorf("%w: chapter section %q is not declared by the bound template", candidateadoption.ErrInvalidState, chapter.SectionID)
		}
	}
	out := frozenChapters(chapters)
	sort.SliceStable(out, func(i, j int) bool {
		a, knownA := ranks[out[i].SectionID]
		b, knownB := ranks[out[j].SectionID]
		if knownA != knownB {
			return knownA
		}
		if !knownA {
			return false
		} // Keep unknown chapters for existing blocked diagnostics.
		return a < b
	})
	return out, nil
}

// The production core owns these provenance checks. Public snapshot JSON is
// only a durable reference to its immutable server pins, never an access proof.
func (b *DeliveryInputBuilder) AuthorizeFrozenProvenance(ctx context.Context, actorID, projectID string, frozen delivery.DeliveryInput) error {
	projects := b.provenance
	if projects == nil {
		// Legacy contract fixtures have no full-workflow core. A persisted
		// provenance snapshot still requires its real validator.
		if frozen.SpecProvenance != nil {
			return candidateadoption.ErrDependencyUnavailable
		}
		return nil
	}
	caller := types.CallerFromContext(ctx)
	if caller.UserID != actorID || frozen.ProjectID != projectID {
		return candidateadoption.ErrSourceAccessDenied
	}
	proof := core.ProjectProvenanceSnapshot{}
	if frozen.SpecProvenance != nil {
		proof = *frozen.SpecProvenance
	}
	return projects.AssertFrozenProjectProvenance(ctx, core.Actor{TenantID: caller.TenantID, UserID: caller.UserID, Role: caller.Role}, projectID, frozen.Spec, proof, false)
}
