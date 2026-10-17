package workspace

import (
	"encoding/json"
	"slices"

	"github.com/Tencent/WeKnora/internal/lingdoc/template"
	governance "github.com/Tencent/WeKnora/internal/lingdoc/templategovernance"
)

// These are public server syntax contracts, not additional template rules or
// project data. Global computed checks cannot be bound to a selected field or
// chapter, so they must not be offered as target-specific model proposals.
type suggestionDefinitionContract struct {
	ID                string         `json:"definition_id"`
	Version           string         `json:"definition_version"`
	ParameterExample  map[string]any `json:"parameter_example"`
	ParameterContract string         `json:"parameter_contract"`
}

func ruleSuggestionDefinitions(targetKind string) []suggestionDefinitionContract {
	definitions := []suggestionDefinitionContract{}
	for _, definition := range template.RuleDefinitions() {
		if !slices.Contains(definition.AllowedTargetKinds, targetKind) {
			continue
		}
		entry := suggestionDefinitionContract{ID: definition.ID, Version: definition.Version}
		switch definition.ID {
		case "presence", "model_assisted":
			entry.ParameterExample = map[string]any{}
			entry.ParameterContract = "parameters必须为{}；不要包含target_kind、target_id、evaluator、code或任何其他参数"
		case "pattern":
			entry.ParameterExample = map[string]any{"pattern": "^.+$"}
			entry.ParameterContract = "仅pattern：有效Go正则表达式字符串，最多512字节"
		case "consistency":
			entry.ParameterExample = map[string]any{"preferred": "研究对象", "variants": []string{"调研对象"}}
			entry.ParameterContract = "仅preferred（非空字符串）、variants（非空字符串数组；每项不同于preferred）"
		case "computed/word_count", "computed/character_count":
			entry.ParameterExample = map[string]any{"minimum": 1}
			entry.ParameterContract = "仅minimum和/或maximum：0至1000000的整数，至少一项；minimum不得大于maximum"
		default:
			// A future definition needs an explicit reviewed parameter contract.
			continue
		}
		definitions = append(definitions, entry)
	}
	return definitions
}

func ruleSuggestionStructuredInput(request governance.RuleSuggestionRequest) (json.RawMessage, error) {
	definitions := ruleSuggestionDefinitions(request.Scope.TargetKind)
	if len(definitions) == 0 {
		return nil, governance.ErrInvalidRequest
	}
	return json.Marshal(map[string]any{
		"scope":               request.Scope,
		"template":            request.Template,
		"ruleset":             request.RuleSet,
		"prompt_version":      request.PromptVersion,
		"allowed_definitions": definitions,
		"output_constraints": []string{
			"根对象仅proposals和explanation，不得输出basis、scope、template、ruleset、版本依据或其他字段",
			"proposals为1至20项；每项仅含示范中的8个字段，禁止rule_id、severity、executor",
			"definition_id与definition_version必须配对选择allowed_definitions；parameters必须遵守相应parameter_contract",
			"target_kind和target_id严格等于当前scope；evidence_source_ids仅能选scope.sources中明确授权的ID，无来源则为[]",
			"message与rationale为非空解释，explanation说明不确定性和仍需人工复核；不得声称规则已生效或科学结论已核实",
		},
		"output_schema": map[string]any{
			"proposals": []map[string]any{{
				"definition_id":       definitions[0].ID,
				"definition_version":  definitions[0].Version,
				"target_kind":         request.Scope.TargetKind,
				"target_id":           request.Scope.TargetID,
				"parameters":          definitions[0].ParameterExample,
				"message":             "建议明确当前目标的内容",
				"rationale":           "当前模板要求该目标；此候选仍需人工复核",
				"evidence_source_ids": []string{},
			}},
			"explanation": "仅为候选示范，实际建议必须解释适用性和不确定性，不能自动生效",
		},
	})
}
