package candidateadoption

import (
	"encoding/json"
	"errors"

	"github.com/Tencent/WeKnora/internal/lingdoc/template"
	governance "github.com/Tencent/WeKnora/internal/lingdoc/templategovernance"
	"gorm.io/gorm"
)

// Delivery reads the exact copy sidecar in its existing workspace snapshot.
// It never substitutes the catalog's current/latest version for frozen history.
type deliveryBindingSidecar struct {
	RuleSetSnapshot      *template.RuleSet          `json:"rule_set_snapshot"`
	SourceRuleSetID      string                     `json:"source_ruleset_id"`
	SourceRuleSetVersion string                     `json:"source_ruleset_version"`
	SourceRuleSetHash    string                     `json:"source_ruleset_hash"`
	BindingPolicies      []governance.BindingPolicy `json:"binding_policies"`
	SourcePolicyHash     string                     `json:"source_policy_hash"`
}

func readDeliveryBinding(tx *gorm.DB, projectID, copyID string) (deliveryBindingSidecar, error) {
	var value deliveryBindingSidecar
	if !tx.Migrator().HasTable("lingdoc_g5_records") {
		return value, nil
	}
	var row struct{ PayloadJSON string }
	err := tx.Table("lingdoc_g5_records").Select("payload_json").Where("project_id=? AND kind='template_binding' AND id=?", projectID, copyID).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return value, nil
	}
	if err != nil {
		return value, err
	}
	if err := json.Unmarshal([]byte(row.PayloadJSON), &value); err != nil {
		return value, err
	}
	if value.RuleSetSnapshot == nil || value.RuleSetSnapshot.State == template.RuleSetDraft {
		return value, ErrInvalidState
	}
	if err := template.ValidateRuleSet(*value.RuleSetSnapshot); err != nil {
		return value, err
	}
	// Canonical G4 copies may have no legacy policy. Any declared policy
	// remains hash-bound; a missing hash never permits nonempty policies.
	if value.SourcePolicyHash != "" || len(value.BindingPolicies) != 0 {
		hash, err := governance.HashBindingPolicies(value.BindingPolicies)
		if err != nil || hash != value.SourcePolicyHash {
			return value, ErrInvalidState
		}
	}
	if value.SourceRuleSetID == "" || value.SourceRuleSetVersion == "" || value.SourceRuleSetHash == "" {
		return value, ErrInvalidState
	}
	return value, nil
}
