package workspacecore

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"

	lingdoctemplate "github.com/Tencent/WeKnora/internal/lingdoc/template"
)

// #468 切片 2(管理裁定 2026-11-04:候选 B——运行控制侧配置切换入口)。
//
// 完整链:回退(生成逆提案,课题不切)→ 恢复被拒(课题没带着目标配置)→
// **apply_rollback:经既有领域入口应用逆提案,课题真的切回目标配置** →
// 恢复成功。切回是负责人主动的独立动作,走全量领域校验;rollback 本身的
// 「不切课题状态」语义不破。
func TestG6SwitchBackToTheVerifiedConfigThenResumeSucceeds(t *testing.T) {
	// r26 的模板升级夹具,但给升级追加一条规则:这样升级后课题的规则摘要
	// **真实变化**——回退目标记录的旧摘要与课题当前摘要不同,正是残留①的
	// 触发形状(只改字段显示名的升级不换摘要,构造不出那个差异)。
	svc, actor, baseline, after, source := func() (*Service, Actor, Project, Project, ChangeSet) {
		path := filepath.Join(t.TempDir(), "template-switch-current-baseline.db")
		s := testStore(t, path)
		a := Actor{TenantID: 926, UserID: "r26-owner"}
		baseline := seedActiveImpactProject(t, s, a)
		if baseline.TemplateCopy == nil || baseline.TemplateCopy.Status != TemplateCopyBound {
			t.Fatal("fixture requires a real bound template")
		}
		definition := lingdoctemplate.Clone(baseline.TemplateCopy.Definition)
		definition.Fields[0].Label += " (reviewed R26 switch upgrade)"
		rules := append([]lingdoctemplate.Rule{}, definition.Rules...)
		rules = append(rules, lingdoctemplate.Rule{ID: "switch-extra-required", Kind: "computed", Severity: lingdoctemplate.SeverityInfo, Evaluator: "required_fields", Parameters: map[string]any{}})
		raw, status, replayed, err := s.CreateChangeSet(context.Background(), a, baseline.ID, "switch-template-source-create", CreateChangeSetInput{
			ExpectedContextRevision: baseline.CurrentContextRevision, Reason: "Only the reviewed template migration",
			TemplateUpgrade: &TemplateUpgradeInput{
				ExpectedProjectVersion: baseline.ProjectVersion, ExpectedTemplateCopyVersion: baseline.TemplateCopyVersion,
				Fields: definition.Fields, Sections: definition.Sections, Terms: append([]lingdoctemplate.Term{}, definition.Terms...),
				RequiredFields: definition.RequiredFields, Rules: rules, FieldValues: map[string]string{},
			},
		})
		if err != nil || status != 201 || replayed {
			t.Fatalf("create source: status=%d replay=%v err=%v", status, replayed, err)
		}
		change := pr150DecodeChange(t, raw)
		raw, status, replayed, err = s.ApplyChangeSet(context.Background(), a, baseline.ID, change.ID, "switch-template-source-apply")
		if err != nil || status != 200 || replayed {
			t.Fatalf("apply source: status=%d replay=%v err=%v", status, replayed, err)
		}
		change = pr150DecodeChange(t, raw)
		after, err := s.GetProject(context.Background(), a, baseline.ID)
		if err != nil || after.TemplateCopy == nil || after.TemplateCopy.RulesetHash == baseline.TemplateCopy.RulesetHash {
			t.Fatalf("the upgrade did not change the ruleset hash: after=%+v err=%v", after.TemplateCopy, err)
		}
		return s, a, baseline, after, change
	}()
	ctx := context.Background()
	checker := &acceptingRuntimeRecovery{}
	svc.SetRuntimeRecoveryChecker(checker)
	// 启用运行模式:「停止新写入」闸门(RuntimeGate)只在 runtimeMode 非空时
	// 生效——不启用的话,豁免逻辑本身测不到。
	svc.SetRuntimeMode("mock")

	// 回退到升级前的模板版本:生成逆提案、暂停运行、记录目标。
	raw, _, _, err := svc.ControlRuntime(ctx, actor, after.ID, "switch-rollback", RuntimeControlInput{
		Scope: RuntimeScope{Kind: "project", Value: after.ID}, Action: "rollback", Reason: "回退模板升级",
		Rollback: &RuntimeRollbackInput{ChangeSetID: source.ID, TemplateVersion: baseline.TemplateVersion, RulesetHash: baseline.TemplateCopy.RulesetHash},
	})
	if err != nil {
		t.Fatalf("rollback: %v", err)
	}
	var control RuntimeControl
	if err := json.Unmarshal(raw, &control); err != nil {
		t.Fatalf("decode the control: %v", err)
	}
	if control.InverseChangeSetID == "" {
		t.Fatalf("the rollback did not record its inverse proposal: %+v", control)
	}

	// 残留①的现状:课题还在升级后的配置上,恢复被拒。
	if _, _, _, err := svc.ControlRuntime(ctx, actor, after.ID, "switch-resume-blocked", RuntimeControlInput{
		ExpectedVersion: 1, Scope: RuntimeScope{Kind: "project", Value: after.ID}, Action: "resume",
		Reason: "恢复运行", SnapshotID: "snapshot-1", ExportID: "export-1",
	}); !errors.Is(err, ErrRuntimeRecoveryRequired) {
		t.Fatalf("a resume before the switch was not refused the way #468 describes: %v", err)
	}

	// 切回已验证配置:经领域入口应用逆提案,课题真的回到基线配置。
	switched, _, _, err := svc.ControlRuntime(ctx, actor, after.ID, "switch-apply-rollback", RuntimeControlInput{
		ExpectedVersion: 1, Scope: RuntimeScope{Kind: "project", Value: after.ID}, Action: "apply_rollback",
		Reason: "切回已验证配置",
	})
	if err != nil {
		t.Fatalf("apply_rollback: %v", err)
	}
	var switchedControl RuntimeControl
	if err := json.Unmarshal(switched, &switchedControl); err != nil {
		t.Fatalf("decode the switched control: %v", err)
	}
	if !switchedControl.RollbackApplied {
		t.Fatalf("the switch did not record itself: %+v", switchedControl)
	}
	if switchedControl.Status != "PAUSED" {
		t.Fatalf("the switch must not resume the runtime by itself: %+v", switchedControl)
	}
	now, err := svc.GetProject(ctx, actor, after.ID)
	if err != nil {
		t.Fatalf("read the project: %v", err)
	}
	// 模板副本版本只增不减(历史可追溯):「切回」= 新副本承载**旧配置内容**
	// (规则摘要回到回退目标记录的那份),而不是副本版本号倒退。
	if now.TemplateCopy == nil || now.TemplateCopy.RulesetHash != baseline.TemplateCopy.RulesetHash {
		t.Fatalf("the switch did not move the project onto the rollback target's ruleset: %+v", now.TemplateCopy)
	}
	if now.TemplateCopyVersion != after.TemplateCopyVersion+1 {
		t.Fatalf("the switched copy must be a new, later copy: %+v", now)
	}

	// 恢复:复验照跑,目标配置现在可读——不再 ErrRuntimeRecoveryRequired。
	resumed, _, _, err := svc.ControlRuntime(ctx, actor, after.ID, "switch-resume-ok", RuntimeControlInput{
		ExpectedVersion: 2, Scope: RuntimeScope{Kind: "project", Value: after.ID}, Action: "resume",
		Reason: "恢复运行", SnapshotID: "snapshot-1", ExportID: "export-1",
	})
	if err != nil {
		t.Fatalf("resume after the switch: %v", err)
	}
	var resumedControl RuntimeControl
	if err := json.Unmarshal(resumed, &resumedControl); err != nil {
		t.Fatalf("decode the resumed control: %v", err)
	}
	if resumedControl.Status != "ACTIVE" || !resumedControl.RecoveryVerified {
		t.Fatalf("the resume did not activate the runtime: %+v", resumedControl)
	}
}

