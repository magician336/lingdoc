package templategovernance

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/lingdoc/rulesetstore"
	"github.com/Tencent/WeKnora/internal/lingdoc/template"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

const RuleSuggestionPromptVersion = "lingdoc-rule-suggestion/2"

func (s *Service) checkSourcesInTransaction(ctx context.Context, tx *gorm.DB, actor Actor, refs []SourceRef) error {
	if len(refs) == 0 {
		return nil
	}
	if s.ports.Sources == nil {
		return ErrSourceUnavailable
	}
	verifier, ok := s.ports.Sources.(interface {
		VerifyTemplateSourcesInTransaction(context.Context, *gorm.DB, Actor, []SourceRef) error
	})
	if !ok {
		return ErrSourceUnavailable
	}
	return verifier.VerifyTemplateSourcesInTransaction(ctx, tx, actor, refs)
}
func (s *Service) checkSuggestionBasis(ctx context.Context, tx *gorm.DB, actor Actor, scope SuggestionScope) (TemplateVersion, error) {
	if err := s.checkSourcesInTransaction(ctx, tx, actor, scope.Sources); err != nil {
		return TemplateVersion{}, err
	}
	actual, err := s.ports.Authorizer.CurrentProjectBasis(ctx, tx, actor, scope.ProjectID)
	if err != nil {
		return TemplateVersion{}, err
	}
	if !reflect.DeepEqual(actual, scope.Basis) || actual.TemplateID != scope.TemplateID || actual.TemplateVersion != scope.TemplateVersion {
		return TemplateVersion{}, ErrStale
	}
	v, err := loadVersion(tx, actor, scope.TemplateID, scope.TemplateVersion)
	if err != nil {
		return v, err
	}
	if v.Revision != scope.CatalogRevision || v.RuleSet != scope.RuleSet || v.State != template.RuleSetPublished {
		return TemplateVersion{}, ErrStale
	}
	if _, err := s.bindableRuleSet(ctx, tx, actor, scope.RuleSet); err != nil {
		return TemplateVersion{}, ErrStale
	}
	return v, nil
}
func validSuggestionScope(scope SuggestionScope) bool {
	if strings.TrimSpace(scope.ProjectID) == "" || scope.Basis.ProjectVersion < 1 || scope.CatalogRevision < 1 || !identifier.MatchString(scope.TemplateID) || !identifier.MatchString(scope.TemplateVersion) || !validSourceRefs(scope.Sources) {
		return false
	}
	return (scope.TargetKind == "field" || scope.TargetKind == "chapter") && identifier.MatchString(scope.TargetID)
}
func (s *Service) RequestRuleSuggestion(ctx context.Context, actor Actor, key string, scope SuggestionScope) (SuggestionRun, error) {
	if err := s.ready(actor); err != nil {
		return SuggestionRun{}, err
	}
	if !validKey(key) || !validSuggestionScope(scope) {
		return SuggestionRun{}, ErrInvalidRequest
	}
	var result SuggestionRun
	var version TemplateVersion
	replayed := false
	err := s.withTx(ctx, func(tx *gorm.DB) error {
		if err := s.ports.Authorizer.AuthorizeSuggestion(ctx, tx, actor, ActionSuggest, scope); err != nil {
			return err
		}
		var err error
		version, err = s.checkSuggestionBasis(ctx, tx, actor, scope)
		if err != nil {
			return err
		}
		op := operationIdentity(actor, "request_suggestion", scope.ProjectID, key)
		found, err := operationGet(tx, op, scope, &result)
		if err != nil {
			return err
		}
		if found {
			result, err = loadSuggestion(tx, actor, result.ID)
			if err != nil {
				return err
			}
			if result.Scope.ProjectID != scope.ProjectID {
				return ErrInvalidState
			}
			replayed = true
			return nil
		}
		if !targetExists(version.Definition, scope.TargetKind, scope.TargetID) {
			return ErrInvalidRequest
		}
		now := time.Now().UTC()
		result = SuggestionRun{ID: uuid.NewString(), ProjectID: scope.ProjectID, ActorID: actor.UserID, Revision: 1, Status: "running", Scope: scope, PromptVersion: RuleSuggestionPromptVersion, ToolNames: []string{}, CreatedAt: now, UpdatedAt: now}
		if err := saveSuggestion(tx, actor, SuggestionRun{}, result); err != nil {
			return err
		}
		if err := recordAudit(tx, actor, ActionSuggest, result.ID, "explicit rule suggestion request", map[string]any{"scope": scope, "status": result.Status, "prompt_version": result.PromptVersion}); err != nil {
			return err
		}
		return operationSave(tx, op, scope, result, scope.Basis)
	})
	if err != nil || replayed {
		return result, err
	}

	start := time.Now()
	var output RuleSuggestionOutput
	var modelErr error
	var resolved AuthorizedContext
	if s.ports.Context == nil {
		modelErr = ErrSourceUnavailable
	} else {
		resolved, modelErr = s.ports.Context.Build(ctx, actor, scope)
		if modelErr == nil {
			modelErr = validateAuthorizedContext(scope, resolved)
		}
	}
	// The context builder may perform network/source IO. Revalidate the exact
	// project and catalog basis before making any external model call.
	if modelErr == nil {
		modelErr = s.withTx(ctx, func(tx *gorm.DB) error {
			if err := s.ports.Authorizer.AuthorizeSuggestion(ctx, tx, actor, ActionSuggest, scope); err != nil {
				return err
			}
			_, err := s.checkSuggestionBasis(ctx, tx, actor, scope)
			return err
		})
	}
	if modelErr == nil {
		if s.ports.Model == nil {
			modelErr = ErrModelUnavailable
		} else {
			output, modelErr = s.ports.Model.Suggest(ctx, actor, RuleSuggestionRequest{Scope: scope, Template: template.Clone(version.Definition), RuleSet: version.RuleSetSnapshot, Context: resolved, PromptVersion: RuleSuggestionPromptVersion, CorrelationID: result.ID})
		}
	}
	var payload *SuggestionPayload
	if modelErr == nil {
		if output.ModelID == "" || (output.RunMode != "real" && output.RunMode != "mock" && output.RunMode != "real_api_fake_model") || output.InputTokens < 0 || output.OutputTokens < 0 {
			modelErr = ErrInvalidOutput
		} else {
			parsed, err := parseSuggestion(output.StructuredOutput, result.ID, actor, version, scope)
			modelErr = err
			if err == nil {
				payload = &parsed
			}
		}
	}
	elapsed := time.Since(start).Milliseconds()
	// A timed-out model call can leave ctx cancelled; use a bounded independent
	// ledger context so the failure is durable, without retrying the model.
	ledgerCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	var denial error
	err = s.withTx(ledgerCtx, func(tx *gorm.DB) error {
		current, err := loadSuggestion(tx, actor, result.ID)
		if err != nil {
			return err
		}
		if current.Status != "running" {
			result = current
			return nil
		}
		next := current
		next.Revision++
		next.UpdatedAt = time.Now().UTC()
		next.DurationMS = elapsed
		next.ModelID = output.ModelID
		next.RunMode = output.RunMode
		next.ToolNames = append([]string{}, output.ToolNames...)
		next.InputTokens = output.InputTokens
		next.OutputTokens = output.OutputTokens
		authErr := s.ports.Authorizer.AuthorizeSuggestion(ledgerCtx, tx, actor, ActionSuggest, scope)
		if authErr != nil {
			denial = authErr
			next.Status = "failed"
			next.ErrorCode = "authorization_changed"
			next.ErrorMessage = "authorization changed while model work was running"
		} else if _, err := s.checkSuggestionBasis(ledgerCtx, tx, actor, scope); err != nil {
			next.Status = "stale"
			next.ErrorCode = "basis_changed"
			next.ErrorMessage = "the pinned project, template or ruleset changed"
		} else if modelErr != nil {
			next.Status = "failed"
			next.ErrorCode = suggestionErrorCode(modelErr)
			next.ErrorMessage = boundedError(modelErr)
		} else {
			next.Status = "suggestion"
			next.Output = payload
		}
		if err := saveSuggestion(tx, actor, current, next); err != nil {
			return err
		}
		if err := recordAudit(tx, actor, ActionSuggest, next.ID, "rule suggestion completed", map[string]any{"status": next.Status, "scope": scope, "model_id": next.ModelID, "run_mode": next.RunMode, "prompt_version": next.PromptVersion, "tool_names": next.ToolNames, "input_tokens": next.InputTokens, "output_tokens": next.OutputTokens, "duration_ms": next.DurationMS, "error_code": next.ErrorCode}); err != nil {
			return err
		}
		result = next
		return nil
	})
	if err != nil {
		return SuggestionRun{}, err
	}
	if denial != nil {
		return SuggestionRun{}, denial
	}
	return result, nil
}
func validateAuthorizedContext(scope SuggestionScope, resolved AuthorizedContext) error {
	if resolved.ProjectID != scope.ProjectID || !reflect.DeepEqual(resolved.Basis, scope.Basis) || resolved.AuthorizationState != "allowed" {
		return ErrSourceUnavailable
	}
	expected := map[string]SourceRef{}
	for _, ref := range scope.Sources {
		expected[ref.ID] = ref
	}
	if len(resolved.Sources) != len(expected) {
		return ErrSourceUnavailable
	}
	seen := map[string]bool{}
	for _, source := range resolved.Sources {
		ref, ok := expected[source.ID]
		if !ok || seen[source.ID] || source.Version != ref.Version || !source.ModelUseAllowed || source.QuotedTextHash == "" {
			return ErrSourceUnavailable
		}
		hash := shaText(source.QuotedText)
		if source.QuotedTextHash != hash {
			return ErrSourceUnavailable
		}
		seen[source.ID] = true
	}
	return nil
}
func shaText(text string) string {
	// hashJSON deliberately isn't used here: G2's quote hash covers UTF-8 bytes.
	return digestText(text)
}
func targetExists(t template.Template, kind, id string) bool {
	if kind == "field" {
		for _, f := range t.Fields {
			if f.ID == id {
				return true
			}
		}
		for _, f := range t.RequiredFields {
			if f == id {
				return true
			}
		}
	}
	if kind == "chapter" {
		for _, section := range t.Sections {
			if section.ID == id {
				return true
			}
		}
	}
	return false
}
func parseSuggestion(raw json.RawMessage, runID string, actor Actor, v TemplateVersion, scope SuggestionScope) (SuggestionPayload, error) {
	if len(raw) == 0 || len(raw) > 256*1024 {
		return SuggestionPayload{}, ErrInvalidOutput
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var payload SuggestionPayload
	if err := decoder.Decode(&payload); err != nil {
		return payload, fmt.Errorf("%w: %v", ErrInvalidOutput, err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return payload, ErrInvalidOutput
	}
	if len(payload.Proposals) == 0 || len(payload.Proposals) > 20 || strings.TrimSpace(payload.Explanation) == "" {
		return payload, ErrInvalidOutput
	}
	allowed := map[string]bool{}
	for _, ref := range scope.Sources {
		allowed[ref.ID] = true
	}
	phantom := v.RuleSetSnapshot
	phantom.State = template.RuleSetDraft
	phantom.RulesetHash = ""
	phantom.Bindings = append([]template.RuleBinding{}, phantom.Bindings...)
	for i := range payload.Proposals {
		proposal := &payload.Proposals[i]
		if proposal.RuleID != "" || proposal.TargetKind != scope.TargetKind || proposal.TargetID != scope.TargetID || strings.TrimSpace(proposal.Rationale) == "" || !targetExists(v.Definition, proposal.TargetKind, proposal.TargetID) {
			return payload, ErrInvalidOutput
		}
		for _, id := range proposal.EvidenceSourceIDs {
			if !allowed[id] {
				return payload, ErrInvalidOutput
			}
		}
		proposal.RuleID = fmt.Sprintf("suggest-%s-%d", strings.ReplaceAll(runID, "-", "")[:20], i+1)
		phantom.Bindings = append(phantom.Bindings, template.RuleBinding{RuleID: proposal.RuleID, DefinitionID: proposal.DefinitionID, DefinitionVersion: proposal.DefinitionVersion, Origin: "project", TargetKind: proposal.TargetKind, TargetID: proposal.TargetID, Parameters: proposal.Parameters, Severity: template.SeverityInfo, Message: proposal.Message, TriggerScenes: []string{"write_check", "preflight"}, Enabled: true, Owner: actor.UserID})
	}
	if err := template.ValidateRuleSet(phantom); err != nil {
		return payload, fmt.Errorf("%w: %v", ErrInvalidOutput, err)
	}
	return payload, nil
}
func suggestionErrorCode(err error) string {
	switch {
	case errors.Is(err, ErrModelUnavailable):
		return "model_unavailable"
	case errors.Is(err, ErrInvalidOutput):
		return "invalid_model_output"
	case errors.Is(err, ErrSourceUnavailable):
		return "source_access_denied"
	case errors.Is(err, ErrStale):
		return "basis_changed"
	case errors.Is(err, context.DeadlineExceeded):
		return "model_timeout"
	case errors.Is(err, context.Canceled):
		return "model_cancelled"
	default:
		return "model_failed"
	}
}
func boundedError(err error) string {
	// Don't persist arbitrary provider text, which may contain prompts or secrets.
	return suggestionErrorCode(err)
}
func (s *Service) GetRuleSuggestion(ctx context.Context, actor Actor, projectID, runID string) (SuggestionRun, error) {
	if err := s.ready(actor); err != nil {
		return SuggestionRun{}, err
	}
	var result SuggestionRun
	err := s.withTx(ctx, func(tx *gorm.DB) error {
		current, err := loadSuggestion(tx, actor, runID)
		if err != nil {
			return err
		}
		if current.ProjectID != projectID {
			return ErrNotFound
		}
		if err := s.ports.Authorizer.AuthorizeSuggestion(ctx, tx, actor, ActionRead, current.Scope); err != nil {
			return err
		}
		result = current
		if current.Status == "running" || current.Status == "suggestion" {
			if _, err := s.checkSuggestionBasis(ctx, tx, actor, current.Scope); err != nil {
				if !errors.Is(err, ErrStale) {
					return err
				}
				result.Revision++
				result.Status = "stale"
				result.ErrorCode = "basis_changed"
				result.ErrorMessage = "the pinned project, template or ruleset changed"
				result.UpdatedAt = time.Now().UTC()
				if err := saveSuggestion(tx, actor, current, result); err != nil {
					return err
				}
				return recordAudit(tx, actor, ActionSuggest, runID, "pinned context changed", map[string]any{"status": "stale", "scope": current.Scope})
			}
		}
		return nil
	})
	return result, err
}
func (s *Service) ListRuleSuggestions(ctx context.Context, actor Actor, projectID string) ([]SuggestionRun, error) {
	if err := s.ready(actor); err != nil {
		return nil, err
	}
	var ids []string
	err := s.withTx(ctx, func(tx *gorm.DB) error {
		// Project authorization happens even for an empty result set.
		if err := s.ports.Authorizer.AuthorizeSuggestion(ctx, tx, actor, ActionRead, SuggestionScope{ProjectID: projectID}); err != nil {
			return err
		}
		return tx.Model(&suggestionRow{}).Where("tenant_id=? AND project_id=?", actor.TenantID, projectID).Order("created_at DESC,id").Pluck("id", &ids).Error
	})
	if err != nil {
		return nil, err
	}
	result := make([]SuggestionRun, 0, len(ids))
	for _, id := range ids {
		run, err := s.GetRuleSuggestion(ctx, actor, projectID, id)
		if err != nil {
			return nil, err
		}
		result = append(result, run)
	}
	return result, nil
}
func (s *Service) AcceptRuleSuggestion(ctx context.Context, actor Actor, projectID, runID, key string, input AcceptSuggestionInput) (SuggestionRun, error) {
	if err := s.ready(actor); err != nil {
		return SuggestionRun{}, err
	}
	if !validKey(key) || input.ExpectedRevision < 1 || !validReason(input.Reason) || !identifier.MatchString(input.RuleSetID) || !identifier.MatchString(input.Version) || len(input.Bindings) == 0 || s.ports.RuleSets == nil {
		return SuggestionRun{}, ErrInvalidRequest
	}
	var result SuggestionRun
	err := s.withTx(ctx, func(tx *gorm.DB) error {
		current, err := loadSuggestion(tx, actor, runID)
		if err != nil {
			return err
		}
		if current.ProjectID != projectID {
			return ErrNotFound
		}
		if err := s.ports.Authorizer.AuthorizeSuggestion(ctx, tx, actor, ActionAcceptSuggestion, current.Scope); err != nil {
			return err
		}
		version, err := s.checkSuggestionBasis(ctx, tx, actor, current.Scope)
		if err != nil {
			return err
		}
		op := operationIdentity(actor, "accept_suggestion", runID, key)
		found, err := operationGet(tx, op, input, &result)
		if err != nil {
			return err
		}
		if found {
			if current.Revision != result.Revision || current.Status != "accepted" {
				return ErrVersionConflict
			}
			return nil
		}
		if current.Status != "suggestion" || current.Output == nil {
			return ErrInvalidState
		}
		if current.Revision != input.ExpectedRevision {
			return ErrVersionConflict
		}
		if input.RuleSetID != current.Scope.RuleSet.ID || input.Version == current.Scope.RuleSet.Version {
			return ErrInvalidRequest
		}
		allowed := map[string]ProposedRule{}
		for _, proposal := range current.Output.Proposals {
			allowed[proposal.RuleID] = proposal
		}
		draft := version.RuleSetSnapshot
		draft.ID = input.RuleSetID
		draft.Version = input.Version
		draft.State = template.RuleSetDraft
		draft.RulesetHash = ""
		draft.Bindings = append([]template.RuleBinding{}, draft.Bindings...)
		seen := map[string]bool{}
		for _, binding := range input.Bindings {
			proposal, ok := allowed[binding.RuleID]
			if !ok || seen[binding.RuleID] || binding.TargetKind != proposal.TargetKind || binding.TargetID != proposal.TargetID || (binding.Severity != template.SeverityInfo && binding.Severity != template.SeverityWarning) {
				return ErrInvalidRequest
			}
			seen[binding.RuleID] = true
			binding.Origin = "project"
			binding.Owner = actor.UserID
			draft.Bindings = append(draft.Bindings, binding)
		}
		if err := template.ValidateRuleSet(draft); err != nil {
			return err
		}
		if err := rulesetstore.NewStore(tx, s.ports.RuleSets).CreateDraft(ctx, actor, draft); err != nil {
			return err
		}
		result = current
		result.Revision++
		result.Status = "accepted"
		result.DecisionReason = input.Reason
		result.DraftRuleSet = &RuleSetRef{ID: draft.ID, Version: draft.Version}
		result.UpdatedAt = time.Now().UTC()
		if err := saveSuggestion(tx, actor, current, result); err != nil {
			return err
		}
		if err := recordAudit(tx, actor, ActionAcceptSuggestion, runID, input.Reason, map[string]any{"scope": current.Scope, "draft_ruleset": result.DraftRuleSet, "human_bindings": input.Bindings}); err != nil {
			return err
		}
		return operationSave(tx, op, input, result, current.Scope.Basis)
	})
	return result, err
}
func (s *Service) RejectRuleSuggestion(ctx context.Context, actor Actor, projectID, runID, key string, input RejectSuggestionInput) (SuggestionRun, error) {
	if err := s.ready(actor); err != nil {
		return SuggestionRun{}, err
	}
	if !validKey(key) || input.ExpectedRevision < 1 || !validReason(input.Reason) {
		return SuggestionRun{}, ErrInvalidRequest
	}
	var result SuggestionRun
	err := s.withTx(ctx, func(tx *gorm.DB) error {
		current, err := loadSuggestion(tx, actor, runID)
		if err != nil {
			return err
		}
		if current.ProjectID != projectID {
			return ErrNotFound
		}
		if err := s.ports.Authorizer.AuthorizeSuggestion(ctx, tx, actor, ActionRejectSuggestion, current.Scope); err != nil {
			return err
		}
		op := operationIdentity(actor, "reject_suggestion", runID, key)
		found, err := operationGet(tx, op, input, &result)
		if err != nil {
			return err
		}
		if found {
			if current.Revision != result.Revision || current.Status != "rejected" {
				return ErrVersionConflict
			}
			return nil
		}
		if current.Status != "suggestion" && current.Status != "stale" && current.Status != "failed" {
			return ErrInvalidState
		}
		if current.Revision != input.ExpectedRevision {
			return ErrVersionConflict
		}
		result = current
		result.Revision++
		result.Status = "rejected"
		result.DecisionReason = input.Reason
		result.UpdatedAt = time.Now().UTC()
		if err := saveSuggestion(tx, actor, current, result); err != nil {
			return err
		}
		if err := recordAudit(tx, actor, ActionRejectSuggestion, runID, input.Reason, map[string]any{"scope": current.Scope, "prior_status": current.Status}); err != nil {
			return err
		}
		return operationSave(tx, op, input, result, current.Scope.Basis)
	})
	return result, err
}
func loadSuggestion(tx *gorm.DB, actor Actor, id string) (SuggestionRun, error) {
	var row suggestionRow
	if err := tx.Where("tenant_id=? AND id=?", actor.TenantID, id).First(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return SuggestionRun{}, ErrNotFound
		}
		return SuggestionRun{}, err
	}
	return decodeSuggestion(row)
}
func decodeSuggestion(row suggestionRow) (SuggestionRun, error) {
	r := SuggestionRun{ID: row.ID, ProjectID: row.ProjectID, ActorID: row.ActorID, Revision: row.Revision, Status: row.Status, ModelID: row.ModelID, RunMode: row.RunMode, PromptVersion: row.PromptVersion, InputTokens: row.InputTokens, OutputTokens: row.OutputTokens, DurationMS: row.DurationMS, ErrorCode: row.ErrorCode, ErrorMessage: row.ErrorMessage, DecisionReason: row.DecisionReason, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt}
	if err := json.Unmarshal([]byte(row.ScopeJSON), &r.Scope); err != nil {
		return r, err
	}
	if err := json.Unmarshal([]byte(row.OutputJSON), &r.Output); err != nil {
		return r, err
	}
	if err := json.Unmarshal([]byte(row.ToolNamesJSON), &r.ToolNames); err != nil {
		return r, err
	}
	if err := json.Unmarshal([]byte(row.DraftRulesetJSON), &r.DraftRuleSet); err != nil {
		return r, err
	}
	return r, nil
}
func saveSuggestion(tx *gorm.DB, actor Actor, previous, next SuggestionRun) error {
	scope, err := jsonText(next.Scope)
	if err != nil {
		return err
	}
	output, err := jsonText(next.Output)
	if err != nil {
		return err
	}
	tools, err := jsonText(next.ToolNames)
	if err != nil {
		return err
	}
	draft, err := jsonText(next.DraftRuleSet)
	if err != nil {
		return err
	}
	row := suggestionRow{TenantID: actor.TenantID, ID: next.ID, ProjectID: next.ProjectID, ActorID: next.ActorID, Revision: next.Revision, Status: next.Status, ScopeJSON: scope, OutputJSON: output, ModelID: next.ModelID, RunMode: next.RunMode, PromptVersion: next.PromptVersion, ToolNamesJSON: tools, InputTokens: next.InputTokens, OutputTokens: next.OutputTokens, DurationMS: next.DurationMS, ErrorCode: next.ErrorCode, ErrorMessage: next.ErrorMessage, DecisionReason: next.DecisionReason, DraftRulesetJSON: draft, CreatedAt: next.CreatedAt, UpdatedAt: next.UpdatedAt}
	if previous.Revision == 0 {
		return tx.Create(&row).Error
	}
	r := tx.Model(&suggestionRow{}).Where("tenant_id=? AND id=? AND revision=? AND status=?", actor.TenantID, previous.ID, previous.Revision, previous.Status).Select("revision", "status", "output_json", "model_id", "run_mode", "tool_names_json", "input_tokens", "output_tokens", "duration_ms", "error_code", "error_message", "decision_reason", "draft_ruleset_json", "updated_at").Updates(&row)
	if r.Error != nil {
		return r.Error
	}
	if r.RowsAffected != 1 {
		return ErrVersionConflict
	}
	return nil
}
