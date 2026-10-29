package workspacecore

import (
	"reflect"
	"testing"

	lingdoctemplate "github.com/Tencent/WeKnora/internal/lingdoc/template"
)

func TestTemplateMigrationPreviewTreatsBlankRequiredValuesAsMissing(t *testing.T) {
	project := Project{ID: "p", Spec: map[string]string{
		"empty": "", "whitespace": " \t\n", "present": "present",
		"invalid_number": "not-a-number", "number_blank": "",
		"required_by_list": "  ", "optional": "",
	}}
	target := Template{Fields: []lingdoctemplate.Field{
		{ID: "empty", Type: "string", Required: true},
		{ID: "whitespace", Type: "text", Required: true},
		{ID: "present", Type: "string", Required: true},
		{ID: "invalid_number", Type: "number", Required: true},
		{ID: "number_blank", Type: "number", Required: true},
		{ID: "required_by_list", Type: "string"},
		{ID: "absent", Type: "string", Required: true},
		{ID: "optional", Type: "string"},
	}, RequiredFields: []string{"required_by_list"}}

	preview := buildTemplateMigrationPreview(project, target, 1)
	wantMissing := []string{"absent", "empty", "invalid_number", "number_blank", "required_by_list", "whitespace"}
	if !reflect.DeepEqual(preview.MissingRequired, wantMissing) {
		t.Fatalf("missing required = %v, want %v", preview.MissingRequired, wantMissing)
	}
	if !reflect.DeepEqual(preview.Incompatible, []string{"invalid_number"}) {
		t.Fatalf("incompatible = %v", preview.Incompatible)
	}
	statuses := make(map[string]string, len(preview.Fields))
	for _, field := range preview.Fields {
		statuses[field.FieldID] = field.Status
	}
	for id, want := range map[string]string{
		"empty": "missing", "whitespace": "missing", "absent": "missing",
		"number_blank": "missing", "required_by_list": "missing", "present": "preserved",
		"optional": "preserved", "invalid_number": "incompatible",
	} {
		if got := statuses[id]; got != want {
			t.Errorf("%s status = %q, want %q", id, got, want)
		}
	}
}

func TestTemplateNumericValuesAreFiniteAndIntegersAreExact(t *testing.T) {
	for _, tc := range []struct {
		value, kind string
		valid       bool
	}{
		{"1.5", "integer", false},
		{"-2", "integer", true},
		{"0", "integer", true},
		{"1.0", "integer", true},
		{"1e3", "integer", true},
		{"9007199254740992.1", "integer", false},
		{"1e-999999", "integer", false},
		{"0e-999999", "integer", true},
		{"0x1p2", "integer", true},
		{"0x1.8p0", "integer", false},
		{"1.5", "number", true},
		{"NaN", "number", false},
		{"+Inf", "number", false},
		{"NaN", "integer", false},
		{"-Inf", "integer", false},
	} {
		if got := compatibleTemplateValue(tc.value, TemplateField{Type: tc.kind}); got != tc.valid {
			t.Errorf("%s as %s: got %v, want %v", tc.value, tc.kind, got, tc.valid)
		}
	}
}

func TestTemplateOptionalBlankValuesRemainUnset(t *testing.T) {
	for _, kind := range []string{"string", "text", "number", "integer", "boolean", "enum"} {
		for _, value := range []string{"", " \t\n"} {
			if !compatibleTemplateValue(value, TemplateField{Type: kind}) {
				t.Errorf("optional %s blank %q must remain unset", kind, value)
			}
			if compatibleTemplateValue(value, TemplateField{Type: kind, Required: true}) {
				t.Errorf("required %s blank %q must be rejected", kind, value)
			}
		}
	}
}

func TestTemplateUpgradeRejectsRequiredFieldFlagEvenIfListOmitsIt(t *testing.T) {
	definition := Template{Fields: []lingdoctemplate.Field{
		{ID: "existing", Type: "string", Required: true},
		{ID: "new_required", Type: "string", Required: true},
	}, RequiredFields: []string{"existing"}}
	if _, err := templateSpecAfterUpgrade(map[string]string{"existing": "present"}, definition, nil); err == nil {
		t.Fatal("upgrade accepted a missing required field absent from required_fields")
	}
}
