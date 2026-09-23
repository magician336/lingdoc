package evidence

import "fmt"

// RunMode 是契约 §9 的运行模式，保真度递增。
type RunMode string

const (
	// ModeMock 全部依赖为替身。
	ModeMock RunMode = "mock"
	// ModeRealAPIFakeModel 真实接口，模型为替身。
	ModeRealAPIFakeModel RunMode = "real_api_fake_model"
	// ModeReal 真实接口 + 真实模型。
	ModeReal RunMode = "real"
)

// CheckStatus 单条断言的结论。
type CheckStatus string

const (
	CheckPassed CheckStatus = "passed"
	CheckFailed CheckStatus = "failed"
	// CheckNotRun 没跑成，必须附原因。它不是通过。
	CheckNotRun CheckStatus = "not_run"
)

// Check 一条断言的结果。
type Check struct {
	// ID 指回契约场景编号（如 scenarios.json 的 F03），便于对上来源。
	ID     string
	Status CheckStatus
	// RequiresModel 标记这条检查是否真的调用了模型。
	// 模式由它推导——声明出来的模式容易被写成想要的而不是实际的。
	RequiresModel bool
	// Detail 失败/未运行时的原因。通过时留空。
	Detail string
}

// Report 一次验证的记录。
type Report struct {
	// declared 是本次运行打算使用的模式。它不是结论，见 EffectiveMode。
	declared RunMode
	Checks   []Check
}

// NewReport 开始一次记录。declared 只作为上限，不会因证据被抬高。
func NewReport(declared RunMode) *Report {
	return &Report{declared: declared}
}

func (r *Report) Add(c Check) {
	r.Checks = append(r.Checks, c)
}

// EffectiveMode 依据实际跑过的检查下调 declared。
//
// 契约 §9：not_run 不得被算作 passed，也不得让整体 mode 升级。所以
// 「打算跑真实模型」不能报成 real——必须有 RequiresModel 的检查真的 passed。
// 下调只朝一个方向：声明为 mock 时不会被证据抬高（宁可少报保真度）。
func (r *Report) EffectiveMode() RunMode {
	for _, c := range r.Checks {
		if c.RequiresModel && c.Status == CheckPassed {
			return r.declared
		}
	}
	if r.declared == ModeReal {
		return ModeRealAPIFakeModel
	}
	return r.declared
}

// Passed 只返回真正通过的检查。not_run 与 failed 都不计入。
func (r *Report) Passed() []Check {
	return r.filter(func(c Check) bool { return c.Status == CheckPassed })
}

// NotRun 返回没跑成的检查（每条都应带原因，见 Problems）。
func (r *Report) NotRun() []Check {
	return r.filter(func(c Check) bool { return c.Status == CheckNotRun })
}

func (r *Report) filter(keep func(Check) bool) []Check {
	var out []Check
	for _, c := range r.Checks {
		if keep(c) {
			out = append(out, c)
		}
	}
	return out
}

// AllPassed 仅当每条检查都真的通过时为真。有任何 not_run 都是假。
func (r *Report) AllPassed() bool {
	return len(r.Checks) > 0 && len(r.Passed()) == len(r.Checks)
}

// Problems 报出报告自身的记录缺陷——目前是"标了 not_run 却没写原因"。
// 缺原因的记录对交接口说等于没记，所以让它可被查询而不是靠人翻。
func (r *Report) Problems() []string {
	var out []string
	for _, c := range r.Checks {
		if c.Status == CheckNotRun && c.Detail == "" {
			out = append(out, fmt.Sprintf("检查 %s 标为 not_run 但未记录原因", c.ID))
		}
	}
	return out
}