// 切回的前置:回退必须带着逆提案(没给 ChangeSetID 的回退没有可应用的提案),
// 且运行必须处于暂停中。
func TestG6ApplyRollbackNeedsTheInverseProposalAndAPausedRuntime(t *testing.T) {
	svc, actor, p := extendedImpactStore(t)
	ctx := context.Background()

	// 只记录目标、没有逆提案的回退:切回无从谈起。
	if _, _, _, err := svc.ControlRuntime(ctx, actor, p.ID, "switch-no-inverse", RuntimeControlInput{
		Scope: RuntimeScope{Kind: "project", Value: p.ID}, Action: "rollback", Reason: "只记录目标",
		Rollback: &RuntimeRollbackInput{TemplateVersion: p.TemplateVersion},
	}); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	if _, _, _, err := svc.ControlRuntime(ctx, actor, p.ID, "switch-without-inverse", RuntimeControlInput{
		ExpectedVersion: 1, Scope: RuntimeScope{Kind: "project", Value: p.ID}, Action: "apply_rollback",
		Reason: "切回已验证配置",
	}); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("a switch without an inverse proposal was accepted: %v", err)
	}

	// 运行不处于暂停中时:没有控制记录,同样拒绝。
	svc2, actor2, p2 := extendedImpactStore(t)
	if _, _, _, err := svc2.ControlRuntime(ctx, actor2, p2.ID, "switch-not-paused", RuntimeControlInput{
		ExpectedVersion: 0, Scope: RuntimeScope{Kind: "project", Value: p2.ID}, Action: "apply_rollback",
		Reason: "未暂停时的切回",
	}); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("a switch outside a paused runtime was accepted: %v", err)
	}
	_ = p
}
