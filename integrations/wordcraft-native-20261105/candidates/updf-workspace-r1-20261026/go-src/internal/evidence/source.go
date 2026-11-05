package evidence

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"unicode/utf8"

	"github.com/Tencent/WeKnora/internal/types"
)

// ErrAssetKnowledgeMismatch indicates that a search hit cannot be attributed
// to the asset supplied by the caller. Such a hit is rejected before reading
// any origin text so it can never be emitted as a source for the wrong asset.
var ErrAssetKnowledgeMismatch = errors.New("evidence: hit does not belong to asset")

// SourceStatus 说明一条来源此刻能不能被当作证据。
type SourceStatus string

const (
	// SourceAvailable 该来源能指回原文（强档逐字命中，或弱档坐标自洽且未被编辑）。
	SourceAvailable SourceStatus = "available"
	// SourceStale 该来源曾经可用，但坐标已不可信——不是失败，是结论。
	SourceStale SourceStatus = "stale"
	// SourceUnavailable 该来源此刻取不回来（坐标越界、长度不自洽）。
	SourceUnavailable SourceStatus = "unavailable"
)

// Anchor 回到原文的坐标。术语表 §2 要求来源锚点支持重定位与失效检测，
// 裸 chunk_id 两样都做不到，故区间与失效钩子必须一起带上。
type Anchor struct {
	KnowledgeID      string
	ChunkID          string
	ChunkIndex       int
	StartAt          int
	EndAt            int
	ContentRevision  int
	ContentRewritten bool
}

// Source 按契约 §2 的字段形状构造，另挂一个不序列化的仓库锚点供本层校验。
// json tag 与契约同名；契约里 Source 是 additionalProperties: false，
// 所以内部字段必须显式排除，否则序列化出去即违规。
type Source struct {
	ID             string       `json:"id"`
	ProjectID      string       `json:"project_id"`
	AssetID        string       `json:"asset_id"`
	AssetRevision  int          `json:"asset_revision"`
	Locator        string       `json:"locator"`
	QuotedText     string       `json:"quoted_text"`
	QuotedTextHash string       `json:"quoted_text_hash"`
	Status         SourceStatus `json:"status"`

	// Anchor 保留回到原文的坐标，供下游重定位与人工复核。
	// 标 json:"-"：它不在契约字段里，而 Source 是 additionalProperties: false；
	// 且 ContentRevision/ContentRewritten 本就是 json:"-"（见 ADR-0001），
	// 序列化出去会丢掉失效信息，反而误导。
	Anchor Anchor `json:"-"`
}

// OriginReader 提供一份资料的解析原文。纯文本资料直接返回内容；
// 需重解析的格式（PDF/DOCX）拿不到原文时返回 ok=false，调用方降为弱档。
type OriginReader interface {
	OriginText(ctx context.Context, knowledgeID string) (text string, ok bool, err error)
}

// SourceResolver 把检索命中解析成可定位的来源。
type SourceResolver interface {
	Resolve(ctx context.Context, asset Asset, hits []*types.SearchResult) ([]Source, error)
}

// NewSourceResolver 组装固定实现。原文读取由调用方注入。
func NewSourceResolver(origins OriginReader) SourceResolver {
	return &sourceResolver{origins: origins}
}

type sourceResolver struct {
	origins OriginReader
}

func (r *sourceResolver) Resolve(
	ctx context.Context, asset Asset, hits []*types.SearchResult,
) ([]Source, error) {
	out := make([]Source, 0, len(hits))
	for _, hit := range hits {
		if hit == nil {
			continue
		}
		src, err := r.resolveOne(ctx, asset, hit)
		if err != nil {
			return nil, err
		}
		out = append(out, src)
	}
	return out, nil
}

func (r *sourceResolver) resolveOne(ctx context.Context, asset Asset, hit *types.SearchResult) (Source, error) {
	if hit.KnowledgeID != asset.KnowledgeID {
		return Source{}, fmt.Errorf("%w: hit knowledge_id=%q asset knowledge_id=%q", ErrAssetKnowledgeMismatch, hit.KnowledgeID, asset.KnowledgeID)
	}
	src := Source{
		ID:            hit.ID,
		ProjectID:     asset.ProjectID,
		AssetID:       asset.ID,
		AssetRevision: asset.AssetRevision,
		Locator:       locatorFor(asset, hit),
		Anchor: Anchor{
			KnowledgeID:      hit.KnowledgeID,
			ChunkID:          hit.ID,
			ChunkIndex:       hit.ChunkIndex,
			StartAt:          hit.StartAt,
			EndAt:            hit.EndAt,
			ContentRevision:  hit.ContentRevision,
			ContentRewritten: hit.ContentRewritten,
		},
	}

	// 失效标记先于内容判定（术语表 §2「默认拒绝」）。被标过重写或编辑过的命中
	// 即便此刻切片仍对得上，也不得采信：坐标已不再描述当前正文。
	if hit.ContentRewritten || hit.ContentRevision != 0 {
		src.Status = SourceStale
		return src, nil
	}
	if present, known, err := originKnowledgePresent(ctx, r.origins, hit.KnowledgeID); err != nil {
		return Source{}, err
	} else if known && !present {
		src.Status = SourceUnavailable
		return src, nil
	}

	origin, ok, err := r.origins.OriginText(ctx, hit.KnowledgeID)
	if err != nil {
		return Source{}, err
	}

	if ok {
		src.Status, src.QuotedText = quoteFromOrigin(origin, hit)
	} else {
		src.Status, src.QuotedText = quoteBySelfConsistency(hit)
	}
	if src.Status == SourceAvailable {
		src.QuotedTextHash = hashText(src.QuotedText)
	}
	return src, nil
}

