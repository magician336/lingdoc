package evidence

import (
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
)

var (
	baseTime = time.Date(2026, 9, 23, 10, 0, 0, 0, time.UTC)
	nextTime = baseTime.Add(2 * time.Hour)
)

// signal 造一份"已解析完成的资料"的观测值，再按需改字段。
func signal(mutate func(*KnowledgeSignal)) KnowledgeSignal {
	sig := KnowledgeSignal{
		KnowledgeID: "k-1",
		ParseStatus: types.ParseStatusCompleted,
		FileHash:    "hash-v1",
		FileSize:    4096,
		ProcessedAt: baseTime,
	}
	if mutate != nil {
		mutate(&sig)
	}
	return sig
}

// 内容指纹只由「内容或解析定位」类信号构成。状态翻转（processing→completed）
// 改的是 processing_state，不该动 asset_revision——契约 §4 说的是
// 「内容或解析定位改变时变化」，把状态也算进去会造出契约没要求的版本递增。
func TestFingerprintIgnoresParseStatusFlip(t *testing.T) {
	completed := FingerprintOf(signal(nil))
	processing := FingerprintOf(signal(func(s *KnowledgeSignal) {
		s.ParseStatus = types.ParseStatusProcessing
	}))
	if completed.Digest != processing.Digest {
		t.Fatalf("状态翻转改了内容指纹：%q → %q", completed.Digest, processing.Digest)
	}
}

func TestFingerprintChangesOnContentAndReparse(t *testing.T) {
	base := FingerprintOf(signal(nil))

	t.Run("内容变化", func(t *testing.T) {
		got := FingerprintOf(signal(func(s *KnowledgeSignal) { s.FileHash = "hash-v2" }))
		if got.Digest == base.Digest {
			t.Fatal("FileHash 变了但指纹没变")
		}
	})
	t.Run("重解析", func(t *testing.T) {
		got := FingerprintOf(signal(func(s *KnowledgeSignal) { s.ProcessedAt = nextTime }))
		if got.Digest == base.Digest {
			t.Fatal("ProcessedAt 变了但指纹没变")
		}
	})
	t.Run("同输入同输出", func(t *testing.T) {
		if again := FingerprintOf(signal(nil)); again.Digest != base.Digest {
			t.Fatal("同样的观测得出了不同的指纹")
		}
	})
}

// 信号缺失时必须退化并**说明依据**，不能悄悄用一个看不出成分的哈希。
func TestFingerprintRecordsItsBasis(t *testing.T) {
	full := FingerprintOf(signal(nil))
	for _, want := range []string{"file_hash", "file_size", "processed_at"} {
		if !strings.Contains(full.Basis, want) {
			t.Errorf("全量信号的依据里缺 %q：%s", want, full.Basis)
		}
	}

	degraded := FingerprintOf(signal(func(s *KnowledgeSignal) {
		s.FileHash = ""
		s.ProcessedAt = time.Time{}
	}))
	if strings.Contains(degraded.Basis, "file_hash") || strings.Contains(degraded.Basis, "processed_at") {
		t.Errorf("退化的依据里不该出现不可得的信号：%s", degraded.Basis)
	}
	if !strings.Contains(degraded.Basis, "file_size") {
		t.Errorf("退化后仅剩的可用信号没进依据：%s", degraded.Basis)
	}
}

// 一个内容信号都拿不到时，指纹必须落在**固定哨兵值**上：既不能每次算出新值
// （版本号会自己往上跑，消费者被迫反复重选资料），也不能是空串——空串在存储里
// 表示"还没观测过"，首次观测就会被误判成一次内容变化。
func TestFingerprintWithoutContentSignalsIsStableSentinel(t *testing.T) {
	blind := FingerprintOf(signal(func(s *KnowledgeSignal) {
		s.FileHash = ""
		s.FileSize = 0
		s.ProcessedAt = time.Time{}
	}))
	if blind.Digest != FingerprintNoContentSignal {
		t.Errorf("无可用内容信号时 Digest = %q, want 哨兵 %q", blind.Digest, FingerprintNoContentSignal)
	}
	if blind.Digest == "" {
		t.Error("哨兵不能是空串——空串表示'还没观测过'")
	}
	if !strings.Contains(blind.Basis, "无可用内容信号") {
		t.Errorf("无信号时依据没有说明检测能力缺失：%s", blind.Basis)
	}
	// 无信号时，状态翻转不得让哨兵漂移。
	flipped := FingerprintOf(signal(func(s *KnowledgeSignal) {
		s.FileHash = ""
		s.FileSize = 0
		s.ProcessedAt = time.Time{}
		s.ParseStatus = types.ParseStatusFailed
	}))
	if flipped.Digest != FingerprintNoContentSignal {
		t.Errorf("无内容信号时状态翻转弄脏了哨兵：%q", flipped.Digest)
	}
}

// Observe 是"这次观测要不要让 asset_revision 递增"的唯一判据入口。
func TestObserveBumpsOnlyOnContentChange(t *testing.T) {
	first := Observe(BindingState{Revision: 1}, signal(nil))
	if first.Changed || first.Next.Revision != 1 {
		t.Fatalf("首次观测就递增了版本：%+v", first)
	}
	if first.State != AssetStateReady {
		t.Fatalf("首次观测的 processing_state = %q, want ready", first.State)
	}

	same := Observe(first.Next, signal(nil))
	if same.Changed || same.Next.Revision != 1 {
		t.Fatalf("同内容再次观测递增了版本：%+v", same)
	}

	changed := Observe(first.Next, signal(func(s *KnowledgeSignal) { s.FileHash = "hash-v2" }))
	if !changed.Changed || changed.Next.Revision != 2 {
		t.Fatalf("内容变化未递增版本：%+v", changed)
	}
	if changed.Reason == "" {
		t.Error("递增没有给出原因——人工复核时看不出为什么变了")
	}

	statusOnly := Observe(first.Next, signal(func(s *KnowledgeSignal) {
		s.ParseStatus = types.ParseStatusProcessing
	}))
	if statusOnly.Changed || statusOnly.Next.Revision != 1 {
		t.Fatalf("状态翻转递增了版本：%+v", statusOnly)
	}
	if statusOnly.State != AssetStateProcessing {
		t.Fatalf("状态翻转没有更新 processing_state：%+v", statusOnly)
	}
}

// ParseStatus → processing_state 的映射必须 fail-closed：只有 completed 是 ready。
func TestProcessingStateOfIsFailClosed(t *testing.T) {
	ready := 0
	for _, status := range []string{
		types.ParseStatusPending,
		types.ParseStatusProcessing,
		types.ParseStatusFinalizing,
		types.ParseStatusCompleted,
		types.ParseStatusFailed,
		types.ParseStatusDeleting,
		types.ParseStatusCancelled,
		"unprocessed", // 历史遗留值：DDL 默认值里有，Go 常量里没有
		"",
		"some-future-status",
	} {
		state := ProcessingStateOf(status)
		if state == AssetStateReady {
			ready++
			if status != types.ParseStatusCompleted {
				t.Errorf("ParseStatus %q 被映射成 ready——只有 completed 可以是 ready", status)
			}
		}
		if state == "" {
			t.Errorf("ParseStatus %q 映射出空状态", status)
		}
	}
	if ready != 1 {
		t.Errorf("有 %d 个状态映射成 ready, want 恰好 1（completed）", ready)
	}
}
