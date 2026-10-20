package workspacecore

import "github.com/Tencent/WeKnora/internal/clienterror"

// ErrRuleSetStateNotAllowed is the failure of a rule set whose current state
// does not allow the requested operation.
//
// The form is decided by the situation, not by the error alone: a rule set
// state is not a project state, so it answers 409 invalid_state instead of
// the project state's 422 invalid_state. The rule governance surface and the
// rule set storage surface answer the same failure the same way.
//
// It wraps ErrInvalidState, so every existing check for the project state
// failure keeps finding it, and it states its own outward form so the
// transport does not have to know which kind of state blocked the operation.
var ErrRuleSetStateNotAllowed = clienterror.Wrap(
	clienterror.RuleSetStateNotAllowed,
	"rule set state not allowed",
	ErrInvalidState)
