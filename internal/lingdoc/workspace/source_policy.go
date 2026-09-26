package workspace

import (
	"context"
	"errors"
	"sort"
	"strconv"
	"strings"

	"github.com/Tencent/WeKnora/internal/evidence"
	"github.com/Tencent/WeKnora/internal/lingdoc/candidateadoption"
	"github.com/Tencent/WeKnora/internal/types"
	"gorm.io/gorm"
)

// CandidateAdoptionSourcePolicy 把 T09 的来源复核接到 T11（候选采纳）与 T12（章节确认）上。
//
// 契约 §2 要求复核回答的是「这批**已经产出**的引用此刻还能不能当证据」，而不是「能不能
// 重新找一遍」。这决定了它手里不能只有一串 source_id：判「坐标还算不算数」要的是
// StartAt/EndAt 与 ContentRevision，而 evidence.Source.Anchor 标了 json:"-"（ADR-0001；
// 契约里 Source 是 additionalProperties: false，内部字段本就带不出去）——跨进程传进来的
// ID 早已丢掉失效信息，这正是 evidence.SourcePolicy 要求「必须在进程内调用」的理由。
//
// 所以复核在进程内回到分块行上把来源重建出来，走的就是产出侧那一套判据：
// 分块行 → 资料 → ResolveAllowed → SourceResolver → SourcePolicy.Validate，
// 与 http.go 的 getSource 逐字同源。判据只有一份，「什么叫可用」在产出与复核两个时点
// 才不会各自演化。
//
// 装配处把 nil 塞进 SourcePolicy 的后果不是「少一层校验」，而是整条链路对带引用的内容
// 恒不可用：带 source_ids 的候选采纳恒 403，带引用的章节确认恒 422。
func (h *Handler) CandidateAdoptionSourcePolicy() candidateadoption.SourcePolicy {
	if h == nil || h.db == nil || h.bindings == nil || h.gateway == nil {
		return nil
	}
	return &candidateAdoptionSourcePolicy{sourceRevalidator: h.sourceRevalidator()}
}

// WorkspaceSourcePolicy 把同一台判定器接到 T08（章节保存）上。
//
// 它每次调用都**现取** h.CandidateAdoptionSourcePolicy()，而不是在装配时抓一份
// revalidator 存下来：Handler 的 gateway 在装配之后还会被换掉（http_currentness_test.go
// 就是这么注入一份放行的资料授权的；运行期也可能整体换一套底座），抓早了那台判定器就永远
// 停在旧的那一份上。症状是「复核结论与产出侧对不上」——最难查的一类错，因为两边都「有实现」。
//
// 依赖不齐（组装漏了库连接/绑定/网关）时答 ErrDependencyUnavailable：既不去猜一个更宽松的
// 答案，也不答成「授权被拒」——后者会让一次装配失误看着像用户的权限出了问题。
func (h *Handler) WorkspaceSourcePolicy() SourcePolicy {
	return workspaceSourcePolicy{handler: h}
}

type workspaceSourcePolicy struct{ handler *Handler }

// 装配处把 SourcePolicy 交给核心域（core_alias.go 的 NewService），断言失败是静默的
// ——把「静默少一层校验」变成「编译不过」。
var _ SourcePolicy = workspaceSourcePolicy{}

func (p workspaceSourcePolicy) Validate(ctx context.Context, projectID, actorID string, sourceIDs []string) error {
	// 复用产出侧那一个 nil 守卫，不在这里再抄一遍判据：依赖齐备与否只该有一处定义。
	policy := p.handler.CandidateAdoptionSourcePolicy()
	if policy == nil {
		return candidateadoption.ErrDependencyUnavailable
	}
	return policy.Validate(ctx, projectID, actorID, sourceIDs)
}

// sourceRevalidator 组装产出侧与复核侧共用的那台判定器。调用方负责确认依赖齐备：
// 它只被那几个带 nil 守卫的装配入口调用。
func (h *Handler) sourceRevalidator() sourceRevalidator {
	origins := evidence.NewOriginReader(dbKnowledgeReader{db: h.db})
	return sourceRevalidator{
		db: h.db, bindings: h.bindings, gateway: h.gateway, origins: origins,
		policy: evidence.NewSourcePolicy(h.gateway, origins, h.bindings),
	}
}

// candidateAdoptionSourcePolicy 同时满足 T11 的 SourcePolicy 与 T12 的
// ConfirmationSourcePolicy：两者问的是同一件事，只是 T12 还要把被引用资料的当前版本交出来
// 给确认记录钉住——确认之后资料再前进就该判失效，而不是静默沿用旧坐标。
type candidateAdoptionSourcePolicy struct {
	sourceRevalidator
}

