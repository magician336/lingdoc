package workspace

import (
	"context"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/lingdoc/delivery"
	lingdoctemplate "github.com/Tencent/WeKnora/internal/lingdoc/template"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/google/uuid"
)

func ruleSetUseEvent(ctx context.Context, actorID string, input delivery.DeliveryInput, action, resourceID, outcome string) (lingdoctemplate.RuleSetUseAuditEvent, bool, error) {
	binding, err := delivery.VerifiedRuleSetBindingForInput(input)
	if err != nil {
		return lingdoctemplate.RuleSetUseAuditEvent{}, true, err
	}
	if binding == nil {
		return lingdoctemplate.RuleSetUseAuditEvent{}, false, nil
	}
	ref := binding.Reference
	caller := types.CallerFromContext(ctx)
	if caller.UserID == "" {
		caller.UserID = actorID
	}
	if caller.TenantID == 0 || caller.UserID != actorID || !caller.Role.IsValid() || strings.TrimSpace(binding.EffectiveHash) == "" {
		return lingdoctemplate.RuleSetUseAuditEvent{}, true, delivery.ErrInvalidRequest
	}
	templateCopy := lingdoctemplate.Clone(input.Template)
	bindingCopy := binding.Clone()
	return lingdoctemplate.RuleSetUseAuditEvent{
		ID: uuid.NewString(), TenantID: caller.TenantID, ActorID: caller.UserID, ActorRole: string(caller.Role),
		ProjectID: input.ProjectID, Action: action, RuleSetID: ref.ID, RuleSetVersion: ref.Version,
		RuleSetHash: ref.Hash, EffectiveRulesetHash: binding.EffectiveHash,
		TemplateID: input.Template.ID, TemplateVersion: input.Template.Version,
		TemplateCopyID: input.TemplateCopyID, TemplateCopyVersion: input.TemplateCopyVersion,
		ProjectVersion: int64(input.ProjectVersion), ResourceID: resourceID, Outcome: outcome, CreatedAt: time.Now().UTC(),
		VerifiedTemplate: &templateCopy, VerifiedBinding: &bindingCopy,
	}, true, nil
}