// originKnowledgePresent is optional to keep OriginReader useful for callers
// that only have a text reconstruction service. The production reader exposes
// presence, so a deleted knowledge row cannot fall through to weak checks.
func originKnowledgePresent(ctx context.Context, origins OriginReader, knowledgeID string) (present, known bool, err error) {
	reader, ok := origins.(OriginPresenceReader)
	if !ok {
		return true, false, nil
	}
	present, err = reader.OriginKnowledgePresent(ctx, knowledgeID)
	return present, true, err
}

// quoteFromOrigin 是强档判据：原文可取回时，逐字比对坐标指向的那一段。
// 判据本体在 quoteAt——产出与复核走同一个函数，档位的含义才只有一个。
func quoteFromOrigin(origin string, hit *types.SearchResult) (SourceStatus, string) {
	return quoteAt(origin, hit.StartAt, hit.EndAt, hit.Content)
}

// quoteAt 与 quoteFromOrigin 是同一个判据，只是坐标直接给：复核已产出的来源时
// 手上没有检索命中，坐标在 Anchor 上。两处**必须走同一个函数**——否则
// 「什么叫可用」在产出与复核两个时点会各自演化。
//
// 偏移单位是 rune 而非 byte（见 merge_overlap.go:98 的不变量
// runeLen(Content) == EndAt-StartAt）。按字节切会静默切错中文，此处是主要防线。
func quoteAt(origin string, startAt, endAt int, content string) (SourceStatus, string) {
	runes := []rune(origin)
	if startAt < 0 || endAt > len(runes) || endAt <= startAt {
		return SourceUnavailable, ""
	}
	if got := string(runes[startAt:endAt]); got != content {
		// 坐标取回的不是这段内容：坐标漂移，或该块已被下游改写。
		return SourceStale, ""
	}
	return SourceAvailable, content
}

// VerbatimAt 回答「这串文本此刻是不是原文坐标 [startAt,endAt) 上的那一段」。
//
// 判据与 quoteAt 是同一个函数：产出（Resolve）、复核（Validate）、语境展开
// 三处必须共用它，否则「什么叫逐字原文」会各自演化（见 quoteAt 的注释）。
// 只回 bool——调用方要的是「能不能把这段当原文摆出去」，档位细节由 Source.status 说。
func VerbatimAt(origin string, startAt, endAt int, content string) bool {
	status, _ := quoteAt(origin, startAt, endAt, content)
	return status == SourceAvailable
}

// quoteBySelfConsistency 是弱档判据：拿不到原文时唯一能诚实声明的一档。
//
// 判据与 chat_pipeline 的 chunkTrusted（merge_overlap.go:94-99）同构，
// 但就地实现而非调用——chunkTrusted 未导出，且 T03 不改动该包。
// 两处若分叉，以 chunkTrusted 为准（它才是生产路径真正使用的那个）。
//
// EndAt > StartAt 必须单独判：空内容配零长区间会满足长度不变量的退化形式
// （runeLen("") == 0 == 0-0），只看长度就会把它当可用证据放过去。
func quoteBySelfConsistency(hit *types.SearchResult) (SourceStatus, string) {
	return selfConsistentAt(hit.StartAt, hit.EndAt, hit.Content)
}

// selfConsistentAt 与 quoteBySelfConsistency 同一判据，坐标直接给（同上，复核要用）。
func selfConsistentAt(startAt, endAt int, content string) (SourceStatus, string) {
	if endAt <= startAt || utf8.RuneCountInString(content) != endAt-startAt {
		return SourceUnavailable, ""
	}
	return SourceAvailable, content
}

func hashText(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

// locatorFor 生成给人看的定位串。契约里 locator 是自由字符串（样例 "测试段落 1"），
// 机器可校验的判据在 anchor 上，这里只负责可读。
func locatorFor(asset Asset, hit *types.SearchResult) string {
	title := asset.Title
	if title == "" {
		title = hit.KnowledgeID
	}
	return fmt.Sprintf("%s · 第 %d 段", title, hit.ChunkIndex)
}