// 装配处靠类型断言才能把复核接给确认服务（candidate_adoption_http.go:26），而断言失败是
// 静默的——把「静默少一层校验」变成「编译不过」。
var _ candidateadoption.ConfirmationSourcePolicy = (*candidateAdoptionSourcePolicy)(nil)

// Validate 是 T11 的采纳前复核：只回答这批引用还能不能用。
func (p *candidateAdoptionSourcePolicy) Validate(ctx context.Context, projectID, actorID string, sourceIDs []string) error {
	_, err := p.recheck(ctx, projectID, actorID, sourceIDs)
	return err
}

// ValidateCurrent 是 T12 的确认前复核：同一套判据，另须给出被引用资料的当前版本。
func (p *candidateAdoptionSourcePolicy) ValidateCurrent(ctx context.Context, projectID, actorID string, sourceIDs []string) ([]candidateadoption.AssetVersion, error) {
	return p.recheck(ctx, projectID, actorID, sourceIDs)
}

// recheck 是两条路径共用的一次复核。空集不是「校验通过」而是「没有可校验的东西」：
// 调用方（candidate_adoption.go:156、confirmation.go:114）已在空集上短路，这里保持同一语义。
func (p *candidateAdoptionSourcePolicy) recheck(ctx context.Context, projectID, actorID string, sourceIDs []string) ([]candidateadoption.AssetVersion, error) {
	if len(sourceIDs) == 0 {
		return nil, nil
	}
	checked, actor, ok := recheckContext(ctx, actorID)
	if !ok || strings.TrimSpace(projectID) == "" {
		// 身份不全就无从判定「此刻还授不授权」，默认拒绝，不去猜一个更宽松的答案。
		return nil, candidateadoption.ErrSourceAccessDenied
	}
	verdicts, err := p.revalidate(checked, projectID, actor, sourceIDs)
	if err != nil {
		return nil, err
	}
	stale, denied := false, false
	usable := make([]evidence.Source, 0, len(verdicts))
	for _, verdict := range verdicts {
		if verdict.Usable {
			usable = append(usable, verdict.Source)
			continue
		}
		if verdict.AssetDeny == "" {
			// 资料层放行、卡在坐标层：资料还在、还授权，只是这枚引用指不回原文了
			// （坐标漂移，或该块已被编辑/重写）。
			stale = true
			continue
		}
		// 资料层不放行：不存在 / 未就绪 / 未授权 / 不属于本项目。四种原因不在措辞上区分，
		// 它们该触发的动作是同一个：拒绝。
		denied = true
	}
	// 坐标失效优先于资料层拒绝，与生成侧 generationSourceValidator 同一条读法：
	// 整批里只要有一条「资料还在、引用失效」，可修的那一类就是它。
	if stale {
		return nil, candidateadoption.ErrStaleInput
	}
	if denied {
		return nil, candidateadoption.ErrSourceAccessDenied
	}
	return assetVersionsOf(usable), nil
}

// recheckContext 把调用上下文补成复核能用的那一份。
//
// 资料网关的读权限判据（kbReadChecker.CanReadKB）会拿调用者与 actor 逐字对账，
// 并要求执行租户一致（modelContextForActor 同款）。复核必须带着同一份调用者
// 上下文去问，否则每一条引用都会被判成越权——这是「复核」与「重新检索」之间
// 唯一一处不能省的差别。
//
// 身份不全（没有租户、没有执行人）时 ok=false：无从判定「此刻还授不授权」，
// 调用方据此默认拒绝，不去猜一个更宽松的答案。
func recheckContext(ctx context.Context, actorID string) (context.Context, evidence.Actor, bool) {
	tenantID, ok := types.TenantIDFromContext(ctx)
	if !ok || tenantID == 0 || strings.TrimSpace(actorID) == "" {
		return nil, evidence.Actor{}, false
	}
	actor := evidence.Actor{UserID: actorID, TenantID: strconv.FormatUint(tenantID, 10)}
	checked := types.WithCaller(ctx, types.Caller{TenantID: tenantID, UserID: actorID, Role: types.TenantRoleViewer})
	return types.WithExecutionTenant(checked, tenantID), actor, true
}

