package writingaction

import "slices"

// Description 是面向 UI 的可序列化动作描述。
//
// 它由执行计划派生，不维护第二份数据源：动作名、产出形状与允许的采纳目标
// 来自同一次 PlanFor 解析，允许范围与可用产出参数来自同一份包内规格表。
type Description struct {
	// Action 是动作标识。
	Action Action `json:"action"`
	// Label 是动作的中文名。
	Label string `json:"label"`
	// Scopes 是这个动作允许的目标范围。
	Scopes []Scope `json:"scopes"`
	// OutputParams 是这个动作可用的产出参数维度及其取值集。
	OutputParams []ParamOption `json:"output_params"`
	// Produce 是产出形状。
	Produce Produce `json:"produce"`
	// Targets 是这个动作允许的采纳目标。
	Targets []AdoptionTarget `json:"targets"`
}

// ParamOption 是一个可用的产出参数维度。
type ParamOption struct {
	// Name 是维度名：length / tone / ratio / transform_mode / target_language / audience。
	Name string `json:"name"`
	// Values 是该维度的闭合取值集；连续区间型的维度留空。
	Values []string `json:"values"`
	// Min 与 Max 是连续区间型维度的闭区间边界；0 表示不适用。
	Min int `json:"min"`
	Max int `json:"max"`
	// AppliesTo 是该维度适用的转写子模式；空表示与模式无关。
	AppliesTo string `json:"applies_to,omitempty"`
}

// 产出参数维度的名字。
const (
	paramNameLength         = "length"
	paramNameTone           = "tone"
	paramNameRatio          = "ratio"
	paramNameTransformMode  = "transform_mode"
	paramNameTargetLanguage = "target_language"
	paramNameAudience       = "audience"
)

// Describe 返回某个目标范围下可用的动作描述。
//
// 每个动作先用一份合法的产出参数解析出 Plan，再把 Plan 的动作名、产出形状与
// 采纳目标写进描述；允许范围与可用产出参数来自同一份包内规格表——仓里不存在
// 第二份手写的动作语义。
func Describe(scope Scope) []Description {
	descriptions := make([]Description, 0, len(actionSpecs))
	for _, spec := range actionSpecs {
		if !slices.Contains(spec.scopes, scope) {
			continue
		}
		plan, err := PlanFor(spec.action, scope, spec.canonicalParams())
		if err != nil {
			continue
		}
		descriptions = append(descriptions, Description{
			Action:       plan.Action,
			Label:        spec.label,
			Scopes:       spec.scopes,
			OutputParams: describeParams(spec),
			Produce:      plan.Produce,
			Targets:      plan.Targets,
		})
	}
	return descriptions
}

// canonicalParams 是这个动作的一份合法产出参数。
//
// 它由允许的参数维度机械地取第一个合法取值，不引入第二份取值表。
func (s actionSpec) canonicalParams() OutputParams {
	params := OutputParams{}
	for _, kind := range s.params {
		switch kind {
		case paramLength:
			params.Length = LengthShort
		case paramTone:
			params.Tone = ToneNeutral
		case paramRatio:
			params.Ratio = 50
		case paramTransform:
			language := LangZH
			params.Transform = &TransformParams{Mode: TransformTranslate, TargetLanguage: &language}
		}
	}
	return params
}

// describeParams 把允许的参数维度翻译成 UI 可用的描述。
func describeParams(spec actionSpec) []ParamOption {
	options := []ParamOption{}
	for _, kind := range spec.params {
		switch kind {
		case paramLength:
			options = append(options, ParamOption{Name: paramNameLength, Values: stringsOf(lengthValues)})
		case paramTone:
			options = append(options, ParamOption{Name: paramNameTone, Values: stringsOf(toneValues)})
		case paramRatio:
			options = append(options, ParamOption{Name: paramNameRatio, Min: minRatio, Max: maxRatio})
		case paramTransform:
			options = append(options,
				ParamOption{Name: paramNameTransformMode, Values: stringsOf(transformModeValues)},
				ParamOption{Name: paramNameTargetLanguage, Values: stringsOf(targetLanguageValues), AppliesTo: string(TransformTranslate)},
				ParamOption{Name: paramNameAudience, Values: stringsOf(audienceValues), AppliesTo: string(TransformExplain)},
			)
		}
	}
	return options
}

// stringsOf 把闭合取值集翻成 UI 描述用的字符串列表。
func stringsOf[T ~string](values []T) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		out = append(out, string(value))
	}
	return out
}
