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
			result.ReasonCode = core.ReasonAllowed
			return nil
		}
		// The Owner may be unable to act at all, and then the Owner-only
		// entries are frozen. The refusal says so, so the caller can tell a
		// frozen project from a request it was never allowed to make.
		//
		// Only an Owner-only capability may be reported as frozen. A refusal
		// of any other capability is about the actor's own authority, and
		// calling it a freeze would swallow the existing
		// "capability_or_scope_denied" meaning.
		//
		// The capability may carry a "base:target" suffix; the base is the
		// capability the authorization asks for, so the base is what decides
		// whether the entry is an Owner-only one.
		base, _, _ := strings.Cut(capability, ":")
		if core.OwnerOnlyCapability(base) {
			if authority := core.OwnerAuthorityOf(tx, p, actor.TenantID); authority.OwnerUserID != "" && !authority.Available {
				result.ReasonCode = core.ReasonOwnerUnavailable
				return nil
			}
		}
		result.ReasonCode = "capability_or_scope_denied"
		return nil
	})
	return result, err
}

// OwnerAvailability reports the derived fact about the project's Owner.
//
// It is the project level read outlet: the project page and the members page
// use it to say what is going on with the Owner and how the project gets back
// to work. It reads the same judgement the permission read outlet reads, so
// the two answers cannot disagree about the same project.
func (s *Service) OwnerAvailability(
	ctx context.Context,
	actor core.Actor,
	projectID string,
) (core.OwnerAvailability, error) {
	var result core.OwnerAvailability
	err := s.repository.Transaction(ctx, core.TransactionOptions{ReadOnly: true}, func(tx core.Transaction) error {
		p, err := s.authorizer.AuthorizeProject(
			ctx, tx, core.NewAuthorizationRequest(actor, projectID, core.AuthorizationRead, ""),
		)
		if err != nil {
			return err
		}
		result = core.OwnerAvailabilityOf(tx, p, actor.TenantID)
		return nil
	})
	return result, err
}
