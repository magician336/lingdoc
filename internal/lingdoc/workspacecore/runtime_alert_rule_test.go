package workspacecore

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

// G6 告警规则的可读回与可停用（#382-A / #382-B）。
//
// 规则此前只有「新建」：既读不回来，也停不掉。这三段用例分别钉住
// 读回（带状态与版本）、处置（停用 / 启用 / 更新）、以及停用之后
// 规则不再产生告警。

func createAlertRule(t *testing.T, svc *Service, actor Actor, projectID, key, signal string) RuntimeAlertRule {
	t.Helper()
	raw, _, _, err := svc.SaveRuntimeAlertRule(context.Background(), actor, projectID, key, RuntimeAlertRule{
		Signal: signal, Severity: "P1", Action: "notify", ResponsibleID: actor.UserID,
	})
	if err != nil {
		t.Fatalf("save alert rule: %v", err)
	}
	var rule RuntimeAlertRule
	if err := json.Unmarshal(raw, &rule); err != nil {
		t.Fatalf("decode alert rule: %v", err)
	}
	return rule
}

func TestG6AlertRulesAreReadableBackWithStatusAndVersion(t *testing.T) {
	svc, actor, p := extendedImpactStore(t)
	rule := createAlertRule(t, svc, actor, p.ID, "alert-rule-readable", "failed")
	if rule.Status != "ACTIVE" || rule.Version != 1 {
		t.Fatalf("a new rule is not armed at version 1: %+v", rule)
	}
	rules, err := svc.ListRuntimeAlertRules(context.Background(), actor, p.ID)
	if err != nil {
		t.Fatalf("list alert rules: %v", err)
	}
	if len(rules) != 1 {
		t.Fatalf("the rule was not readable back: %+v", rules)
	}
	read := rules[0]
	if read.ID != rule.ID || read.Signal != "failed" || read.Severity != "P1" ||
		read.Action != "notify" || read.ResponsibleID != actor.UserID {
		t.Fatalf("the rule came back changed: %+v", read)
	}
	if read.Status != "ACTIVE" || read.Version != 1 {
		t.Fatalf("the rule came back without its state and version: %+v", read)
	}
}