// sourceRevalidator 是「这批已经产出的引用此刻还能不能当证据」这一问的唯一实现。
//
// 它服务两个消费方，两者的**判定**相同而**处置**相反：
//   - T11/T12（人工确认前后的复核）逐项作答后整批拒绝：有一条不可用就带着
//     ErrStaleInput / ErrSourceAccessDenied 打回，人改完再说；
//   - T13（冻结前的交付检查）则把不可用的那条**留在原地**（章节仍引用它）而不放进
//     冻结来源，让 T13 的 source_invalid 规则把结论落成一份可读的报告。
//
// 所以这里交回的是逐条的结论，不替调用方决定整批怎么办。
type sourceRevalidator struct {
	db       *gorm.DB
	bindings *evidence.Bindings
	gateway  evidence.AssetGateway
	origins  evidence.OriginReader
	policy   evidence.SourcePolicy
}

// revalidated 是一次复核对一条引用的回答。
type revalidated struct {
	SourceID string
	// Source 是放行时重建出来的来源；不可用时只带 ID（零值的其余字段）。
	Source evidence.Source
	// DisplayTitle 是这条引用所属资料此刻的标题。T13 的冻结来源要 display_title，
	// 而它是资料的属性、不在 evidence.Source 里。
	DisplayTitle string
	Usable       bool
	// AssetDeny 非空表示卡在资料层（不存在 / 未就绪 / 未授权 / 不属于本项目）；
	// 为空而 Usable=false 表示资料层放行、只是这枚引用指不回原文了。
	AssetDeny evidence.DenyReason
	Detail    string
}

// revalidate 逐条给出结论，顺序与请求一致（去重、去空白后）。
func (p *sourceRevalidator) revalidate(ctx context.Context, projectID string, actor evidence.Actor, sourceIDs []string) ([]revalidated, error) {
	rebuilt, err := p.rebuild(ctx, projectID, actor, sourceIDs)
	if err != nil {
		return nil, err
	}
	if len(rebuilt) == 0 {
		return nil, nil
	}
	sources := make([]evidence.Source, 0, len(rebuilt))
	for _, entry := range rebuilt {
		sources = append(sources, entry.Source)
	}
	result, err := p.policy.Validate(ctx, projectID, actor, sources)
	if err != nil {
		return nil, err
	}
	if result == nil {
		return nil, candidateadoption.ErrDependencyUnavailable
	}
	usable := make(map[string]evidence.Source, len(result.Usable))
	for _, source := range result.Usable {
		usable[source.ID] = source
	}
	unusable := make(map[string]evidence.UnusableSource, len(result.Unusable))
	for _, entry := range result.Unusable {
		unusable[entry.SourceID] = entry
	}

	verdicts := make([]revalidated, 0, len(rebuilt))
	for _, entry := range rebuilt {
		verdict := revalidated{SourceID: entry.Source.ID, DisplayTitle: entry.DisplayTitle}
		if source, ok := usable[entry.Source.ID]; ok {
			verdict.Source, verdict.Usable = source, true
			verdicts = append(verdicts, verdict)
			continue
		}
		entry, ok := unusable[verdict.SourceID]
		if !ok {
			// 每一项请求都必须得到「可用」或「不可用」之一（ValidateResult 的不变量）。
			// 两个都没有说明协作方坏了：如实报错，别把没被复核的当成复核通过。
			return nil, candidateadoption.ErrDependencyUnavailable
		}
		verdict.AssetDeny, verdict.Detail = entry.AssetDeny, entry.Detail
		verdicts = append(verdicts, verdict)
	}
	return verdicts, nil
}

// rebuiltSource 是一条引用在底座上重建出来的样子，连同它所属资料此刻的标题。
type rebuiltSource struct {
	Source       evidence.Source
	DisplayTitle string
}

