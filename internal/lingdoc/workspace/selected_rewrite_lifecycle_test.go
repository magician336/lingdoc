package workspace

import (
	"encoding/json"
	"testing"

	"github.com/Tencent/WeKnora/internal/lingdoc/candidateadoption"
)

// 这组断言守的是「失效判据只有一个拥有者，『正在应用』是一个事实而不是一个阶段」。
//
// 候选上曾经有一个 `status` 字段，它同时装两件事：这一次运行走到了哪一步
// （queued / ready / applying / failed / stale），以及这个产出还能不能被采纳
// （fresh / stale / not_authorized）。两件事回答的不是同一个问题，混在一个
// 字段里，就必然出现「状态 = 生命周期阶段」这个说法解释不了 `Validity` 的
// 时候。
//
// 归位之后：
//
//   - 失效判据只有 `Validity` 一个拥有者（候选层的枚举）。
//   - 「正在应用」是 `apply_pending` 这个布尔事实：它由行上两把键的关系派生，
//     不再是一段需要另外维护的字符串状态机。
//   - 线上形状里不再有 `status` 键：保留路径（frontend/src/views/lingdoc/
//     Workspace.vue）已改读 `validity` 与 `apply_pending`，那个键没有读者了。

// TestSelectedRewriteViewReportsApplyPendingWhileTheApplyIsRunning 断言
// 「正在应用」由行上的两把键派生：有待应用的键、且还没有落下的应用结果 ⇒
// 这次采纳正在写；应用已落下 ⇒ 不再是。
func TestSelectedRewriteViewReportsApplyPendingWhileTheApplyIsRunning(t *testing.T) {
	cases := []struct {
		name string
		row  selectedRewriteRow
		want bool
	}{
		{name: "never applied", row: selectedRewriteRow{}, want: false},
		{name: "applying", row: selectedRewriteRow{ApplyPendingKey: "apply-key-1"}, want: true},
		{
			name: "already applied",
			row:  selectedRewriteRow{ApplyPendingKey: "apply-key-1", AppliedKey: "apply-key-1"},
			want: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			row := tc.row
			row.ID = "candidate-1"
			row.ProjectID = "project-1"
			row.ChapterID = "chapter-1"
			row.Validity = string(candidateadoption.ValidityFresh)
			row.SourceIDsJSON = "[]"
			row.AuthorizedSourcesJSON = "[]"
			row.ReviewItemsJSON = "[]"
			candidate, err := selectedRewriteView(row)
			if err != nil {
				t.Fatalf("读候选视图失败：%v", err)
			}
			if candidate.ApplyPending != tc.want {
				t.Fatalf("apply_pending = %v, want %v", candidate.ApplyPending, tc.want)
			}
		})
	}
}

// TestRewriteCandidateKeepsValidityAsTheOnlyVerdict 断言候选的线上形状：
// 失效判据由 `validity` 说，「正在应用」由 `apply_pending` 说，`status` 键
// 不再出现。
func TestRewriteCandidateKeepsValidityAsTheOnlyVerdict(t *testing.T) {
	candidate := SelectedRewriteCandidate{
		CandidateID:  "candidate-1",
		ProjectID:    "project-1",
		ChapterID:    "chapter-1",
		Validity:     candidateadoption.ValidityFresh,
		ApplyPending: true,
		RunMode:      candidateadoption.RunModeMock,
	}
	encoded, err := json.Marshal(candidate)
	if err != nil {
		t.Fatalf("编码候选失败：%v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("解码候选失败：%v", err)
	}
	if _, ok := decoded["status"]; ok {
		t.Fatalf("候选还有 status 键：保留路径已改读 validity 与 apply_pending：%s", string(encoded))
	}
	if _, ok := decoded["apply_pending"]; !ok {
		t.Fatalf("候选没有 apply_pending 键，「正在应用」无处安放：%s", string(encoded))
	}
	if decoded["apply_pending"] != true {
		t.Errorf("apply_pending = %v, want true（正在应用是一个事实，不是一段状态词）", decoded["apply_pending"])
	}
	if _, ok := decoded["validity"]; !ok {
		t.Fatalf("候选没有 validity 键，失效判据无处安放：%s", string(encoded))
	}
	if decoded["validity"] != "fresh" {
		t.Errorf("validity = %v, want fresh（失效判据只有这一个拥有者）", decoded["validity"])
	}
}
