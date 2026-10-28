package workspacecore

import (
	"slices"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Tencent/WeKnora/internal/clienterror"
	"github.com/google/uuid"
)

// The wording-commit condition gate (#753).
//
// A wording commit promises "nothing confirmed changed": the operator told the
// panel the edit is phrasing only. The one signal the server can check without
// a model is literal: a registered spec value that USED to appear in the
// chapter text no longer does. That is a SUSPECTED condition change, not a
// verdict — the wording path stays open behind an explicit, audited human
// dismissal, and the semantic path (a real ChangeSet) stays the primary exit.
//
// Suspicion is deliberately cheap and literal: exact substring disappearance,
// values shorter than four runes excluded (noise like "是" or "500" matches
// too much prose). Paraphrasing a value away is a false positive the dismissal
// gate absorbs and records; quietly missing a condition change is the failure
// this gate exists to prevent.

// conditionValueMinRunes is the shortest spec value the gate looks at.
const conditionValueMinRunes = 4

// SuspectedConditionChange is one spec value that disappeared from the text.
type SuspectedConditionChange struct {
	FieldID string `json:"field_id"`
	Value   string `json:"value"`
}

// suspectedConditionChanges reports the registered spec values that occur in
// the old chapter text and no longer occur in the new one. The result is
// sorted by field id, so two runs over the same inputs agree exactly — the
// dismissal check below relies on that.
func suspectedConditionChanges(spec map[string]string, oldBody, newBody string) []SuspectedConditionChange {
	out := []SuspectedConditionChange{}
	for field, raw := range spec {
		value := strings.TrimSpace(raw)
		if utf8.RuneCountInString(value) < conditionValueMinRunes {
			continue
		}
		if strings.Contains(oldBody, value) && !strings.Contains(newBody, value) {
			out = append(out, SuspectedConditionChange{FieldID: field, Value: value})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].FieldID < out[j].FieldID })
	return out
}

// suspectedConditionChangeFields is the field-id set of a suspicion list, in
// the same deterministic order.
func suspectedConditionChangeFields(suspected []SuspectedConditionChange) []string {
	fields := make([]string, 0, len(suspected))
	for _, item := range suspected {
		fields = append(fields, item.FieldID)
	}
	return fields
}

// ErrConditionChangeDismissalStale reports that the dismissal the caller sent
// does not name the suspicions the server just recomputed — the operator
// confirmed a list they did not see. The commit is refused; the caller must
// show the current list and take a fresh decision.
var ErrConditionChangeDismissalStale = clienterror.New(clienterror.Form{
	Status: 409, Code: "condition_change_dismissal_stale",
	Message: "确认的研究条件疑义清单与当前不一致，请重新查看后再决定。", Retryable: false,
}, "condition_change_dismissal_stale")

// conditionChangeConfirmationRequiredError is the client-facing refusal of a
// wording commit that dropped a registered condition value. It carries the
// suspicions in Details so the panel can show exactly what the server saw.
type conditionChangeConfirmationRequiredError struct {
	suspected []SuspectedConditionChange
}

func (e conditionChangeConfirmationRequiredError) Error() string {
	return "working copy commit suspected a research-condition change"
}

func (e conditionChangeConfirmationRequiredError) ClientForm() clienterror.Form {
	return clienterror.Form{
		Status: 422, Code: "condition_change_confirmation_required",
		Message: "正文中不再出现已登记的研究条件值，疑似研究条件变化：请改走正式变更，或确认仍按措辞提交。",
		Details: map[string]any{"suspected_condition_changes": e.suspected},
	}
}

// reviewConditionChanges is the wording-commit gate: refuse with a named
// suspicion list, or accept an explicit dismissal that names the SAME list.
func reviewConditionChanges(spec map[string]string, oldBody, newBody string, dismissal *ConditionChangeDismissal) error {
	suspected := suspectedConditionChanges(spec, oldBody, newBody)
	if len(suspected) == 0 {
		return nil
	}
	if dismissal == nil {
		return conditionChangeConfirmationRequiredError{suspected: suspected}
	}
	if strings.TrimSpace(dismissal.Reason) == "" || len(dismissal.Reason) > dependencyCandidateTextLimit {
		return ErrInvalidRequest
	}
	dismissed := append([]string(nil), dismissal.DismissedFields...)
	sort.Strings(dismissed)
	if !slices.Equal(dismissed, suspectedConditionChangeFields(suspected)) {
		return ErrConditionChangeDismissalStale
	}
	return nil
}

// auditConditionDismissal writes the audited "still wording" decision. The
// auditTransaction assertion works off the same narrow transaction the commit
// closure holds.
func auditConditionDismissal(tx ChapterMutationTransaction, actor Actor, projectID, chapterID string, suspected []SuspectedConditionChange, dismissal *ConditionChangeDismissal, specRevision, workingCopyRevision int64) error {
	audit, ok := tx.(auditTransaction)
	if !ok {
		return ErrInvalidState
	}
	values := map[string]string{}
	for _, item := range suspected {
		values[item.FieldID] = item.Value
	}
	return audit.RecordAudit(AuditEvent{
		ID: uuid.NewString(), TenantID: actor.TenantID, ProjectID: projectID, ActorID: actor.UserID,
		Action: "chapter.commit.condition_dismissed", Target: chapterID,
		Details: map[string]any{
			"fields":                dismissal.DismissedFields,
			"values":                values,
			"reason":                dismissal.Reason,
			"spec_revision":         specRevision,
			"working_copy_revision": workingCopyRevision,
		},
		CreatedAt: time.Now().UTC(),
	})
}
