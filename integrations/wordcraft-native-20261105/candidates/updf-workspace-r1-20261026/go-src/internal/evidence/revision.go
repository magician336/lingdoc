package evidence

import (
	"fmt"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
)

// FingerprintNoContentSignal 是「一个内容信号都拿不到」时的固定哨兵值。
//
// 它必须是常量而不是每次现算的哈希：否则每次观测都会判成「变了」，
// asset_revision 会自己往上跑，消费者被迫反复重选资料。也不能用空串——
// 空串在存储里表示「还没观测过」，会把首次观测误判成一次内容变化。
const FingerprintNoContentSignal = "no_content_signal"

// KnowledgeSignal 是底座对一份资料当前状态的观测结果。
//
// 底座（`types.Knowledge`）没有修订号字段，只有这几个可观测信号，
// 所以版本只能从这里推：契约 §10 把「资料版本的可信取得方式」点给了 T09，
// 这就是那个决定。信号来源与限制见 T09-项目资料与来源.md §3.1。
type KnowledgeSignal struct {
	KnowledgeID string
	// ParseStatus 是底座的解析状态，只影响 processing_state，不参与内容指纹。
	ParseStatus string
	// FileHash 是内容指纹，最强信号；外链/手工资料上可能为空。
	FileHash string
	// FileSize 为 0 表示不可得（空文件与不可得在此不可区分，取更保守的一侧）。
	FileSize int64
	// ProcessedAt 是那次解析的完成时刻；重解析会变，是「解析定位改变」的直接证据。
	ProcessedAt time.Time
}

// Fingerprint 是一次观测的可比较摘要，并带上它是**由哪些信号构成**的。
//
// 只给哈希不给依据的话，人工复核时看不出这次指纹是不是在「退化模式」下算的。
type Fingerprint struct {
	Digest string
	Basis  string
}

// FingerprintOf 计算内容/解析定位指纹。
//
// 只吃「内容或解析定位」类信号（契约 §4）。**刻意不吃 ParseStatus，也不吃
// UpdatedAt**：前者翻转只是处理进度，后者在任何行更新（含状态翻转）时都会变，
// 把它们算进来会造出契约没要求的版本递增。
func FingerprintOf(sig KnowledgeSignal) Fingerprint {
	parts := make([]string, 0, 3)
	basis := make([]string, 0, 3)

	if sig.FileHash != "" {
		parts = append(parts, "file_hash="+sig.FileHash)
		basis = append(basis, "file_hash")
	}
	if sig.FileSize > 0 {
		parts = append(parts, fmt.Sprintf("file_size=%d", sig.FileSize))
		basis = append(basis, "file_size")
	}
	if !sig.ProcessedAt.IsZero() {
		parts = append(parts, fmt.Sprintf("processed_at=%d", sig.ProcessedAt.UTC().UnixNano()))
		basis = append(basis, "processed_at")
	}

	if len(parts) == 0 {
		return Fingerprint{
			Digest: FingerprintNoContentSignal,
			Basis:  "无可用内容信号（FileHash/FileSize/ProcessedAt 都不可得）：内容变化检测不到",
		}
	}
	// 版本前缀 v1：将来改变信号集合时，旧指纹不会与新指纹相撞。
	return Fingerprint{
		Digest: hashText("v1|" + strings.Join(parts, "|")),
		Basis:  strings.Join(basis, "+"),
	}
}

// ProcessingStateOf 把底座的解析状态映射成契约的 processing_state。
//
// **只有 completed 是 ready**：不认识的状态一律不当就绪（术语表 §2「默认拒绝」）。
// 契约枚举里没有 cancelled，而取消后既没有可用解析产物、也不会自行恢复，
// 故并入 failed（提示需要重新解析），而不是 pending（会暗示还在排队）。
func ProcessingStateOf(parseStatus string) AssetState {
	switch parseStatus {
	case types.ParseStatusCompleted:
		return AssetStateReady
	case types.ParseStatusProcessing, types.ParseStatusFinalizing:
		return AssetStateProcessing
	case types.ParseStatusFailed, types.ParseStatusCancelled:
		return AssetStateFailed
	case types.ParseStatusDeleting:
		return AssetStateReplaced
	case types.ParseStatusPending, "unprocessed", "":
		return AssetStatePending
	default:
		return AssetStatePending
	}
}

// BindingState 是绑定记录里参与版本判据的那部分。
// Fingerprint 为空串表示「还没观测过」。
//
// 刻意不含 processing_state：处理状态是这次观测信号的纯函数，旧状态不参与判据。
// 带上它只会多一个只写不读的字段。
type BindingState struct {
	Revision    int64
	Fingerprint string
}

// ObserveResult 说清这次观测得到什么、是否让版本前进，以及为什么。
type ObserveResult struct {
	Next BindingState
	// State 是这次观测得到的处理状态（由信号决定，与是否递增版本无关）。
	State AssetState
	// Fingerprint 是本次算出的指纹（含依据），供落库时记 metadata，
	// 免得调用方为了写依据再算一遍。
	Fingerprint Fingerprint
	Changed     bool
	Reason      string
}

// Observe 是「这次观测要不要让 asset_revision 递增」的唯一判据入口。
//
// 递增条件是**内容/解析定位指纹变化**——契约 §4 的原话。首次观测只建立基线，
// 不递增：还没有任何东西基于旧版本产出，递增只会平白作废消费者的工作。
func Observe(prev BindingState, sig KnowledgeSignal) ObserveResult {
	fp := FingerprintOf(sig)

	next := BindingState{
		Revision:    prev.Revision,
		Fingerprint: fp.Digest,
	}
	if next.Revision < 1 {
		next.Revision = 1
	}
	state := ProcessingStateOf(sig.ParseStatus)

	switch {
	case prev.Fingerprint == "":
		// 建立基线。状态照常更新，版本不动。
		return ObserveResult{Next: next, State: state, Fingerprint: fp}
	case prev.Fingerprint == fp.Digest:
		return ObserveResult{Next: next, State: state, Fingerprint: fp}
	default:
		next.Revision = prev.Revision + 1
		return ObserveResult{
			Next:        next,
			State:       state,
			Fingerprint: fp,
			Changed:     true,
			Reason: fmt.Sprintf("内容或解析定位变化：指纹 %s → %s（依据 %s）",
				shortDigest(prev.Fingerprint), shortDigest(fp.Digest), fp.Basis),
		}
	}
}

// shortDigest 让原因行可读；完整指纹在库里。
func shortDigest(digest string) string {
	const keep = 12
	if len(digest) <= keep {
		return digest
	}
	return digest[:keep]
}