// rebuild 把已产出的引用从底座上取回来。
//
// 取不回来的（分块行已删、这份知识已不挂在本项目下、资料已被重解析换成另一份知识）
// 不在这里判死：按 §8「逐项作答」把它当成一条不指向任何资料的来源交给复核，由复核
// 给出 not_found。这样「引用失效」只有一个出口，不会多出一套只在本函数里成立的判据。
func (p *sourceRevalidator) rebuild(ctx context.Context, projectID string, actor evidence.Actor, sourceIDs []string) ([]rebuiltSource, error) {
	ids := normalizeSourceIDs(sourceIDs)
	rows, err := p.chunks(ctx, ids)
	if err != nil {
		return nil, err
	}

	type located struct {
		chunk types.Chunk
		asset evidence.Asset
	}
	held := make(map[string]located, len(ids))
	assetIDs := make([]string, 0, len(ids))
	for _, id := range ids {
		chunk, ok := rows[id]
		if !ok {
			continue
		}
		asset, err := p.bindings.AssetForKnowledge(ctx, projectID, chunk.KnowledgeID)
		if errors.Is(err, evidence.ErrAssetNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		held[id] = located{chunk: chunk, asset: asset}
		assetIDs = append(assetIDs, asset.ID)
	}

	// 先把这批引用指向的资料一次问清楚。ResolveAllowed 会顺带刷新资料版本，而复核侧的
	// 判据用的是**刷新后**的那一份；重建一侧若拿刷新前的版本去取坐标，就会报出假的
	// 「这条引用产生于旧版本」。所以这里必须与复核看到的是同一份。
	current := make(map[string]evidence.Asset, len(assetIDs))
	if len(assetIDs) > 0 {
		resolved, err := p.gateway.ResolveAllowed(ctx, projectID, actor, assetIDs)
		if err != nil {
			return nil, err
		}
		for _, asset := range resolved.Allowed {
			current[asset.ID] = asset
		}
	}

	resolver := evidence.NewSourceResolver(p.origins)
	out := make([]rebuiltSource, 0, len(ids))
	for _, id := range ids {
		entry, ok := held[id]
		if !ok {
			out = append(out, rebuiltSource{Source: evidence.Source{ID: id}})
			continue
		}
		asset := entry.asset
		if refreshed, ok := current[asset.ID]; ok {
			asset = refreshed
		}
		// 判据与产出侧同源：坐标取自分块行（StartAt/EndAt/Content，以及标记失效的
		// ContentRevision），Resolve 会拿它们与此刻的原文逐字比对，产出侧与复核
		// 侧因此得到同一个「可用/失效」结论。
		hit := &types.SearchResult{
			ID: entry.chunk.ID, KnowledgeID: entry.chunk.KnowledgeID, ChunkIndex: entry.chunk.ChunkIndex,
			StartAt: entry.chunk.StartAt, EndAt: entry.chunk.EndAt, Content: entry.chunk.Content,
			ContentRevision: entry.chunk.ContentRevision, KnowledgeTitle: asset.Title,
		}
		resolved, err := resolver.Resolve(ctx, asset, []*types.SearchResult{hit})
		if err != nil {
			if errors.Is(err, evidence.ErrAssetKnowledgeMismatch) {
				// 锚点指向的知识不是这份资料此刻的正文（重解析换过知识 ID）：
				// 同样交给复核逐项作答，不在两处各写一条守卫。
				out = append(out, rebuiltSource{Source: evidence.Source{ID: id}})
				continue
			}
			return nil, err
		}
		// 一条命中交回一条来源（resolveOne 逐命中作答），所以这里取首条即可。
		// 零条说明协作方坏了：留一条只带 ID 的来源让复核逐项作答，
		// 免得它既不进 Usable 也不进 Unusable——那会被读成「复核通过」。
		if len(resolved) == 0 {
			out = append(out, rebuiltSource{Source: evidence.Source{ID: id}})
			continue
		}
		out = append(out, rebuiltSource{Source: resolved[0], DisplayTitle: asset.Title})
	}
	return out, nil
}

func (p *sourceRevalidator) chunks(ctx context.Context, ids []string) (map[string]types.Chunk, error) {
	var rows []types.Chunk
	if err := p.db.WithContext(ctx).Where("id IN ?", ids).Find(&rows).Error; err != nil {
		return nil, err
	}
	found := make(map[string]types.Chunk, len(rows))
	for _, row := range rows {
		found[row.ID] = row
	}
	return found, nil
}

// normalizeSourceIDs 与网关/复核的归一政策一致（trim、丢空、保序、首次为准）：
// 政策若各写一套，同一个 ID 会在重建里是一种、在复核里是另一种。
func normalizeSourceIDs(ids []string) []string {
	out := make([]string, 0, len(ids))
	seen := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

// assetVersionsOf 收拢复核放行的那批来源所引用的资料版本，按资料去重后排序——
// 顺序稳定，确认记录才可比对（生成侧的 assetVersions 同样按 AssetID 排序）。
//
// 能走到这里说明每条引用的 AssetRevision 与资料此刻的版本一致：复核的 recheck
// 正是拿这条等式当版本判据（policy.go:176），版本前进的一律已经落进 Unusable。
func assetVersionsOf(sources []evidence.Source) []candidateadoption.AssetVersion {
	seen := make(map[string]struct{}, len(sources))
	out := make([]candidateadoption.AssetVersion, 0, len(sources))
	for _, source := range sources {
		if _, ok := seen[source.AssetID]; ok {
			continue
		}
		seen[source.AssetID] = struct{}{}
		out = append(out, candidateadoption.AssetVersion{AssetID: source.AssetID, AssetRevision: source.AssetRevision})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].AssetID < out[j].AssetID })
	return out
}
