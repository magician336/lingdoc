package workspacecore

import (
	"context"
	"path/filepath"
	"testing"

	lingdoctemplate "github.com/Tencent/WeKnora/internal/lingdoc/template"
	governance "github.com/Tencent/WeKnora/internal/lingdoc/templategovernance"
)

func TestTemplateCopyPreviewMatchesSavedEffectiveRuleSetHash(t *testing.T) {
	for _, binding := range []string{"legacy_original", "legacy_overridden", "canonical", "unbound"} {
		t.Run(binding, func(t *testing.T) {
			ctx := context.Background()
			path := filepath.Join(t.TempDir(), "preview-hash.db")
			var svc *Service
			var owner Actor
			var project Project
			switch binding {
			case "legacy_original", "legacy_overridden":
				var source lingdoctemplate.RuleSet
				svc, owner, project, source = legacyOverrideProject(t, path)
				if binding == "legacy_overridden" {
					input := legacyOverrideInput(project, governance.BindingOverride{RuleID: "goal-pattern", Severity: "info"})
					raw, _, _, err := svc.ApplyLegacyRuleOverrides(ctx, owner, project.ID, "preview-hash-override", input)
					if err != nil {
						t.Fatal(err)
					}
					project = asProject(t, raw)
					if project.TemplateCopy.RulesetHash == source.RulesetHash {
						t.Fatal("fixture must contain a distinct project-effective RuleSet")
					}
				}
			case "canonical":
				var set lingdoctemplate.RuleSet
				svc, owner, project, set = sourcePolicyProject(t, path)
				svc.WithRuleSetSnapshotReader(&ruleSetSnapshotReaderStub{set: set})
				raw, _, _, err := svc.BindTemplateCopyRuleSet(ctx, owner, project.ID, "preview-hash-bind", BindTemplateCopyRuleSetInput{
					ExpectedProjectVersion: project.ProjectVersion, ExpectedTemplateCopyVersion: project.TemplateCopyVersion,
					RuleSetRef: &lingdoctemplate.RuleSetReference{ID: set.ID, Version: set.Version, Hash: set.RulesetHash},
				})
				if err != nil {
					t.Fatal(err)
				}
				project = asProject(t, raw)
				if project.TemplateCopy.Definition.RuleSetSnapshot == nil {
					t.Fatal("fixture must contain the canonical binding")
				}
			case "unbound":
				svc = testStore(t, path)
				owner = Actor{TenantID: 9101, UserID: "preview-hash-owner"}
				seedTenantMember(t, svc, owner)
				raw, _, _, err := svc.CreateProject(ctx, owner, "preview-hash-create", CreateProjectInput{Name: "preview hash", TemplateID: lingdoctemplate.DemoTemplateID})
				if err != nil {
					t.Fatal(err)
				}
				project = asProject(t, raw)
			}
			if binding == "canonical" || binding == "unbound" {
				raw, _, _, err := svc.SaveSpec(ctx, owner, project.ID, "preview-hash-spec", SaveSpecInput{
					ExpectedSpecRevision: project.SpecRevision,
					Fields:               map[string]string{"research_subject": "subject", "research_goal": "goal"},
				})
				if err != nil {
					t.Fatal(err)
				}
				project = asProject(t, raw)
			}
			edit := projectionGuardEdit(project)
			edit.Fields[0].Label += " revised"
			preview, err := svc.PreviewTemplateCopyEdit(ctx, owner, project.ID, edit)
			if err != nil {
				t.Fatalf("preview: %v", err)
			}
			if preview.RulesetChanged || preview.TargetRulesetHash != project.TemplateCopy.RulesetHash {
				t.Fatalf("structural edit misreported effective RuleSet: preview=%+v current=%s", preview, project.TemplateCopy.RulesetHash)
			}
			unchanged, err := svc.GetProject(ctx, owner, project.ID)
			if err != nil || unchanged.ProjectVersion != project.ProjectVersion || unchanged.TemplateCopyVersion != project.TemplateCopyVersion {
				t.Fatalf("preview changed persisted versions: %+v err=%v", unchanged, err)
			}
			raw, _, _, err := svc.SaveTemplateCopy(ctx, owner, project.ID, "preview-hash-save", edit)
			if err != nil {
				t.Fatalf("save: %v", err)
			}
			saved := asProject(t, raw)
			if preview.TargetCopyVersion != saved.TemplateCopyVersion || preview.TargetContentHash != saved.TemplateCopy.ContentHash ||
				preview.TargetRulesetHash != saved.TemplateCopy.RulesetHash ||
				preview.RulesetChanged != (project.TemplateCopy.RulesetHash != saved.TemplateCopy.RulesetHash) {
				t.Fatalf("preview does not match saved copy: preview=%+v saved=%+v", preview, saved.TemplateCopy)
			}
		})
	}
}
