package workspace

import (
	"context"
	"errors"
	"sort"
	"strconv"
	"strings"

	"github.com/Tencent/WeKnora/internal/evidence"
	"github.com/Tencent/WeKnora/internal/lingdoc/candidateadoption"
	"github.com/Tencent/WeKnora/internal/lingdoc/sourcepolicy"
	workspacecore "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
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
func (h *SourceRuntime) CandidateAdoptionSourcePolicy() candidateadoption.SourcePolicy {
	if h == nil || h.db == nil || h.bindings == nil || h.gateway == nil {
		return nil
	}
	return &candidateAdoptionSourcePolicy{reviewer: h.sourceRevalidator(), service: h.service}
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
func (h *SourceRuntime) WorkspaceSourcePolicy() SourcePolicy {
	return workspaceSourcePolicy{handler: h}
}

type workspaceSourcePolicy struct{ handler *SourceRuntime }

// 装配处把 SourcePolicy 交给核心域（core_alias.go 的 NewService）；读侧的逐项状态
// 通过同一个 Reviewer 获取，不依赖某个具体适配器的类型形状。
var _ SourcePolicy = workspaceSourcePolicy{}
var _ sourcepolicy.Reviewer = workspaceSourcePolicy{}

// Review exposes the shared per-source verdicts to delivery adapters. The
// adapter owns only context and representation conversion; source access,
// revocation and coordinate checks remain in sourceRevalidator.
func (p workspaceSourcePolicy) Review(ctx context.Context, projectID string, actor evidence.Actor, sourceIDs []string) ([]sourcepolicy.Verdict, error) {
	if len(sourceIDs) == 0 {
		return nil, nil
	}
	reviewer := p.reviewer()
	if reviewer == nil {
		return nil, candidateadoption.ErrDependencyUnavailable
	}
	return reviewer.Review(ctx, projectID, actor, sourceIDs)
}

func (p workspaceSourcePolicy) Validate(ctx context.Context, projectID, actorID string, sourceIDs []string) error {
	// 复用产出侧那一个 nil 守卫，不在这里再抄一遍判据：依赖齐备与否只该有一处定义。
	if len(sourceIDs) == 0 {
		return nil
	}
	reviewer := p.reviewer()
	if reviewer == nil {
		return candidateadoption.ErrDependencyUnavailable
	}
	checked, actor, ok := recheckContext(ctx, actorID)
	if !ok || strings.TrimSpace(projectID) == "" {
		return candidateadoption.ErrSourceAccessDenied
	}
	verdicts, err := reviewer.Review(checked, projectID, actor, sourceIDs)
	if err != nil {
		return err
	}
	return sourceReviewError(verdicts)
}

func (p workspaceSourcePolicy) CitationStatuses(ctx context.Context, projectID, actorID string, sourceIDs []string) ([]workspacecore.CitationStatus, error) {
	reviewer := p.reviewer()
	if reviewer == nil {
		return nil, candidateadoption.ErrDependencyUnavailable
	}
	checked, actor, ok := recheckContext(ctx, actorID)
	if !ok || strings.TrimSpace(projectID) == "" {
		return nil, candidateadoption.ErrSourceAccessDenied
	}
	verdicts, err := reviewer.Review(checked, projectID, actor, sourceIDs)
	if err != nil {
		return nil, err
	}
	statuses := make([]workspacecore.CitationStatus, 0, len(verdicts))
	for _, verdict := range verdicts {
		status := workspacecore.CitationStatus{SourceID: verdict.SourceID, Status: "unavailable", Detail: "来源当前不可用，请重新选择资料片段"}
		switch {
		case verdict.Usable:
			status.Status = "available"
			status.Detail = "来源当前可用"
		case verdict.AssetDeny == "" && verdict.Status == evidence.SourceStale:
			status.Status = "needs_review"
			status.Detail = "来源定位或资料版本已变化，请重新核对"
		}
		statuses = append(statuses, status)
	}
	return statuses, nil
}

// reviewer resolves the current source-review implementation at call time.
// The handler may replace its gateway after construction (tests and runtime
// reconfiguration both do this), so the adapter must not retain a stale
// implementation snapshot.
func (p workspaceSourcePolicy) reviewer() sourcepolicy.Reviewer {
	if p.handler == nil || p.handler.db == nil || p.handler.bindings == nil || p.handler.gateway == nil {
		return nil
	}
	return p.handler.sourceRevalidator()
}

func sourceReviewError(verdicts []sourcepolicy.Verdict) error {
	stale, denied := false, false
	for _, verdict := range verdicts {
		if verdict.Usable {
			continue
		}
		if verdict.AssetDeny == "" {
			stale = true
			continue
		}
		denied = true
	}
	if stale {
		return candidateadoption.ErrStaleInput
	}
	if denied {
		return candidateadoption.ErrSourceAccessDenied
	}
	return nil
}

// reviewSourceIDs is the delivery-facing adapter for the shared reviewer. It
// maps the per-source verdicts into the existing all-or-nothing error contract;
// it does not recreate source authorization or currentness rules.
func reviewSourceIDs(ctx context.Context, projectID, actorID string, reviewer sourcepolicy.Reviewer, sourceIDs []string) error {
	if len(sourceIDs) == 0 {
		return nil
	}
	if reviewer == nil {
		return candidateadoption.ErrDependencyUnavailable
	}
	checked, actor, ok := recheckContext(ctx, actorID)
	if !ok || strings.TrimSpace(projectID) == "" {
		return candidateadoption.ErrSourceAccessDenied
	}
	verdicts, err := reviewer.Review(checked, projectID, actor, sourceIDs)
	if err != nil {
		return err
	}
	return sourceReviewError(verdicts)
}

// legacySourcePolicyReview is the explicit compatibility adapter for callers
// that have not yet been handed the shared Reviewer. It keeps the old
// all-or-nothing representation at the boundary; production paths use
// reviewSourceIDs above.
func legacySourcePolicyReview(ctx context.Context, projectID, actorID string, policy candidateadoption.SourcePolicy, sourceIDs []string) error {
	if policy == nil {
		return candidateadoption.ErrDependencyUnavailable
	}
	return policy.Validate(ctx, projectID, actorID, sourceIDs)
}

// sourceRevalidator 组装产出侧与复核侧共用的那台判定器。调用方负责确认依赖齐备：
// 它只被那几个带 nil 守卫的装配入口调用。
func (h *SourceRuntime) sourceRevalidator() sourcepolicy.Reviewer {
	return h.sourceReviewerForGateway(h.gateway)
}

// Model and delegated readers share source reconstruction while retaining their
// own asset-use policy. A different gateway must never widen the model scope.
func (h *SourceRuntime) sourceReviewerForGateway(gateway evidence.AssetGateway) sourcepolicy.Reviewer {
	origins := h.origins()
	return &sourceRevalidator{
		db: h.db, bindings: h.bindings, gateway: gateway, origins: origins,
		policy: evidence.NewSourcePolicy(gateway, origins, h.bindings),
	}
}

// candidateAdoptionSourcePolicy 同时满足 T11 的 SourcePolicy 与 T12 的
// ConfirmationSourcePolicy：两者问的是同一件事，只是 T12 还要把被引用资料的当前版本交出来
// 给确认记录钉住——确认之后资料再前进就该判失效，而不是静默沿用旧坐标。
type candidateAdoptionSourcePolicy struct {
	reviewer sourcepolicy.Reviewer
	service  ApplicationService
}

// 装配处靠类型断言才能把复核接给确认服务（candidate_adoption_http.go:26），而断言失败是
// 静默的——把「静默少一层校验」变成「编译不过」。
var _ candidateadoption.ConfirmationSourcePolicy = (*candidateAdoptionSourcePolicy)(nil)

// Review exposes the shared verdict seam to candidate adoption. Validate and
// ValidateCurrent below remain compatibility adapters for callers that only
// need an all-or-nothing answer.
func (p *candidateAdoptionSourcePolicy) Review(ctx context.Context, projectID string, actor evidence.Actor, sourceIDs []string) ([]sourcepolicy.Verdict, error) {
	if p == nil || p.reviewer == nil {
		return nil, candidateadoption.ErrDependencyUnavailable
	}
	return p.reviewer.Review(ctx, projectID, actor, sourceIDs)
}

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
	if p == nil || p.reviewer == nil {
		return nil, candidateadoption.ErrDependencyUnavailable
	}
	verdicts, err := p.reviewer.Review(checked, projectID, actor, sourceIDs)
	if err != nil {
		return nil, err
	}
	usable := make([]evidence.Source, 0, len(verdicts))
	for _, verdict := range verdicts {
		if verdict.Usable {
			usable = append(usable, verdict.Source)
		}
	}
	// 坐标失效优先于资料层拒绝，与生成侧 generationSourceValidator 同一条读法：
	// 整批里只要有一条「资料还在、引用失效」，可修的那一类就是它。
	if err := sourceReviewError(verdicts); err != nil {
		return nil, err
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
	caller := types.CallerFromContext(ctx)
	if caller.TenantID != tenantID || caller.UserID != actorID {
		return nil, evidence.Actor{}, false
	}
	if contextUser, exists := types.UserIDFromContext(ctx); exists && contextUser != actorID {
		return nil, evidence.Actor{}, false
	}
	// Preserve Caller, Principal and API key scope. Execution scope is not a grant.
	return types.WithExecutionTenant(ctx, tenantID), actor, true
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

var _ sourcepolicy.Reviewer = (*sourceRevalidator)(nil)

// Review 逐条给出结论，顺序与请求一致（去重、去空白后）。
func (p *sourceRevalidator) Review(ctx context.Context, projectID string, actor evidence.Actor, sourceIDs []string) ([]sourcepolicy.Verdict, error) {
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

	verdicts := make([]sourcepolicy.Verdict, 0, len(rebuilt))
	for _, entry := range rebuilt {
		verdict := sourcepolicy.Verdict{
			SourceID:     entry.Source.ID,
			Source:       entry.Source,
			Chunk:        entry.Chunk,
			DisplayTitle: entry.DisplayTitle,
		}
		if source, ok := usable[entry.Source.ID]; ok {
			verdict.Source, verdict.Usable = source, true
			verdicts = append(verdicts, verdict)
			continue
		}
		reason, ok := unusable[verdict.SourceID]
		if !ok {
			// 每一项请求都必须得到「可用」或「不可用」之一（ValidateResult 的不变量）。
			// 两个都没有说明协作方坏了：如实报错，别把没被复核的当成复核通过。
			return nil, candidateadoption.ErrDependencyUnavailable
		}
		verdict.AssetDeny, verdict.Detail = reason.AssetDeny, reason.Detail
		verdict.Status = reason.Status
		verdicts = append(verdicts, verdict)
	}
	return verdicts, nil
}

// sourceRead 是一次「把引用块读成一条已复核来源」的结果。
//
// 只有 readSource 能构造它，而语境展开收它作入参——于是「没走完授权与复核
// 就先查邻居」在类型上就写不出来，不必靠一句注释来维持。
type sourceRead struct {
	// Chunk 是引用块那一行：坐标、ChunkIndex、ChunkType 都在它上面，窗口按它取。
	Chunk types.Chunk
	// Source 是复核后的来源。坐标层失效时它照样带着 locator 与 asset_revision，
	// 只是 Status 换成了失效结论——界面要说得清失效的是哪一条引用。
	Source evidence.Source
}

// readSource 是「项目读权限 → 引用块 → 资料绑定 → 授权 → 复原坐标 → 复核」
// 这条判定链的唯一实现，getSource 与 getSourceContext 都走它。
//
// 抽出来的理由不是省行数：两个端点各写一条时，某天有人只改了其中一条（比如给
// 授权加一道判据），同一个 sourceId 就会在两个 URL 上得到不同的可用性结论，
// 而两边都「有实现」、都说得通——最难查的一类错。
//
// 返回的全是既有 sentinel（ErrNotFound / ErrSourceUnavailable / 上游 error），
// 不含任何 HTTP 概念：两个端点走同一个 sendError，状态码不会分叉。
func (h *SourceRuntime) readSource(ctx context.Context, actor Actor, projectID, sourceID string) (sourceRead, error) {
	if err := h.service.Authorize(ctx, actor, projectID, "read"); err != nil {
		return sourceRead{}, err
	}
	revalidator := h.sourceRevalidator()
	verdicts, err := revalidator.Review(ctx, projectID,
		evidence.Actor{UserID: actor.UserID, TenantID: strconv.FormatUint(actor.TenantID, 10)},
		[]string{sourceID})
	if err != nil {
		return sourceRead{}, err
	}
	// revalidate 逐项作答，请求几条就回几条；一条都没有说明协作方坏了。
	if len(verdicts) == 0 {
		return sourceRead{}, ErrNotFound
	}
	verdict := verdicts[0]
	if verdict.Chunk.ID == "" {
		// 分块行不存在：这条引用指向的块没了。与「资料不可用」分开答——
		// 在用户眼里那是两件事：一个链接坏了，一个权限没了。
		return sourceRead{}, ErrNotFound
	}
	switch {
	case verdict.Usable:
		return sourceRead{Chunk: verdict.Chunk, Source: verdict.Source}, nil
	case verdict.AssetDeny != "":
		// 资料层拒绝（不存在 / 未就绪 / 未授权 / 不属于本项目）：四种原因不在措辞上
		// 区分，它们该触发的动作是同一个——拒绝。
		return sourceRead{}, ErrSourceUnavailable
	default:
		// 资料层放行、卡在坐标层：这是 200 的合法结论，不是错误。
		source := verdict.Source
		source.Status = verdict.Status
		return sourceRead{Chunk: verdict.Chunk, Source: source}, nil
	}
}

// origins 现取原文读取器。装配只有这一处定义——将来换实现不会漏掉某条读路径。
func (h *SourceRuntime) origins() evidence.OriginReader {
	return evidence.NewOriginReader(dbKnowledgeReader{db: h.db})
}

// rebuiltSource 是一条引用在底座上重建出来的样子，连同它所属资料此刻的标题。
type rebuiltSource struct {
	Source evidence.Source
	// Chunk 是这条引用在底座上的那一行。**分块行不存在时为零值**——调用方据此答
	// 「查无此块」（404），而不是把它折叠成复核侧那条 not_found（那是资料层的措辞，
	// 答的是 403）。两者在用户眼里是不同的事：一个是链接坏了，一个是权限没了。
	Chunk        types.Chunk
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
				out = append(out, rebuiltSource{Source: evidence.Source{ID: id}, Chunk: entry.chunk})
				continue
			}
			return nil, err
		}
		// 一条命中交回一条来源（resolveOne 逐命中作答），所以这里取首条即可。
		// 零条说明协作方坏了：留一条只带 ID 的来源让复核逐项作答，
		// 免得它既不进 Usable 也不进 Unusable——那会被读成「复核通过」。
		if len(resolved) == 0 {
			out = append(out, rebuiltSource{Source: evidence.Source{ID: id}, Chunk: entry.chunk})
			continue
		}
		out = append(out, rebuiltSource{Source: resolved[0], Chunk: entry.chunk, DisplayTitle: asset.Title})
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

var _ candidateadoption.CandidateEvidencePolicy = (*candidateAdoptionSourcePolicy)(nil)

type candidateEvidenceTransactionSelector interface {
	SelectGenerationEvidenceInTransaction(context.Context, workspacecore.Transaction, workspacecore.Actor, string, string, []workspacecore.G2VersionRef, bool, []string) (workspacecore.G2EvidenceSelection, error)
}

func candidateEvidenceActor(ctx context.Context, actorID string) (context.Context, workspacecore.Actor, error) {
	checked, _, ok := recheckContext(ctx, actorID)
	if !ok {
		return nil, workspacecore.Actor{}, candidateadoption.ErrSourceAccessDenied
	}
	caller := types.CallerFromContext(checked)
	return checked, workspacecore.Actor{TenantID: caller.TenantID, UserID: caller.UserID, Role: caller.Role}, nil
}

func (p *candidateAdoptionSourcePolicy) ValidateCandidateEvidence(ctx context.Context, projectID, chapterID, actorID string, refs []workspacecore.G2VersionRef, sourceIDs []string) (workspacecore.G2EvidenceSelection, error) {
	checked, actor, err := candidateEvidenceActor(ctx, actorID)
	if err != nil {
		return workspacecore.G2EvidenceSelection{}, err
	}
	port, ok := p.service.(g2EvidenceSelector)
	if !ok {
		return workspacecore.G2EvidenceSelection{}, candidateadoption.ErrDependencyUnavailable
	}
	selected, err := port.SelectGenerationEvidence(checked, actor, projectID, chapterID, refs, true)
	if err != nil {
		return selected, errors.Join(candidateadoption.ErrSourceAccessDenied, err)
	}
	if err = p.Validate(checked, projectID, actorID, sourceIDs); err != nil {
		return selected, err
	}
	return selected, nil
}

func (p *candidateAdoptionSourcePolicy) ValidateCandidateEvidenceInTransaction(ctx context.Context, tx *gorm.DB, projectID, chapterID, actorID string, refs []workspacecore.G2VersionRef, sourceIDs []string) (workspacecore.G2EvidenceSelection, error) {
	checked, actor, err := candidateEvidenceActor(ctx, actorID)
	if err != nil {
		return workspacecore.G2EvidenceSelection{}, err
	}
	port, ok := p.service.(candidateEvidenceTransactionSelector)
	if !ok || tx == nil {
		return workspacecore.G2EvidenceSelection{}, candidateadoption.ErrDependencyUnavailable
	}
	selected, err := port.SelectGenerationEvidenceInTransaction(checked, workspacecore.NewTransactionAdapter(tx), actor, projectID, chapterID, refs, true, sourceIDs)
	if err != nil {
		return selected, errors.Join(candidateadoption.ErrSourceAccessDenied, err)
	}
	return selected, nil
}

type candidateEvidenceProofPreparer interface {
	PrepareCandidateGenerationEvidence(context.Context, workspacecore.Actor, string, string, []workspacecore.G2VersionRef, []string) (context.Context, workspacecore.G2EvidenceSelection, error)
}

func (p *candidateAdoptionSourcePolicy) PrepareCandidateEvidence(ctx context.Context, projectID, chapterID, actorID string, refs []workspacecore.G2VersionRef, sourceIDs []string) (context.Context, workspacecore.G2EvidenceSelection, error) {
	checked, actor, err := candidateEvidenceActor(ctx, actorID)
	if err != nil {
		return ctx, workspacecore.G2EvidenceSelection{}, err
	}
	port, ok := p.service.(candidateEvidenceProofPreparer)
	if !ok {
		return ctx, workspacecore.G2EvidenceSelection{}, candidateadoption.ErrDependencyUnavailable
	}
	prepared, selected, err := port.PrepareCandidateGenerationEvidence(checked, actor, projectID, chapterID, refs, sourceIDs)
	if err != nil {
		return ctx, selected, errors.Join(candidateadoption.ErrSourceAccessDenied, err)
	}
	return prepared, selected, nil
}
