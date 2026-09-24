package evidence

import (
	"context"
	"fmt"
)

// UnusableSource 说清一条来源此刻为什么不能再用。
//
// 结论分两层，各自作答、不互相顶替：
//   - AssetDeny 非空 = 卡在资料层（不存在 / 未就绪 / 未授权），此时 Status 留空——
//     资料都不能用了，坐标是否对得上已无意义；
//   - AssetDeny 为空、Status 非空 = 资料层放行，卡在坐标层。
type UnusableSource struct {
	SourceID string
	AssetID  string
	// Status 是坐标层的结论，取值复用 SourceStatus，不新造词汇。
	Status SourceStatus
	// AssetDeny 是资料层的拒绝原因，取值复用 DenyReason。
	AssetDeny DenyReason
	// Detail 是给人看的补充说明（版本从几到几、锚点为什么不算数、坐标为什么失效），
	// 机器别解析它——这里的取值是散文，不是枚举（区别见 DeniedAsset.Reason）。
	Detail string
}

// ValidateResult 一次复核的完整回答：请求了哪些、哪些还能用、哪些不能。
//
// 三段缺一不可，理由与 ResolveResult 一样（契约 §8）：只交 Usable，
// 调用方就会把「没被复核」当成「复核通过」。
type ValidateResult struct {
	Requested []string
	Usable    []Source
	Unusable  []UnusableSource
}

// RevisionReader 提供「某一版资料当时对应底座的哪一份知识」，供复核做归位校验。
//
// 为什么不能拿 `Asset.KnowledgeID` 代替：它是**绑定时**那一份（绑定的身份，
// 见 `ProjectAsset.KnowledgeID` 与 `findBinding`），重解析产生的新知识 ID 只记在
// 修订行上（`ProjectAssetRevision.KnowledgeID`）。判不定时返回 ok=false。
type RevisionReader interface {
	KnowledgeOfRevision(ctx context.Context, assetID string, revision int) (knowledgeID string, ok bool, err error)
}

// SourcePolicy 是契约 §2 的 SourcePolicy.Validate(projectID, actor, sourceRefs)。
//
// 它回答的不是「能不能重新找一遍」，而是「这批**已经产出**的引用此刻还能不能当证据」。
// 消费者（工作区的采纳、交付前的冻结）手上拿的就是已产出的来源，重新检索会得到
// 另一批引用，答的不是同一个问题。
//
// **必须在进程内调用**：定位复核要用 Anchor 上的 ContentRevision/ContentRewritten，
// 而那两个字段是 json:"-"（ADR-0001），跨进程传进来的 sourceRefs 已经丢了失效信息。
// 这正是 T03 记下的偏离（T03-检索与生成来源验证.md:82）与 T09 §5 的答复：
// 独立 Validate 是 Resolve 的**薄封装**而非替身，不能替代它做检索。
type SourcePolicy interface {
	Validate(ctx context.Context, projectID string, actor Actor, sources []Source) (*ValidateResult, error)
}

// NewSourcePolicy 组装来源复核：资料层复用 AssetGateway，坐标层复用 OriginReader，
// 归位校验复用 RevisionReader（实现都在同一处存储上：`Bindings`）。
// 三个依赖都是本领域已有的组件，复核因此不会与产出分叉出第二套判据。
func NewSourcePolicy(gateway AssetGateway, origins OriginReader, revisions RevisionReader) SourcePolicy {
	return &sourcePolicy{gateway: gateway, origins: origins, revisions: revisions}
}

type sourcePolicy struct {
	gateway   AssetGateway
	origins   OriginReader
	revisions RevisionReader
}

