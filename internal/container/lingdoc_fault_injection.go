package container

import (
	"context"
	"os"
	"strconv"
	"strings"

	"github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
	"github.com/Tencent/WeKnora/internal/logger"
)

// 受控故障注入的组合根装配（#256 / AC-G3-03）。
//
// 故障注入不是一个产品端点：它只在本进程内、由组合根按环境变量装配一个会失败
// 的窄 port 实现。HTTP 路由、handler 与领域规则都不因此多出一条对外契约。
//
// 开关默认关闭：必须同时点名注入点与演示租户，缺一不可。点名租户是必需的——
// 没有租户边界的注入会命中本进程里的每一个租户。

const (
	// FaultInjectionPointEnv names the seam to fail: impact_task_write,
	// audit_write or authorization.
	FaultInjectionPointEnv = "LINGDOC_FAULT_INJECTION"
	// FaultInjectionTenantEnv names the only tenant the injection may reach.
	FaultInjectionTenantEnv = "LINGDOC_FAULT_TENANT_ID"
	// FaultInjectionActionEnv narrows the injection to one authorization
	// action or one audit action. Empty means every action of that point.
	FaultInjectionActionEnv = "LINGDOC_FAULT_ACTION"
	// FaultInjectionReasonEnv is the readable cause reported to the caller.
	FaultInjectionReasonEnv = "LINGDOC_FAULT_REASON"
)

// faultInjectionFromEnv reads the controlled failure configuration.
//
// Anything incomplete or unknown leaves the configuration inert: a typo in the
// environment must not arm an unintended failure.
func faultInjectionFromEnv(ctx context.Context) workspacecore.FaultInjection {
	point := strings.TrimSpace(os.Getenv(FaultInjectionPointEnv))
	if point == "" {
		return workspacecore.FaultInjection{}
	}
	tenant, err := strconv.ParseUint(strings.TrimSpace(os.Getenv(FaultInjectionTenantEnv)), 10, 64)
	if err != nil || tenant == 0 {
		logger.Warnf(ctx, "受控故障注入未装配：%s 需要一个有效的 %s（演示租户）", FaultInjectionPointEnv, FaultInjectionTenantEnv)
		return workspacecore.FaultInjection{}
	}
	fault := workspacecore.FaultInjection{
		Point:    workspacecore.FaultPoint(point),
		TenantID: tenant,
		Action:   strings.TrimSpace(os.Getenv(FaultInjectionActionEnv)),
		Reason:   strings.TrimSpace(os.Getenv(FaultInjectionReasonEnv)),
	}
	if !fault.Enabled() || !fault.KnownPoint() {
		logger.Warnf(ctx, "受控故障注入未装配：%s=%q 不是受支持的注入点", FaultInjectionPointEnv, point)
		return workspacecore.FaultInjection{}
	}
	logger.Warnf(ctx, "受控故障注入已装配：注入点=%s 租户=%d（仅本进程、仅该租户）", point, tenant)
	return fault
}