func TestG6AlertRuleCanBeDisabledEnabledAndUpdated(t *testing.T) {
	svc, actor, p := extendedImpactStore(t)
	ctx := context.Background()
	rule := createAlertRule(t, svc, actor, p.ID, "alert-rule-actions", "failed")

	// 停用：状态转 DISABLED，版本递增。
	raw, _, _, err := svc.ActOnRuntimeAlertRule(ctx, actor, p.ID, rule.ID, "alert-rule-disable", RuntimeAlertRuleActionInput{
		ExpectedVersion: 1, Operation: "disable", Reason: "规则误配，先停用",
	})
	if err != nil {
		t.Fatalf("disable the rule: %v", err)
	}
	var disabled RuntimeAlertRule
	if err := json.Unmarshal(raw, &disabled); err != nil {
		t.Fatalf("decode the disabled rule: %v", err)
	}
	if disabled.Status != "DISABLED" || disabled.Version != 2 {
		t.Fatalf("the rule was not disabled: %+v", disabled)
	}
	// 停用之后读回来也是停用的。
	rules, err := svc.ListRuntimeAlertRules(ctx, actor, p.ID)
	if err != nil || len(rules) != 1 || rules[0].Status != "DISABLED" || rules[0].Version != 2 {
		t.Fatalf("the disabled rule did not come back: %+v %v", rules, err)
	}
	// 已停用再停用：状态不对。
	if _, _, _, err := svc.ActOnRuntimeAlertRule(ctx, actor, p.ID, rule.ID, "alert-rule-disable-twice", RuntimeAlertRuleActionInput{
		ExpectedVersion: 2, Operation: "disable", Reason: "重复停用",
	}); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("a disabled rule was disabled again: %v", err)
	}
	// 启用：状态转 ACTIVE。
	raw, _, _, err = svc.ActOnRuntimeAlertRule(ctx, actor, p.ID, rule.ID, "alert-rule-enable", RuntimeAlertRuleActionInput{
		ExpectedVersion: 2, Operation: "enable", Reason: "规则已修好",
	})
	if err != nil {
		t.Fatalf("enable the rule: %v", err)
	}
	var enabled RuntimeAlertRule
	if err := json.Unmarshal(raw, &enabled); err != nil {
		t.Fatalf("decode the enabled rule: %v", err)
	}
	if enabled.Status != "ACTIVE" || enabled.Version != 3 {
		t.Fatalf("the rule was not enabled: %+v", enabled)
	}
	// 已启用再启用：状态不对。
	if _, _, _, err := svc.ActOnRuntimeAlertRule(ctx, actor, p.ID, rule.ID, "alert-rule-enable-twice", RuntimeAlertRuleActionInput{
		ExpectedVersion: 3, Operation: "enable", Reason: "重复启用",
	}); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("an active rule was enabled again: %v", err)
	}
	// 更新：只改给的字段，没给的字段保持原值。
	raw, _, _, err = svc.ActOnRuntimeAlertRule(ctx, actor, p.ID, rule.ID, "alert-rule-update", RuntimeAlertRuleActionInput{
		ExpectedVersion: 3, Operation: "update", Reason: "调整严重级别", Severity: "P0",
	})
	if err != nil {
		t.Fatalf("update the rule: %v", err)
	}
	var updated RuntimeAlertRule
	if err := json.Unmarshal(raw, &updated); err != nil {
		t.Fatalf("decode the updated rule: %v", err)
	}
	if updated.Severity != "P0" {
		t.Fatalf("the severity was not updated: %+v", updated)
	}
	if updated.Signal != "failed" || updated.Action != "notify" || updated.ResponsibleID != actor.UserID {
		t.Fatalf("an update dropped a field that was not sent: %+v", updated)
	}
	if updated.Version != 4 || updated.Status != "ACTIVE" {
		t.Fatalf("the update lost the state or the version: %+v", updated)
	}
	// 版本不符：冲突。
	if _, _, _, err := svc.ActOnRuntimeAlertRule(ctx, actor, p.ID, rule.ID, "alert-rule-stale", RuntimeAlertRuleActionInput{
		ExpectedVersion: 1, Operation: "disable", Reason: "用旧版本",
	}); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("a stale expected version was accepted: %v", err)
	}
	// 没有理由：请求不合法。
	if _, _, _, err := svc.ActOnRuntimeAlertRule(ctx, actor, p.ID, rule.ID, "alert-rule-no-reason", RuntimeAlertRuleActionInput{
		ExpectedVersion: 4, Operation: "disable",
	}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("a decision without a reason was accepted: %v", err)
	}
	// 处置动作改到不存在的值：请求不合法（合法值与新建时同一套）。
	if _, _, _, err := svc.ActOnRuntimeAlertRule(ctx, actor, p.ID, rule.ID, "alert-rule-bad-disposition", RuntimeAlertRuleActionInput{
		ExpectedVersion: 4, Operation: "update", Reason: "改成一个不存在的处置动作", Disposition: "explode",
	}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("an illegal disposition was accepted: %v", err)
	}
	// 处置动作改到合法值。
	raw, _, _, err = svc.ActOnRuntimeAlertRule(ctx, actor, p.ID, rule.ID, "alert-rule-disposition", RuntimeAlertRuleActionInput{
		ExpectedVersion: 4, Operation: "update", Reason: "改成人工确认", Disposition: "human_confirm",
	})
	if err != nil {
		t.Fatalf("update the disposition: %v", err)
	}
	var disposition RuntimeAlertRule
	if err := json.Unmarshal(raw, &disposition); err != nil {
		t.Fatalf("decode the disposition update: %v", err)
	}
	if disposition.Action != "human_confirm" {
		t.Fatalf("the disposition was not updated: %+v", disposition)
	}
}

