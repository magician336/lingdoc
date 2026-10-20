package container

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
)

// 受控故障注入的组合根装配（#256）。
//
// 开关默认关闭：只有显式点名注入点与演示租户时才会装上会失败的窄 port 实现。

func TestFaultInjectionStaysOffByDefault(t *testing.T) {
	t.Setenv(FaultInjectionPointEnv, "")
	t.Setenv(FaultInjectionTenantEnv, "")
	if fault := faultInjectionFromEnv(context.Background()); fault.Enabled() {
		t.Fatalf("默认装配就启用了注入: %+v", fault)
	}
}

func TestFaultInjectionRequiresATenantScope(t *testing.T) {
	t.Setenv(FaultInjectionPointEnv, string(workspacecore.FaultPointImpactTaskWrite))
	t.Setenv(FaultInjectionTenantEnv, "")
	if fault := faultInjectionFromEnv(context.Background()); fault.Enabled() {
		t.Fatalf("没有租户作用域也启用了注入: %+v", fault)
	}
	t.Setenv(FaultInjectionTenantEnv, "not-a-number")
	if fault := faultInjectionFromEnv(context.Background()); fault.Enabled() {
		t.Fatalf("非法租户也启用了注入: %+v", fault)
	}
}

func TestFaultInjectionIgnoresUnknownPoints(t *testing.T) {
	t.Setenv(FaultInjectionPointEnv, "typo")
	t.Setenv(FaultInjectionTenantEnv, "7")
	if fault := faultInjectionFromEnv(context.Background()); fault.Enabled() {
		t.Fatalf("未知注入点被启用了: %+v", fault)
	}
}

func TestFaultInjectionIsArmedByPointAndTenant(t *testing.T) {
	t.Setenv(FaultInjectionPointEnv, string(workspacecore.FaultPointImpactTaskWrite))
	t.Setenv(FaultInjectionTenantEnv, "7")
	t.Setenv(FaultInjectionActionEnv, "")
	t.Setenv(FaultInjectionReasonEnv, "合成注入")
	fault := faultInjectionFromEnv(context.Background())
	if !fault.Enabled() {
		t.Fatalf("点名注入点与租户后仍然没有装配: %+v", fault)
	}
	if fault.Point != workspacecore.FaultPointImpactTaskWrite || fault.TenantID != 7 || fault.Reason != "合成注入" {
		t.Fatalf("装配出来的注入配置不对: %+v", fault)
	}
}
