package evidence

import (
	"encoding/json"
	"strings"
	"testing"
)

// 契约 §9：not_run 不得让整体 mode 升级。打算跑真实模型不等于跑成了——
// 没有 RequiresModel 的检查真的 passed，就不许报 real。
func TestReportDowngradesModeWhenModelCheckDidNotRun(t *testing.T) {
	for _, tc := range []struct {
		name       string
		modelCheck CheckStatus
		want       RunMode
	}{
		{"模型的检查根本没跑", CheckNotRun, ModeRealAPIFakeModel},
		{"模型的检查跑了但失败", CheckFailed, ModeRealAPIFakeModel},
		{"模型的检查真的通过", CheckPassed, ModeReal},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rep := &Report{declared: ModeReal}
			rep.Add(Check{ID: "F01-retrieve", Status: CheckPassed})
			rep.Add(Check{
				ID: "S7-real-model", Status: tc.modelCheck, RequiresModel: true,
				Detail: "模型不可用或未配置",
			})

			if got := rep.EffectiveMode(); got != tc.want {
				t.Errorf("EffectiveMode() = %q, want %q", got, tc.want)
			}
		})
	}
}

// not_run 不得被算作 passed：这是契约 §9 最容易被绕过的一条。
func TestReportDoesNotCountNotRunAsPassed(t *testing.T) {
	rep := &Report{}
	rep.Add(Check{ID: "a", Status: CheckPassed})
	rep.Add(Check{ID: "b", Status: CheckNotRun, Detail: "模型不可用"})
	rep.Add(Check{ID: "c", Status: CheckFailed, Detail: "坐标越界"})

	if got := len(rep.Passed()); got != 1 {
		t.Errorf("Passed() 有 %d 条, want 1——not_run 与 failed 都不得计入", got)
	}
	if got := len(rep.NotRun()); got != 1 {
		t.Errorf("NotRun() 有 %d 条, want 1", got)
	}
	if rep.AllPassed() {
		t.Error("AllPassed() 为真，但有一条 not_run 一条 failed")
	}
}

// 声明成 mock 时不许被证据"升级"——保真度宁可少报不可多报。
func TestReportNeverUpgradesDeclaredMode(t *testing.T) {
	rep := &Report{declared: ModeMock}
	rep.Add(Check{ID: "S7", Status: CheckPassed, RequiresModel: true})

	if got := rep.EffectiveMode(); got != ModeMock {
		t.Errorf("EffectiveMode() = %q, want %q", got, ModeMock)
	}
}

// 导出物必须是结论：声明 real 但模型没跑成，序列化出的 mode 不能是 real。
// 否则交接记录会把"打算用 real"当成事实写出去。
func TestReportSerializesEffectiveModeNotDeclared(t *testing.T) {
	rep := &Report{declared: ModeReal}
	rep.Add(Check{ID: "S7", Status: CheckNotRun, RequiresModel: true, Detail: "模型不可用"})

	raw, err := json.Marshal(rep)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var got struct {
		Mode RunMode `json:"mode"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if got.Mode == ModeReal {
		t.Errorf("序列化出的 mode = %q，把意图写成了事实", got.Mode)
	}
}

// 未运行的检查必须带原因。没写原因的报告等于没记，必须能被主动查出来。
func TestReportFlagsNotRunWithoutReason(t *testing.T) {
	rep := &Report{}
	rep.Add(Check{ID: "S7-no-reason", Status: CheckNotRun})
	rep.Add(Check{ID: "S7-ok", Status: CheckNotRun, Detail: "WEKNORA_E2E_* 未设置"})

	problems := rep.Problems()
	if len(problems) != 1 {
		t.Fatalf("Problems() 有 %d 条, want 1（只有没写原因的那条该报）: %v", len(problems), problems)
	}
	if !strings.Contains(problems[0], "S7-no-reason") {
		t.Errorf("Problems() = %q, 没指出是哪条检查", problems[0])
	}
}