func TestG6DisabledAlertRuleStopsReactingToRuntimeEvents(t *testing.T) {
	svc, actor, p := extendedImpactStore(t)
	ctx := context.Background()
	rule := createAlertRule(t, svc, actor, p.ID, "alert-rule-silence", "failed")

	// 规则武装着：一次失败产生一条告警。
	if err := svc.ObserveRuntime(ctx, actor, p.ID, "model", "failed", 5); err != nil {
		t.Fatalf("observe the failure: %v", err)
	}
	alerts, err := svc.ListRuntimeAlerts(ctx, actor, p.ID)
	if err != nil {
		t.Fatalf("list alerts: %v", err)
	}
	if len(alerts) != 1 || alerts[0].RuleID != rule.ID {
		t.Fatalf("the armed rule did not react: %+v", alerts)
	}

	// 停用之后：同样的失败不再产生告警。
	if _, _, _, err := svc.ActOnRuntimeAlertRule(ctx, actor, p.ID, rule.ID, "alert-rule-silence-off", RuntimeAlertRuleActionInput{
		ExpectedVersion: 1, Operation: "disable", Reason: "先停用",
	}); err != nil {
		t.Fatalf("disable the rule: %v", err)
	}
	if err := svc.ObserveRuntime(ctx, actor, p.ID, "model", "failed", 5); err != nil {
		t.Fatalf("observe the second failure: %v", err)
	}
	alerts, err = svc.ListRuntimeAlerts(ctx, actor, p.ID)
	if err != nil {
		t.Fatalf("list alerts again: %v", err)
	}
	if len(alerts) != 1 {
		t.Fatalf("a disabled rule still reacted: %+v", alerts)
	}

	// 重新启用：它又开始反应。
	if _, _, _, err := svc.ActOnRuntimeAlertRule(ctx, actor, p.ID, rule.ID, "alert-rule-silence-on", RuntimeAlertRuleActionInput{
		ExpectedVersion: 2, Operation: "enable", Reason: "重新启用",
	}); err != nil {
		t.Fatalf("enable the rule: %v", err)
	}
	if err := svc.ObserveRuntime(ctx, actor, p.ID, "model", "failed", 5); err != nil {
		t.Fatalf("observe the third failure: %v", err)
	}
	alerts, err = svc.ListRuntimeAlerts(ctx, actor, p.ID)
	if err != nil {
		t.Fatalf("list alerts a third time: %v", err)
	}
	if len(alerts) != 2 {
		t.Fatalf("the re-enabled rule did not react again: %+v", alerts)
	}
}

func TestG6AlertRuleActionsRefuseAnOutsider(t *testing.T) {
	svc, actor, p := extendedImpactStore(t)
	ctx := context.Background()
	rule := createAlertRule(t, svc, actor, p.ID, "alert-rule-outsider", "failed")
	outsider := Actor{TenantID: actor.TenantID, UserID: "outsider"}

	// 越权读规则：不存在，不是被禁止。
	if _, err := svc.ListRuntimeAlertRules(ctx, outsider, p.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("an outsider read the alert rules: %v", err)
	}
	// 越权处置规则：同样不存在。
	if _, _, _, err := svc.ActOnRuntimeAlertRule(ctx, outsider, p.ID, rule.ID, "alert-rule-outsider-act", RuntimeAlertRuleActionInput{
		ExpectedVersion: 1, Operation: "disable", Reason: "越权停用",
	}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("an outsider acted on the alert rule: %v", err)
	}
	// 不是本课题的规则：在本课题里不存在。规则是按课题读的，所以别的课题的
	// 规则在这里只是一个找不到的 key。
	if _, _, _, err := svc.ActOnRuntimeAlertRule(ctx, actor, p.ID, "rule-of-another-project", "alert-rule-cross-project", RuntimeAlertRuleActionInput{
		ExpectedVersion: 1, Operation: "disable", Reason: "跨课题停用",
	}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("a rule of another project was acted on: %v", err)
	}
}
