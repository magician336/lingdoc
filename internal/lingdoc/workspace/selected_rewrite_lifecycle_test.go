package workspace

import (
	"encoding/json"
	"testing"

	"github.com/Tencent/WeKnora/internal/lingdoc/candidateadoption"
)

// 这组断言守的是「生命周期阶段」与「失效判据」各归其位。
//
// 候选上曾经只有一个 `Status` 字段，它同时装两件事：这一次运行走到了哪一步
// （queued / ready / applying / failed），以及这个产出还能不能被采纳
// （fresh / stale / not_authorized）。两件事回答的不是同一个问题，混在一个
// 字段里，就必然出现「状态 = 生命周期阶段」这个说法解释不了 `Validity` 的
// 时候。
//
// 归位之后：
//
//   - 失效判据只有 `Validity` 一个拥有者（候选层的枚举）。
//   - `status` 这个 JSON 键承载**生命周期阶段**。键名逐字不变——保留路径
//     （frontend/src/views/lingdoc/Workspace.vue）还在读它。

// TestRewriteLifecycleStageReportsWhereTheRunGot 断言生命周期阶段的取值。
//
// 判据是「这个候选走到了哪一步」：fresh 是 ready，stale 与 not_authorized 是
// stale（都不可采纳），空值 + 失败原因是 failed，空值 + 无失败原因是 queued，
// 正在应用是 applying。
func TestRewriteLifecycleStageReportsWhereTheRunGot(t *testing.T) {
	cases := []struct {
		name string
		row  selectedRewriteRow
		want string
	}{
		{name: "fresh", row: selectedRewriteRow{Validity: string(candidateadoption.ValidityFresh)}, want: "ready"},
		{name: "stale", row: selectedRewriteRow{Validity: string(candidateadoption.ValidityStale)}, want: "stale"},
		{name: "not authorized", row: selectedRewriteRow{Validity: string(candidateadoption.ValidityNotAuthorized)}, want: "stale"},
		{name: "failed", row: selectedRewriteRow{ErrorCode: "model_unavailable"}, want: "failed"},
		{name: "queued", row: selectedRewriteRow{}, want: "queued"},
		{name: "applying", row: selectedRewriteRow{ApplyPendingKey: "apply-key-1"}, want: "applying"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := rewriteLifecycleStage(tc.row); got != tc.want {
				t.Fatalf("lifecycle stage = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestRewriteCandidateKeepsTheStatusKeyAndValidityAsTheVerdict 断言候选的线上
// 形状逐字不变：保留路径还在读 `status`，失效判据由 `validity` 说。
func TestRewriteCandidateKeepsTheStatusKeyAndValidityAsTheVerdict(t *testing.T) {
	candidate := SelectedRewriteCandidate{
		CandidateID:    "candidate-1",
		ProjectID:      "project-1",
		ChapterID:      "chapter-1",
		Validity:       candidateadoption.ValidityFresh,
		LifecycleStage: rewriteLifecycleStage(selectedRewriteRow{Validity: string(candidateadoption.ValidityFresh)}),
		RunMode:        candidateadoption.RunModeMock,
	}
	encoded, err := json.Marshal(candidate)
	if err != nil {
		t.Fatalf("编码候选失败：%v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("解码候选失败：%v", err)
	}
	// JSON 键逐字不变：保留路径依赖它。
	if _, ok := decoded["status"]; !ok {
		t.Fatalf("候选没有 status 键，保留路径读不到它：%s", string(encoded))
	}
	if decoded["status"] != "ready" {
		t.Errorf("status = %v, want ready（生命周期阶段）", decoded["status"])
	}
	if _, ok := decoded["validity"]; !ok {
		t.Fatalf("候选没有 validity 键，失效判据无处安放：%s", string(encoded))
	}
	if decoded["validity"] != "fresh" {
		t.Errorf("validity = %v, want fresh（失效判据只有这一个拥有者）", decoded["validity"])
	}
}