// Validate 按契约 §2 那句话的四个维度逐条复核：当前授权、引用存在性、定位、资料版本。
//
// 前两个维度交给 AssetGateway：「这份资料此刻还在不在本项目、就绪没有、还授不授权」
// 与绑定/允许集合是同一个问题，不另写一套。契约 §2 禁止的是「再调用已经包含来源检查的
// 总入口」——那是跨域循环；这里组合的是本领域自己的网关，调用方向是单向的。
//
// 第三个维度（定位）与第四个（版本）在这里判：归位——坐标得先落在这份资料
// **此刻**的正文上——然后版本前进了就不再采信旧坐标，版本没动才去比坐标本身。
func (p *sourcePolicy) Validate(
	ctx context.Context, projectID string, actor Actor, sources []Source,
) (*ValidateResult, error) {
	byID, requested := dedupeSources(sources)
	if len(requested) == 0 {
		// 空批次是「没有引用」，不是「全部引用」：不触达存储与底座。
		return &ValidateResult{Requested: []string{}}, nil
	}

	// 涉及的资料按来源顺序去重后一次问网关，别按来源逐条问：
	// 同一条资料的多个引用只该判一次权。
	assetIDs := make([]string, 0, len(requested))
	seen := make(map[string]struct{}, len(requested))
	for _, id := range requested {
		assetID := byID[id].AssetID
		if _, ok := seen[assetID]; ok {
			continue
		}
		seen[assetID] = struct{}{}
		assetIDs = append(assetIDs, assetID)
	}

	resolved, err := p.gateway.ResolveAllowed(ctx, projectID, actor, assetIDs)
	if err != nil {
		return nil, err
	}
	allowed := make(map[string]Asset, len(resolved.Allowed))
	for _, asset := range resolved.Allowed {
		allowed[asset.ID] = asset
	}
	denied := make(map[string]DenyReason, len(resolved.Denied))
	for _, d := range resolved.Denied {
		denied[d.AssetID] = d.Reason
	}

	out := &ValidateResult{Requested: requested}
	for _, id := range requested {
		src := byID[id]
		asset, ok := allowed[src.AssetID]
		if !ok {
			reason, reported := denied[src.AssetID]
			if !reported && src.AssetID == "" {
				// 不指向任何资料的来源：网关那边空 ID 会被丢成「什么都没请求」，
				// 于是它既不在允许集合也不在拒绝集合。这不是协作方坏了，是这条
				// 来源本身不成话——按 §8 逐项作答，不去污染整批。
				out.Unusable = append(out.Unusable, UnusableSource{
					SourceID: id, AssetID: "", AssetDeny: DenyNotFound,
					Detail: "这条来源没有指向任何资料",
				})
				continue
			}
			if !reported {
				// 网关对每个请求的 ID 都必须给出「允许」或「拒绝」（ResolveResult 的不变量）。
				// 两个都没有说明协作方坏了：如实报错，别凭空造一个原因——
				// 编出来的原因会让调用方按错误的理由丢弃引用。
				return nil, fmt.Errorf("来源复核：资料 %s 既不在允许集合也不在拒绝集合", src.AssetID)
			}
			out.Unusable = append(out.Unusable, UnusableSource{
				SourceID: id, AssetID: src.AssetID, AssetDeny: reason,
				Detail: "资料此刻不可用（" + string(reason) + "）",
			})
			continue
		}

		status, detail, err := p.recheck(ctx, asset, src)
		if err != nil {
			return nil, err
		}
		if status == SourceAvailable {
			src.Status = SourceAvailable
			out.Usable = append(out.Usable, src)
			continue
		}
		out.Unusable = append(out.Unusable, UnusableSource{
			SourceID: id, AssetID: src.AssetID, Status: status, Detail: detail,
		})
	}
	return out, nil
}

