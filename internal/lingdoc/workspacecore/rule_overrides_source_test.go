package workspacecore

import (
	"context"
	"path/filepath"
	"reflect"
	"strconv"
	"testing"

	template "github.com/Tencent/WeKnora/internal/lingdoc/template"
	governance "github.com/Tencent/WeKnora/internal/lingdoc/templategovernance"
)

// This proves the stored project-copy history can provide the exact original
// snapshot on the same project transaction after an effective snapshot exists,
// without requiring the catalog service or live publication state.
func TestLegacyOverrideSourceSnapshotIsInProjectCopyHistory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy-source-proof.db")
	svc, owner, project, _ := sourcePolicyProject(t, path)
	original := project.TemplateCopy
	effective, err := governance.ApplyBindingOverrides(*original.RuleSetSnapshot, "project-"+project.ID,
		strconv.FormatInt(project.TemplateCopyVersion+1, 10), original.BindingPolicies,
		[]governance.BindingOverride{{RuleID: "goal-required", Severity: "info"}})
	if err != nil {
		t.Fatal(err)
	}
	definition := template.Clone(original.Definition)
	definition.Rules, err = template.CompileRuleSet(effective)
	if err != nil {
		t.Fatal(err)
	}
	definition, err = template.WithHashes(definition)
	if err != nil {
		t.Fatal(err)
	}
	copy, err := projectTemplateCopy(owner, project.ID, project.TemplateCopyVersion+1, definition, TemplateCopyDraft)
	if err != nil {
		t.Fatal(err)
	}
	copy.RuleSetSnapshot = &effective
	copy.SourceRuleSetID, copy.SourceRuleSetVersion, copy.SourceRuleSetHash = original.SourceRuleSetID, original.SourceRuleSetVersion, original.SourceRuleSetHash
	copy.BindingPolicies, copy.SourcePolicyHash = cloneRuleSetSourcePolicies(original.BindingPolicies), original.SourcePolicyHash
	copy.RulesetHash = effective.RulesetHash
	if err := svc.repository.Transaction(context.Background(), TransactionOptions{}, func(tx Transaction) error {
		next := project
		next.ProjectVersion++
		next.TemplateCopyVersion, next.TemplateCopy = copy.Version, copy
		return tx.UpdateProject(owner.TenantID, project, next)
	}); err != nil {
		t.Fatal(err)
	}
	// The historical read must work even when the original catalog resolver is
	// unavailable. Restarting also proves this is durable sidecar state.
	svc = reopenSourcePolicyStore(t, svc, path)
	if err := svc.repository.Transaction(context.Background(), TransactionOptions{}, func(tx Transaction) error {
		current, err := tx.Project(owner.TenantID, project.ID)
		if err != nil {
			return err
		}
		reader, ok := tx.(interface {
			ProjectTemplateCopy(uint64, string, int64) (ProjectTemplateCopy, error)
		})
		if !ok {
			t.Fatal("transaction cannot read project-copy history")
		}
		history, err := reader.ProjectTemplateCopy(owner.TenantID, project.ID, original.Version)
		if err != nil {
			return err
		}
		if history.RuleSetSnapshot == nil || current.TemplateCopy.RuleSetSnapshot == nil ||
			!reflect.DeepEqual(history.RuleSetSnapshot, original.RuleSetSnapshot) ||
			history.RuleSetSnapshot.ID != current.TemplateCopy.SourceRuleSetID ||
			history.RuleSetSnapshot.Version != current.TemplateCopy.SourceRuleSetVersion ||
			history.RuleSetSnapshot.RulesetHash != current.TemplateCopy.SourceRuleSetHash ||
			history.SourcePolicyHash != current.TemplateCopy.SourcePolicyHash ||
			current.TemplateCopy.RuleSetSnapshot.RulesetHash == history.RuleSetSnapshot.RulesetHash {
			t.Fatalf("source/effective history mismatch: history=%+v current=%+v", history, current.TemplateCopy)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
