package membership

import (
	"context"
	"strings"

	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
)

// EffectivePermission reports whether the actor may act on the project with
// the given capability, and why the answer is what it is.
//
// Permission status is visible only after basic project membership has been
// established. A denied stranger receives no object names or version
// metadata.
func (s *Service) EffectivePermission(
	ctx context.Context,
	actor core.Actor,
	projectID, capability string,
) (core.EffectivePermission, error) {
	request, err := core.NewAuthorizationRequestFromCapability(actor, projectID, capability)
	if err != nil {
		return core.EffectivePermission{}, err
	}
	var result core.EffectivePermission
	err = s.repository.Transaction(ctx, core.TransactionOptions{ReadOnly: true}, func(tx core.Transaction) error {
		p, err := s.authorizer.AuthorizeProject(
			ctx, tx, core.NewAuthorizationRequest(actor, projectID, core.AuthorizationRead, ""),
		)
		if err != nil {
			return err
		}
		result = core.EffectivePermission{
			Capability: capability, ProjectVersion: p.ProjectVersion,
			ContextRevision: p.CurrentContextRevision,
		}
		_, result.Target, _ = strings.Cut(capability, ":")
		_, err = s.authorizer.AuthorizeProject(ctx, tx, request)
		if err == nil {
			result.Allowed = true
			result.ReasonCode = "allowed"
			return nil
		}
		result.ReasonCode = "capability_or_scope_denied"
		return nil
	})
	return result, err
}