// recheck 复核资料层已经放行的那条来源：归位、版本与定位。
//
// 结论里的 stale 覆盖三种「坐标已不可信」：被标过重写/编辑、资料版本前进、
// 锚点不属于这份资料。三者的区别在 Detail 里，机器判断只看 AssetDeny 与 Status
// 两层——契约 §2 的 Source.status 只有这三个取值，复核不另造一套词，
// 否则消费者得为同一个概念写两套映射。
func (p *sourcePolicy) recheck(ctx context.Context, asset Asset, src Source) (SourceStatus, string, error) {
	// 失效标记先于内容判定，与 Resolve 同一条规矩（术语表 §2「默认拒绝」）：
	// 被标过重写或编辑过的引用，即便坐标此刻仍对得上，也不得采信。
	if src.Anchor.ContentRewritten || src.Anchor.ContentRevision != 0 {
		return SourceStale, "这条引用的坐标被标过重写或编辑，不再可信", nil
	}

	// 指纹变了版本才递增（§3.1），所以版本前进 = 所引正文被改过。
	// 与内容改写同理：不因为「坐标碰巧还对得上」就放行——
	// 那样复核会比产出更松，而产出时的旧版本引用恰恰是最容易悄悄失效的一类。
	if src.AssetRevision != asset.AssetRevision {
		return SourceStale, fmt.Sprintf("资料已从第 %d 版前进到第 %d 版，这条引用产生于旧版本",
			src.AssetRevision, asset.AssetRevision), nil
	}

	// 归位：坐标要回原文，而「哪一份知识是这份资料的正文」必须由资料侧说了算。
	// 不校验的话，一条锚点指向别的文档、坐标又恰好对得上的来源会被判可用——
	// 产出的 Resolve 有同义的守卫（source.go 的 ErrAssetKnowledgeMismatch），
	// 复核不重做它就等于自己拆掉了那道守卫。
	known, ok, err := p.revisions.KnowledgeOfRevision(ctx, asset.ID, asset.AssetRevision)
	if err != nil {
		return "", "", err
	}
	if !ok {
		// 修订行缺失（历史数据/基线没补上）：判不定时退回产出侧用的那条判据——
		// 资料行上的知识。它与 Resolve 的守卫逐字同源，不会造出产出侧认不下的结论。
		known = asset.KnowledgeID
	}
	if known != "" && src.Anchor.KnowledgeID != known {
		return SourceStale, fmt.Sprintf("锚点指向的知识 %s 不是这份资料第 %d 版的知识 %s",
			src.Anchor.KnowledgeID, asset.AssetRevision, known), nil
	}
	if present, known, err := originKnowledgePresent(ctx, p.origins, src.Anchor.KnowledgeID); err != nil {
		return "", "", err
	} else if known && !present {
		return SourceUnavailable, "锚点指向的知识已删除，不能作为弱档来源", nil
	}

	origin, ok, err := p.origins.OriginText(ctx, src.Anchor.KnowledgeID)
	if err != nil {
		return "", "", err
	}
	var status SourceStatus
	if ok {
		// 强档：逐字比对坐标指向的那一段，与产出时同一个判据。
		status, _ = quoteAt(origin, src.Anchor.StartAt, src.Anchor.EndAt, src.QuotedText)
	} else {
		// 弱档：原文仍取不回来，回到产出时的同一条退路——坐标自洽即可用。
		status, _ = selfConsistentAt(src.Anchor.StartAt, src.Anchor.EndAt, src.QuotedText)
	}

	switch status {
	case SourceAvailable:
		return status, "", nil
	case SourceStale:
		return status, "坐标取回的正文与这条引用记录的不再一致", nil
	default:
		return status, "坐标越界或长度不自洽，取不回正文", nil
	}
}

// dedupeSources 收敛输入：把来源挂到它们归一后的 ID 上，并给出「请求了哪些」。
//
// 归一与去重的**政策不在本函数里**：它复用网关的 normalizeKey/normalizeIDs
// （trim、丢空、保序、首次为准）。政策若各写一套，同一个 ID 会在复核里是一种、
// 在网关里是另一种，而两边的报错都说得通。AssetID 同样归一：否则网关按修剪后的
// ID 回答了，这里却按原样去查，会得出「网关一条都没答」的假故障。
//
// 空 ID 的来源不进 Requested：没有身份就无从作答，它算不上一条引用。
// AssetID 为空则**照答**（判 not_found 并说明）——那是能逐项说清的事，不该吞掉。
func dedupeSources(sources []Source) (map[string]Source, []string) {
	first := make(map[string]Source, len(sources))
	for _, src := range sources {
		src.ID = normalizeKey(src.ID)
		src.AssetID = normalizeKey(src.AssetID)
		if src.ID == "" {
			continue
		}
		if _, seen := first[src.ID]; !seen {
			first[src.ID] = src
		}
	}

	requested := normalizeIDs(sourceIDs(sources))
	byID := make(map[string]Source, len(requested))
	for _, id := range requested {
		// requested 的每一项都来自 first 的键：两者走同一个 normalizeKey，
		// 空与不空、同与不同都一致。
		byID[id] = first[id]
	}
	return byID, requested
}

// sourceIDs 列出这批来源的 ID（原样，归一交给 normalizeIDs）。
func sourceIDs(sources []Source) []string {
	ids := make([]string, 0, len(sources))
	for _, src := range sources {
		ids = append(ids, src.ID)
	}
	return ids
}
