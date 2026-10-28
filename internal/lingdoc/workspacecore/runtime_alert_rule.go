package workspacecore

import (
	"context"
	"encoding/json"
	"slices"
	"strings"

	"github.com/google/uuid"
)

// G6 告警规则的可读回与可停用（#382-A / #382-B）。
//
// 规则此前只有「新建」这一条路：SaveRuntimeAlertRule 每次都换一个新的
// ID 并纯插入，所以既读不回来，也停不掉。本文件补上读回与处置。
//
// 处置动作叫 operation，不叫 action：规则自己的 Action 是**处置动作**
// （notify / pause / rollback …），两个「动作」如果同名，语义会撞车。

const (
	// runtimeAlertRuleActive is a rule that still reacts to runtime events.
	runtimeAlertRuleActive = "ACTIVE"
	// runtimeAlertRuleDisabled is a rule that no longer reacts to runtime
	// events. It stays in the list, with its history, so that the operator
	// can see what was turned off and turn it on again.
	runtimeAlertRuleDisabled = "DISABLED"
)

// RuntimeAlertRuleActionInput is one operator decision about an alert rule.
//
// The fields that are not sent keep their current value. That is why the
// scope is a pointer and the disposition is not the rule's own action field:
// an absent field and an empty field mean the same thing here — "leave it
// alone".
type RuntimeAlertRuleActionInput struct {
	ExpectedVersion int64         `json:"expected_version"`
	Operation       string        `json:"operation"`
	Reason          string        `json:"reason"`
	Signal          string        `json:"signal,omitempty"`
	Severity        string        `json:"severity,omitempty"`
	Disposition     string        `json:"disposition,omitempty"`
	ResponsibleID   string        `json:"responsible_id,omitempty"`
	Scope           *RuntimeScope `json:"scope,omitempty"`
}

// newRuntimeAlertRuleID mints the identifier of a freshly created rule.
func newRuntimeAlertRuleID() string { return uuid.NewString() }

// validRuntimeAlertSignal answers whether a signal names a runtime event.
func validRuntimeAlertSignal(signal string) bool { return runtimeSafeCode.MatchString(signal) }

// validRuntimeAlertSeverity answers whether a severity is one of the four
// levels the runtime knows.
func validRuntimeAlertSeverity(severity string) bool {
	return slices.Contains([]string{"P0", "P1", "P2", "P3"}, severity)
}

// validRuntimeAlertDisposition answers whether a disposition is one of the
// actions a rule may ask for.
//
// The list lives here, once: SaveRuntimeAlertRule and the update path below
// must not disagree about what a legal rule is.
func validRuntimeAlertDisposition(disposition string) bool {
	return slices.Contains([]string{"notify", "retry", "pause", "block_export", "rollback", "human_confirm"}, disposition)
}

// resaveRuntimeAlertRule applies a save onto the rule that already holds the
// same signal and scope.
//
// The fields the save carries are the rule's configuration; the rule's status
// is not one of them. A rule an operator turned off stays off: re-arming is
// the `enable` decision, with its own reason and its own audit row. A save
// that carries exactly what is stored writes nothing at all, so pressing save
// twice does not manufacture a version either.
func resaveRuntimeAlertRule(tx Transaction, actor Actor, store runtimeTransaction, p Project, existing RuntimeAlertRule, recordKey string, version int64, input RuntimeAlertRule) (operationResult[RuntimeAlertRule], error) {
	if existing.Severity == input.Severity && existing.Action == input.Action && existing.ResponsibleID == input.ResponsibleID {
		return operationResult[RuntimeAlertRule]{Value: existing, Status: operationStatusOK}, nil
	}
	next := existing
	next.Severity = input.Severity
	next.Action = input.Action
	next.ResponsibleID = input.ResponsibleID
	next.Version = version + 1
	if err := store.CompareAndSwapRuntimeRecord(actor.TenantID, p.ID, "alert_rule", recordKey, version, next); err != nil {
		return operationResult[RuntimeAlertRule]{}, err
	}
	if err := recordAuditEvent(tx, actor, p.ID, "runtime.alert_rule.update", recordKey, map[string]any{
		"operation": "save", "signal": next.Signal, "severity": next.Severity,
		"action": next.Action, "responsible_id": next.ResponsibleID, "version": next.Version,
	}); err != nil {
		return operationResult[RuntimeAlertRule]{}, err
	}
	return operationResult[RuntimeAlertRule]{Value: next, Status: operationStatusOK}, nil
}

