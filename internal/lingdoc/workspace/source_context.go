package workspace

import (
	"context"
	"unicode/utf8"

	"github.com/Tencent/WeKnora/internal/evidence"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// contextWindow 是引用处两侧各展开的段数。写死而不是做成请求参数：本轮界面只用
// 一种窗口，而暴露参数就要在契约里定义它的取值域与上限——多出来的自由度没有消费者。
const contextWindow = 1

// maxContextSegmentRunes 是单段正文的上限。超了就只给位置、不给正文：语境是给人
// 看的，不该变成一次把大块文本灌进响应体的搬运。降档而不报错，与 evidence/origin.go
// 的 maxOriginBytes 同一条政策。
const maxContextSegmentRunes = 4096

// expandableChunkType 回答「这一块能不能作语境窗口的中心」。
//
// text 与 parent_text 各有独立且稠密的 chunk_index 空间（子块另起序号），两者混进
// 同一个窗口，同一段文字会出现两次——父块一次、子块一次。所以窗口只在引用块
// **自己那一族**里扫，不做跨族拼接。
//
// 派生块（summary / image_ocr / image_caption / entity / relationship / wiki_page…）
// 没有指向原文的坐标，一律不可展开。白名单而非黑名单：将来底座加了新的派生类型，
// 默认落在「不可展开」这一侧。
func expandableChunkType(t types.ChunkType) bool {
	return t == types.ChunkTypeText || t == types.ChunkTypeParentText
}

// expandSourceContext 把一条已复核的来源摊成「这道引用周围长什么样」。
//
// read 必须来自 readSource：窗口是在授权与复核**之后**才读的。这不是纪律问题——
// 走完那条链才拿得到 sourceRead，而邻居查询只收它作入参。
func (h *Handler) expandSourceContext(ctx context.Context, read sourceRead) (gin.H, error) {
	// 必须是空切片而不是 nil：契约里 segments 是 array，nil 会序列化成 null，
	// 界面 .map 直接炸。accessStatus 那里已经栽过一次，注释留在那儿。
	segments, available := []gin.H{}, false
	if expandableChunkType(read.Chunk.ChunkType) && read.Source.Status == evidence.SourceAvailable {
		var err error
		available, segments, err = h.contextSegments(ctx, read)
		if err != nil {
			return nil, err
		}
	}
	return gin.H{
		"source":            read.Source,
		"context_available": available,
		"window":            gin.H{"before": contextWindow, "after": contextWindow},
		"segments":          segments,
	}, nil
}

// contextSegments 取引用块两侧的邻居并逐段作答。
func (h *Handler) contextSegments(ctx context.Context, read sourceRead) (bool, []gin.H, error) {
	origin, haveOrigin, err := h.origins().OriginText(ctx, read.Chunk.KnowledgeID)
	if err != nil {
		// 底座故障如实报错。把它吞成「没有上下文」等于把一次事故说成「这段没有邻居」。
		return false, nil, err
	}
	before, after, err := h.contextNeighbours(ctx, read.Chunk, contextWindow)
	if err != nil {
		return false, nil, err
	}
	segments := make([]gin.H, 0, len(before)+len(after))
	// before 是倒序取回来的（为了拿到「最靠近引用块的那一段」），这里翻回文档顺序。
	for i := len(before) - 1; i >= 0; i-- {
		segments = append(segments, contextSegment(origin, haveOrigin, before[i], "before"))
	}
	for _, row := range after {
		segments = append(segments, contextSegment(origin, haveOrigin, row, "after"))
	}
	return true, segments, nil
}

// contextSegment 为一段邻居作答。
//
// verbatim 说的是「这段文字此刻逐字等于原文坐标上的那一段」。拿得到原文（强档）
// 才可能为 true；拿不到原文时它是 false，但**正文照样给**——段里放的是分块表的
// 文本，它就是这个系统里落库的那份正文，代价是它无法被原文证明。两种 false 在
// 界面上的措辞不同，不能混成一句「原文」。
//
// 强档下逐字对不上的（坐标漂移、该块被单独编辑过）**只给位置、不给正文**：那一段
// 确实不是原文，摆出来就是错的。它仍然留在 segments 里——丢掉它会让窗口看上去是
// 连续的，而 chunk_index 的号码本来就把这个空洞说清了。
func contextSegment(origin string, haveOrigin bool, row types.Chunk, relation string) gin.H {
	verbatim, text := false, row.Content
	switch {
	case !haveOrigin:
	case evidence.VerbatimAt(origin, row.StartAt, row.EndAt, row.Content):
		verbatim = true
	default:
		text = ""
	}
	if utf8.RuneCountInString(text) > maxContextSegmentRunes {
		verbatim, text = false, ""
	}
	return gin.H{
		"source_id":   row.ID,
		"chunk_index": row.ChunkIndex,
		"relation":    relation,
		"verbatim":    verbatim,
		"text":        text,
	}
}

// contextNeighbours 取引用块两侧各 window 段**同族**的邻居。
//
// 按索引位取（`<` / `>` 加 LIMIT），不是 chunk_index 的算术区间：那个区间把「第几段」
// 当成稠密的连续整数，而 chunk_index 有空隙（被删的块留下空洞、别的族也占着同一片
// 号码），算术区间会静默少给几段，让人以为是文档到头了。
//
// 只按 knowledge_id 收敛，不加租户：正文范围由 knowledge_id 唯一确定，而绑定与授权
// 已经在 readSource 里判过。软删由 gorm 的 deleted_at 兜住（模型带 gorm.DeletedAt）。
//
// 不看 is_enabled：停用是「不参与检索」，不是「不在原文里」。窗口跳过它，前后两段
// 看上去就挨着了——那是这个端点最不该给的一种错觉。
func (h *Handler) contextNeighbours(ctx context.Context, cited types.Chunk, window int) (before, after []types.Chunk, err error) {
	base := func() *gorm.DB {
		return h.db.WithContext(ctx).Model(&types.Chunk{}).
			Select("id", "chunk_index", "content", "start_at", "end_at").
			Where("knowledge_id = ? AND chunk_type = ?", cited.KnowledgeID, cited.ChunkType)
	}
	if err = base().Where("chunk_index < ?", cited.ChunkIndex).
		Order("chunk_index DESC, id DESC").Limit(window).Find(&before).Error; err != nil {
		return nil, nil, err
	}
	if err = base().Where("chunk_index > ?", cited.ChunkIndex).
		Order("chunk_index ASC, id ASC").Limit(window).Find(&after).Error; err != nil {
		return nil, nil, err
	}
	return before, after, nil
}
