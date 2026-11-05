package workspacecore

import "fmt"

// Only the server-loaded project copy defines valid ChangeSet fields. This
// helper runs in the existing native mutation callback, never in a new read.
// Template upgrades retain their separate full-definition migration path.
func validateChangeTemplateFields(p Project, fields []ChangeFieldDelta) error {
	if len(fields) == 0 {
		return nil
	}
	if p.TemplateCopy == nil || p.TemplateCopy.Status != TemplateCopyBound {
		return ErrInvalidState
	}
	values := make(map[string]string, len(fields))
	for _, field := range fields {
		if _, exists := p.Spec[field.Key]; !exists {
			return fmt.Errorf("%w: changed field %q is absent from the current project spec", ErrInvalidRequest, field.Key)
		}
		if _, duplicate := values[field.Key]; duplicate {
			return fmt.Errorf("%w: duplicate changed field %q", ErrInvalidRequest, field.Key)
		}
		values[field.Key] = field.NewValue
	}
	// Reuse the existing template Spec validator for bound IDs, value types,
	// and required fields. Historic orphan values remain readable, but cannot
	// be changed through an unbound key.
	_, err := templateSpecAfterUpgrade(p.Spec, p.TemplateCopy.Definition, values)
	return err
}

func validateChangeTemplateInput(p Project, fields map[string]ChangeFieldInput) error {
	deltas := make([]ChangeFieldDelta, 0, len(fields))
	for key, field := range fields {
		deltas = append(deltas, ChangeFieldDelta{Key: key, OldValue: field.OldValue, NewValue: field.NewValue})
	}
	return validateChangeTemplateFields(p, deltas)
}