// validRuntimeAlertRule answers whether a whole rule is legal.
func validRuntimeAlertRule(rule RuntimeAlertRule) bool {
	return validRuntimeAlertSignal(rule.Signal) && rule.ResponsibleID != "" &&
		validRuntimeAlertSeverity(rule.Severity) && validRuntimeAlertDisposition(rule.Action)
}

// ListRuntimeAlertRules reads the alert rules of one project back.
//
// It is the read side of the same rule list the runtime matches against, so
// an operator can see which rules are armed, which are off, and which
// version they are looking at.
func (s *Service) ListRuntimeAlertRules(ctx context.Context, actor Actor, projectID string) ([]RuntimeAlertRule, error) {
	var result []RuntimeAlertRule
	ctx, emit := s.withRuntimeObservations(ctx)
	defer emit()
	err := s.repository.Transaction(ctx, TransactionOptions{ReadOnly: true}, func(tx Transaction) error {
		if _, err := s.authorizeProject(ctx, tx, authorizationRequest(actor, projectID, AuthorizationRead, "")); err != nil {
			return err
		}
		store, ok := tx.(runtimeTransaction)
		if !ok {
			return ErrInvalidState
		}
		rows, err := store.ListRuntimeRecords(actor.TenantID, projectID, "alert_rule")
		if err != nil {
			return err
		}
		result = []RuntimeAlertRule{}
		for _, row := range rows {
			rule, err := runtimeAlertRuleFromRow(row)
			if err != nil {
				return err
			}
			result = append(result, rule)
		}
		return nil
	})
	return result, err
}

// runtimeAlertRuleFromRow decodes one stored rule.
//
// The stored model grows, so a record written before Version/Status existed
// reads as an armed rule at the version the store holds for it. The decode
// lives here once: every read path must agree on what a stored row means.
func runtimeAlertRuleFromRow(row runtimeRecordRow) (RuntimeAlertRule, error) {
	var rule RuntimeAlertRule
	if err := json.Unmarshal([]byte(row.BodyJSON), &rule); err != nil {
		return RuntimeAlertRule{}, err
	}
	if rule.Version == 0 {
		rule.Version = row.Version
	}
	if rule.Status == "" {
		rule.Status = runtimeAlertRuleActive
	}
	if rule.ID == "" {
		// The record key is the identifier every other path uses, including
		// the actions route, so a rule read without one still names itself.
		rule.ID = row.RecordKey
	}
	return rule, nil
}

// sameRuntimeAlertRuleIdentity reports whether a stored rule holds the given
// signal and scope.
//
// A rule is identified by its signal and its scope: a project arms one rule
// per pair. Severity, disposition and the responsible member are fields of
// that rule, not part of what makes it a different rule (#470).
func sameRuntimeAlertRuleIdentity(rule RuntimeAlertRule, signal string, scope RuntimeScope) bool {
	return rule.Signal == signal && rule.Scope == scope
}

// findRuntimeAlertRuleByIdentity reads the rule a save is about: the one that
// already holds this signal and scope, under whatever record key it was
// created with.
func findRuntimeAlertRuleByIdentity(store runtimeTransaction, tenantID uint64, projectID, signal string, scope RuntimeScope) (RuntimeAlertRule, string, int64, error) {
	rows, err := store.ListRuntimeRecords(tenantID, projectID, "alert_rule")
	if err != nil {
		return RuntimeAlertRule{}, "", 0, err
	}
	for _, row := range rows {
		rule, err := runtimeAlertRuleFromRow(row)
		if err != nil {
			return RuntimeAlertRule{}, "", 0, err
		}
		if sameRuntimeAlertRuleIdentity(rule, signal, scope) {
			return rule, row.RecordKey, row.Version, nil
		}
	}
	return RuntimeAlertRule{}, "", 0, ErrNotFound
}

// findRuntimeAlertRule reads one rule by its record key, together with the
// version the store holds for it.
func findRuntimeAlertRule(store runtimeTransaction, tenantID uint64, projectID, ruleID string) (RuntimeAlertRule, int64, error) {
	rows, err := store.ListRuntimeRecords(tenantID, projectID, "alert_rule")
	if err != nil {
		return RuntimeAlertRule{}, 0, err
	}
	for _, row := range rows {
		if row.RecordKey != ruleID {
			continue
		}
		rule, err := runtimeAlertRuleFromRow(row)
		if err != nil {
			return RuntimeAlertRule{}, 0, err
		}
		return rule, row.Version, nil
	}
	return RuntimeAlertRule{}, 0, ErrNotFound
}

// ActOnRuntimeAlertRule turns one alert rule off, back on, or changes it.
//
// The rule must belong to the project it is acted on: a rule of another
// project is not found, not forbidden, so the answer does not leak that it
// exists.
func (s *Service) ActOnRuntimeAlertRule(ctx context.Context, actor Actor, projectID, ruleID, key string, input RuntimeAlertRuleActionInput) (json.RawMessage, int, bool, error) {
	if err := RequireHumanDecision(ctx); err != nil {
		return nil, 0, false, err
	}
	if !slices.Contains([]string{"disable", "enable", "update"}, input.Operation) ||
		strings.TrimSpace(input.Reason) == "" || input.ExpectedVersion < 0 || ruleID == "" {
		return nil, 0, false, ErrInvalidRequest
	}
	return runOperation(s, ctx, actor, operationInput(operationRuntimeAlertRuleAction, projectID+"/"+ruleID, key, input, authorizationRequest(actor, projectID, AuthorizationManage, "")), mutationPort[Transaction], func(tx Transaction, p Project) (operationResult[RuntimeAlertRule], error) {
		store, ok := tx.(runtimeTransaction)
		if !ok {
			return operationResult[RuntimeAlertRule]{}, ErrInvalidState
		}
		rule, version, err := findRuntimeAlertRule(store, actor.TenantID, p.ID, ruleID)
		if err != nil {
			return operationResult[RuntimeAlertRule]{}, err
		}
		if version != input.ExpectedVersion {
			return operationResult[RuntimeAlertRule]{}, ErrVersionConflict
		}
		next := rule
		switch input.Operation {
		case "disable":
			if rule.Status == runtimeAlertRuleDisabled {
				return operationResult[RuntimeAlertRule]{}, ErrInvalidState
			}
			next.Status = runtimeAlertRuleDisabled
		case "enable":
			if rule.Status != runtimeAlertRuleDisabled {
				return operationResult[RuntimeAlertRule]{}, ErrInvalidState
			}
			next.Status = runtimeAlertRuleActive
		case "update":
			if input.Signal != "" {
				next.Signal = input.Signal
			}
			if input.Severity != "" {
				next.Severity = input.Severity
			}
			if input.Disposition != "" {
				next.Action = input.Disposition
			}
			if input.ResponsibleID != "" {
				next.ResponsibleID = input.ResponsibleID
			}
			if input.Scope != nil {
				next.Scope = *input.Scope
			}
			if !validRuntimeAlertRule(next) {
				return operationResult[RuntimeAlertRule]{}, ErrInvalidRequest
			}
			if !runtimeScopeMatches(next.Scope, actor, p, s.runtimeMode) {
				return operationResult[RuntimeAlertRule]{}, ErrNotFound
			}
			// The responsible member has to be able to act on the project,
			// exactly like when the rule was created. A rule whose
			// responsible member lost the right is not silently re-pointed
			// at somebody else.
			recipient := actor
			recipient.UserID = next.ResponsibleID
			if _, err := s.authorizeProject(ctx, tx, authorizationRequest(recipient, p.ID, AuthorizationManage, "")); err != nil {
				return operationResult[RuntimeAlertRule]{}, err
			}
		}
		next.Version = version + 1
		if err := store.CompareAndSwapRuntimeRecord(actor.TenantID, p.ID, "alert_rule", ruleID, version, next); err != nil {
			return operationResult[RuntimeAlertRule]{}, err
		}
		if err := recordAuditEvent(tx, actor, p.ID, "runtime.alert_rule."+input.Operation, ruleID, map[string]any{
			"operation": input.Operation, "signal": next.Signal, "severity": next.Severity,
			"disposition": next.Action, "status": next.Status, "version": next.Version,
		}); err != nil {
			return operationResult[RuntimeAlertRule]{}, err
		}
		return operationResult[RuntimeAlertRule]{Value: next, Status: operationStatusOK}, nil
	})
}
