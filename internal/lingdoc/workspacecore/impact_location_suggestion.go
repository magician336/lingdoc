package workspacecore

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Tencent/WeKnora/internal/clienterror"
	"github.com/google/uuid"
)

// Issue #774 tier C: AI sentence-level location suggestions.
//
// The owner's friction is between "which chapter" and "which sentence". Tier
// A locates the values the server already watches, literally; this tier asks
// a model to read the changed diff plus the FULL affected chapter text and
// propose the sentences that may need a human pass. The chapter is the
// project's own content, so the whole text goes in one call — the retrieval
// window limits that bind the writing pipeline do not apply here.
//
// The discipline (the user's decision recorded on #774):
//   - the model only SUGGESTS; the server verifies every quote verbatim
//     against the chapter body and drops (and counts) what it cannot find —
//     a hallucinated location never reaches the task;
//   - a verified proposal lands on the task as a PENDING suggestion; it
//     becomes a location hint only after a human adopts it;
//   - the trigger is human-pulled and the model call is metered through the
//     shared ledger; a replayed idempotency key never costs a second call;
//   - when the chapter evolves, the server re-verifies every quote and marks
//     the misses for relocation instead of letting them rot silently.
const (
	ImpactLocationSuggestionPending  = "pending"
	ImpactLocationSuggestionAdopted  = "adopted"
	ImpactLocationSuggestionRejected = "rejected"

	// ImpactLocationSourceAISentence marks a hint that came from an adopted
	// AI suggestion rather than from the tier-A literal watch list.
	ImpactLocationSourceAISentence = "ai_sentence"

	// impactSuggestionMinQuoteRunes keeps the #753 noise floor: shorter
	// strings match too much prose to be a location.
	impactSuggestionMinQuoteRunes  = conditionValueMinRunes
	impactSuggestionMaxQuoteRunes  = 200
	impactSuggestionMaxReasonRunes = 500
	impactSuggestionMaxActionRunes = 50
	// impactSuggestionMaxProposals caps one model answer; the prompt asks for
	// at most this many and the review truncates regardless.
	impactSuggestionMaxProposals = 20
	// impactSuggestionMaxPerTask caps the suggestions a single task carries.
	impactSuggestionMaxPerTask = 50
)

// impactSuggestionCallTimeout bounds one model call.
const impactSuggestionCallTimeout = 90 * time.Second

// ErrImpactLocationSuggestionUnavailable reports that the sentence-location
// recommender is not wired or its model could not answer. The manual paths
// (tier-A hints, human work on the task) are unaffected.
var ErrImpactLocationSuggestionUnavailable = clienterror.New(clienterror.Form{
	Status: 503, Code: "impact_location_suggestion_unavailable",
	Message: "AI 句级定位当前不可用，请稍后重试或直接按章节复核。", Retryable: true,
}, "impact_location_suggestion_unavailable")

// ErrImpactLocationSuggestionInvalidOutput reports a model answer the server
// could not review (truncated or malformed).
var ErrImpactLocationSuggestionInvalidOutput = clienterror.New(clienterror.Form{
	Status: 502, Code: "impact_location_suggestion_invalid_output",
	Message: "AI 句级定位返回了无法复核的结果，本次未产生建议。", Retryable: true,
}, "impact_location_suggestion_invalid_output")

// ImpactLocationSuggestion is one AI-proposed sentence location on a task,
// verbatim-verified by the server, waiting for a human decision.
type ImpactLocationSuggestion struct {
	ID              string  `json:"id"`
	Quote           string  `json:"quote"`
	ParagraphIndex  int     `json:"paragraph_index"`
	StartOffset     int     `json:"start_offset"`
	EndOffset       int     `json:"end_offset"`
	Excerpt         string  `json:"excerpt"`
	ImpactReason    string  `json:"impact_reason"`
	SuggestedAction string  `json:"suggested_action,omitempty"`
	Confidence      float64 `json:"confidence"`
	Status          string  `json:"status"`
	// Relocate marks a quote that no longer occurs in the current chapter
	// body. It is recomputed whenever the chapter advances; a relocated
	// suggestion cannot be adopted until someone re-anchors it.
	Relocate   bool      `json:"relocate,omitempty"`
	ModelRunID string    `json:"model_run_id"`
	CreatedAt  time.Time `json:"created_at"`
}

// ModelProposedImpactLocation is one raw model proposal. The server owns the
// position: the model only points at text, the server computes where that
// text sits.
type ModelProposedImpactLocation struct {
	Quote           string  `json:"quote"`
	ParagraphHint   int     `json:"paragraph_hint"`
	ImpactReason    string  `json:"impact_reason"`
	Confidence      float64 `json:"confidence"`
	SuggestedAction string  `json:"suggested_action"`
}

// ImpactLocationSuggestionRequest is what the adapter sends to the model:
// the applied change (old→new values) and the full affected chapter.
type ImpactLocationSuggestionRequest struct {
	ProjectID string
	Project   Project
	Task      ImpactTask
	Change    ChangeSet
	Chapter   Chapter
}

// ImpactLocationSuggestionResult is the adapter's answer.
type ImpactLocationSuggestionResult struct {
	Proposals  []ModelProposedImpactLocation
	ModelID    string
	RunMode    string
	ModelRunID string
}

// ImpactLocationSuggestionModel is the narrow port: one call, no automatic
// retry, one ledger row per call — the same discipline as the dependency
// recommender.
type ImpactLocationSuggestionModel interface {
	Suggest(ctx context.Context, actor Actor, request ImpactLocationSuggestionRequest) (ImpactLocationSuggestionResult, error)
}

// ImpactLocationSuggestionInput is the trigger body.
type ImpactLocationSuggestionInput struct {
	// IdempotencyKey identifies this suggestion run; it defaults to the
	// transport idempotency key. A replayed key returns the task untouched
	// without a second model call.
	IdempotencyKey string `json:"idempotency_key,omitempty"`
	// ExpectedVersion is the task version the caller read; the write is
	// refused when the task moved under the caller.
	ExpectedVersion int64  `json:"expected_version"`
	Reason          string `json:"reason"`
}

// ImpactLocationSuggestionDecisionInput adopts or rejects one suggestion.
type ImpactLocationSuggestionDecisionInput struct {
	ExpectedVersion int64  `json:"expected_version"`
	Decision        string `json:"decision"`
	Reason          string `json:"reason"`
}

// ImpactLocationSuggestionDrop is one proposal the server refused, with the
// reason. Drops are reported back so the caller sees what did not survive —
// recall honesty: the UI must be able to say the list is not exhaustive.
type ImpactLocationSuggestionDrop struct {
	Quote  string `json:"quote"`
	Reason string `json:"reason"`
}

// ImpactLocationSuggestionModelInfo reports which model produced a batch.
type ImpactLocationSuggestionModelInfo struct {
	ModelID    string `json:"model_id,omitempty"`
	RunMode    string `json:"run_mode,omitempty"`
	ModelRunID string `json:"model_run_id,omitempty"`
}

// ImpactLocationSuggestionOutcome is the trigger response: the suggestions
// that survived verification, the ones that did not, and the model that ran.
type ImpactLocationSuggestionOutcome struct {
	Task        json.RawMessage                   `json:"task"`
	Suggestions []ImpactLocationSuggestion        `json:"suggestions"`
	Dropped     []ImpactLocationSuggestionDrop    `json:"dropped,omitempty"`
	Model       ImpactLocationSuggestionModelInfo `json:"model"`
}

// SetImpactLocationSuggestionModel wires the sentence-location model. Without
// it the trigger fails closed; the tier-A hints and all manual work are
// unaffected.
func (s *Service) SetImpactLocationSuggestionModel(model ImpactLocationSuggestionModel) {
	s.impactLocationModel = model
}

// suggestImpactLocationBasis reads the task, its change and the chapter in
// one read-only transaction, and reports a replay before the model is ever
// invoked: a replayed key must not cost a second model call.
func (s *Service) suggestImpactLocationBasis(ctx context.Context, actor Actor, projectID, taskID, batchKey string) (Project, ImpactTask, ChangeSet, Chapter, bool, error) {
	var p Project
	var task ImpactTask
	var change ChangeSet
	var chapter Chapter
	replayed := false
	err := s.repository.Transaction(ctx, TransactionOptions{ReadOnly: true}, func(tx Transaction) error {
		store, ok := tx.(impactTransaction)
		if !ok {
			return ErrInvalidState
		}
		authorized, err := s.authorizeProject(ctx, tx, authorizationRequest(actor, projectID, AuthorizationWrite, ""))
		if err != nil {
			return err
		}
		p = authorized
		task, err = store.ImpactTask(projectID, taskID)
		if err != nil {
			return err
		}
		if task.Target.Kind != "ChapterVersion" {
			return ErrInvalidRequest
		}
		if slices.Contains(ImpactTaskClosedStatuses, task.Status) || task.Status == "STALE" {
			return ErrInvalidState
		}
		if task.SuggestionBatchKey == batchKey {
			replayed = true
			return nil
		}
		changes, ok := tx.(changeSetTransaction)
		if !ok {
			return ErrInvalidState
		}
		change, err = changes.ChangeSet(projectID, task.ChangeSetID)
		if err != nil {
			return err
		}
		chapter, err = tx.Chapter(projectID, task.Target.ID)
		if err != nil {
			return err
		}
		return nil
	})
	return p, task, change, chapter, replayed, err
}

// SuggestImpactLocations runs one sentence-location suggestion round on an
// impact task: read the basis, ask the model, verify every quote verbatim,
// and append the survivors as PENDING suggestions on the task. The model call
// itself happens OUTSIDE any write transaction; the write re-checks the task
// version so a concurrent move of the task is refused, not overwritten.
func (s *Service) SuggestImpactLocations(ctx context.Context, actor Actor, projectID, taskID, key string, input ImpactLocationSuggestionInput) (json.RawMessage, int, bool, error) {
	if err := RequireHumanDecision(ctx); err != nil {
		return nil, 0, false, err
	}
	batchKey := strings.TrimSpace(input.IdempotencyKey)
	if batchKey == "" {
		batchKey = strings.TrimSpace(key)
	}
	if batchKey == "" || len(batchKey) > 128 || input.ExpectedVersion < 1 || strings.TrimSpace(input.Reason) == "" || len([]rune(input.Reason)) > 2000 {
		return nil, 0, false, ErrInvalidRequest
	}
	if s.impactLocationModel == nil {
		return nil, 0, false, ErrImpactLocationSuggestionUnavailable
	}
	p, task, change, chapter, replayed, err := s.suggestImpactLocationBasis(ctx, actor, projectID, taskID, batchKey)
	if err != nil {
		return nil, 0, false, err
	}
	if replayed {
		raw, err := json.Marshal(ImpactLocationSuggestionOutcome{Task: mustMarshalTask(task), Suggestions: pendingSuggestions(task), Model: ImpactLocationSuggestionModelInfo{}})
		if err != nil {
			return nil, 0, false, err
		}
		return raw, 200, true, nil
	}
	if task.Version != input.ExpectedVersion {
		return nil, 0, false, ErrVersionConflict
	}
	modelCtx, cancel := context.WithTimeout(ctx, impactSuggestionCallTimeout)
	defer cancel()
	result, err := s.impactLocationModel.Suggest(modelCtx, actor, ImpactLocationSuggestionRequest{
		ProjectID: projectID, Project: p, Task: task, Change: change, Chapter: chapter,
	})
	if err != nil {
		// A model that could not answer is an unavailable recommender; the
		// cause stays first in the chain.
		return nil, 0, false, errors.Join(err, ErrImpactLocationSuggestionUnavailable)
	}
	if result.ModelRunID == "" {
		return nil, 0, false, ErrImpactLocationSuggestionInvalidOutput
	}
	suggestions, dropped := reviewModelProposedLocations(result.Proposals, chapter.BodyMarkdown, task)
	if len(suggestions) == 0 && len(dropped) == 0 {
		return nil, 0, false, ErrImpactLocationSuggestionInvalidOutput
	}
	now := time.Now().UTC()
	for i := range suggestions {
		suggestions[i].ModelRunID = result.ModelRunID
		suggestions[i].CreatedAt = now
	}
	// The write re-reads the task in its own transaction: the model call sat
	// outside, so the task may have moved; refuse instead of overwriting.
	outcomeTask, err := s.appendImpactLocationSuggestions(ctx, actor, p, projectID, taskID, batchKey, input.ExpectedVersion, suggestions)
	if err != nil {
		return nil, 0, false, err
	}
	outcome := ImpactLocationSuggestionOutcome{
		Task:        mustMarshalTask(outcomeTask),
		Suggestions: suggestions,
		Dropped:     dropped,
		Model: ImpactLocationSuggestionModelInfo{
			ModelID: result.ModelID, RunMode: result.RunMode, ModelRunID: result.ModelRunID,
		},
	}
	out, err := json.Marshal(outcome)
	if err != nil {
		return nil, 0, false, err
	}
	return out, 200, false, nil
}

// mustMarshalTask serializes a task for an outcome envelope; a task is plain
// data and cannot fail to marshal, so a failure degrades to an empty object
// rather than discarding an otherwise complete outcome.
func mustMarshalTask(task ImpactTask) json.RawMessage {
	raw, err := json.Marshal(task)
	if err != nil {
		return json.RawMessage("{}")
	}
	return raw
}

// appendImpactLocationSuggestions appends the verified suggestions to the
// task in one write transaction with a version CAS and an audit event.
func (s *Service) appendImpactLocationSuggestions(ctx context.Context, actor Actor, p Project, projectID, taskID, batchKey string, expectedVersion int64, suggestions []ImpactLocationSuggestion) (ImpactTask, error) {
	var out ImpactTask
	err := s.repository.Transaction(ctx, TransactionOptions{}, func(tx Transaction) error {
		store, ok := tx.(impactTransaction)
		if !ok {
			return ErrInvalidState
		}
		if _, err := s.authorizeProject(ctx, tx, authorizationRequest(actor, projectID, AuthorizationWrite, "")); err != nil {
			return err
		}
		task, err := store.ImpactTask(projectID, taskID)
		if err != nil {
			return err
		}
		if task.Version != expectedVersion {
			return ErrVersionConflict
		}
		if slices.Contains(ImpactTaskClosedStatuses, task.Status) || task.Status == "STALE" {
			return ErrInvalidState
		}
		old := task
		task.LocationSuggestions = append(append([]ImpactLocationSuggestion{}, task.LocationSuggestions...), suggestions...)
		task.SuggestionBatchKey = batchKey
		task.Version++
		task.UpdatedAt = time.Now().UTC()
		if err := store.UpdateImpactTask(old, task); err != nil {
			return err
		}
		quotes := make([]string, 0, len(suggestions))
		for _, item := range suggestions {
			quotes = append(quotes, item.Quote)
		}
		if err := recordAuditEvent(tx, actor, projectID, "impact_task.location_suggested", task.ID, map[string]any{
			"suggestions": len(suggestions), "model_run_id": suggestionsModelRun(suggestions), "batch_key": batchKey, "quotes": quotes}); err != nil {
			return err
		}
		out = task
		return nil
	})
	return out, err
}

// DecideImpactLocationSuggestion adopts or rejects one pending suggestion.
// Adoption appends the location hint (source ai_sentence) to the task; a
// relocated suggestion cannot be adopted — the text it pointed at is gone.
func (s *Service) DecideImpactLocationSuggestion(ctx context.Context, actor Actor, projectID, taskID, suggestionID, key string, input ImpactLocationSuggestionDecisionInput) (json.RawMessage, int, bool, error) {
	if err := RequireHumanDecision(ctx); err != nil {
		return nil, 0, false, err
	}
	if input.ExpectedVersion < 1 || !slices.Contains([]string{"adopt", "reject"}, input.Decision) || strings.TrimSpace(input.Reason) == "" || len([]rune(input.Reason)) > 2000 {
		return nil, 0, false, ErrInvalidRequest
	}
	return checkedImpactWrite(s, ctx, actor, projectID, operationDecideImpactLocationSuggestion, projectID+"/"+taskID+"/"+suggestionID, key, input, nil, authorizationRequest(actor, projectID, AuthorizationWrite, ""), func(tx Transaction, p Project) (operationResult[ImpactTask], error) {
		store, ok := tx.(impactTransaction)
		if !ok {
			return operationResult[ImpactTask]{}, ErrInvalidState
		}
		task, err := store.ImpactTask(projectID, taskID)
		if err != nil {
			return operationResult[ImpactTask]{}, err
		}
		if task.Version != input.ExpectedVersion {
			return operationResult[ImpactTask]{}, ErrVersionConflict
		}
		// The decision is a responsible person's judgment: manage may decide,
		// otherwise only the assignee (the same bar as assign/dismiss).
		if _, err := s.authorizeProject(ctx, tx, authorizationRequest(actor, projectID, AuthorizationManage, "")); err != nil {
			if task.AssigneeID != actor.UserID {
				return operationResult[ImpactTask]{}, ErrNotFound
			}
		}
		if slices.Contains(ImpactTaskClosedStatuses, task.Status) || task.Status == "STALE" {
			return operationResult[ImpactTask]{}, ErrInvalidState
		}
		index := -1
		for i := range task.LocationSuggestions {
			if task.LocationSuggestions[i].ID == suggestionID {
				index = i
				break
			}
		}
		if index < 0 {
			return operationResult[ImpactTask]{}, ErrNotFound
		}
		suggestion := task.LocationSuggestions[index]
		if suggestion.Status != ImpactLocationSuggestionPending {
			return operationResult[ImpactTask]{}, ErrInvalidState
		}
		if input.Decision == "adopt" {
			if suggestion.Relocate {
				return operationResult[ImpactTask]{}, ErrInvalidState
			}
			task.LocationHints = append(append([]ImpactLocationHint{}, task.LocationHints...), ImpactLocationHint{
				Source: ImpactLocationSourceAISentence, Value: suggestion.Quote,
				ParagraphIndex: suggestion.ParagraphIndex, StartOffset: suggestion.StartOffset,
				EndOffset: suggestion.EndOffset, Excerpt: suggestion.Excerpt,
			})
		}
		old := task
		task.LocationSuggestions[index].Status = ImpactLocationSuggestionAdopted
		if input.Decision == "reject" {
			task.LocationSuggestions[index].Status = ImpactLocationSuggestionRejected
		}
		task.Version++
		task.UpdatedAt = time.Now().UTC()
		if err := store.UpdateImpactTask(old, task); err != nil {
			return operationResult[ImpactTask]{}, err
		}
		if err := recordAuditEvent(tx, actor, projectID, "impact_task.location_decided", task.ID, map[string]any{
			"decision": input.Decision, "suggestion_id": suggestionID, "quote": suggestion.Quote, "reason": input.Reason}); err != nil {
			return operationResult[ImpactTask]{}, err
		}
		return operationResult[ImpactTask]{Value: task, Status: operationStatusOK}, nil
	})
}

// revalidateImpactLocations recomputes, for every task aimed at one chapter,
// whether each hint and suggestion still occurs verbatim in the NEW chapter
// body. A miss is marked for relocation — visible, never silently expired.
// It returns the number of tasks it had to rewrite. Callers run it inside the
// chapter-advance transaction; callers without the impact port skip it.
func revalidateImpactLocations(tx impactTransaction, projectID, chapterID, newBody string) (int, error) {
	tasks, err := tx.ImpactTasks(projectID)
	if err != nil {
		return 0, err
	}
	relocated := 0
	for _, task := range tasks {
		if task.Target.Kind != "ChapterVersion" || task.Target.ID != chapterID {
			continue
		}
		next := task
		changed := false
		for i, hint := range next.LocationHints {
			if missing := !strings.Contains(newBody, hint.Value); missing != hint.Relocate {
				next.LocationHints[i].Relocate = missing
				changed = true
			}
		}
		for i, suggestion := range next.LocationSuggestions {
			if suggestion.Status == ImpactLocationSuggestionRejected {
				continue
			}
			if missing := !strings.Contains(newBody, suggestion.Quote); missing != suggestion.Relocate {
				next.LocationSuggestions[i].Relocate = missing
				changed = true
			}
		}
		if !changed {
			continue
		}
		next.Version++
		next.UpdatedAt = time.Now().UTC()
		if err := tx.UpdateImpactTask(task, next); err != nil {
			return relocated, err
		}
		relocated++
	}
	return relocated, nil
}

// reviewModelProposedLocations verifies each proposal and computes its
// position from the chapter body. A proposal that fails the contract or whose
// quote is not found verbatim is dropped with the reason — never guessed.
func reviewModelProposedLocations(proposals []ModelProposedImpactLocation, body string, task ImpactTask) ([]ImpactLocationSuggestion, []ImpactLocationSuggestionDrop) {
	suggestions := []ImpactLocationSuggestion{}
	drops := []ImpactLocationSuggestionDrop{}
	if len(proposals) > impactSuggestionMaxProposals {
		proposals = proposals[:impactSuggestionMaxProposals]
	}
	known := map[string]bool{}
	for _, hint := range task.LocationHints {
		known[hint.Value] = true
	}
	for _, suggestion := range task.LocationSuggestions {
		if suggestion.Status != ImpactLocationSuggestionRejected {
			known[suggestion.Quote] = true
		}
	}
	paragraphs := splitImpactParagraphs(body)
	for _, proposal := range proposals {
		quote := strings.TrimSpace(proposal.Quote)
		switch {
		case utf8.RuneCountInString(quote) < impactSuggestionMinQuoteRunes:
			drops = append(drops, ImpactLocationSuggestionDrop{Quote: quote, Reason: "quote_too_short"})
			continue
		case utf8.RuneCountInString(quote) > impactSuggestionMaxQuoteRunes:
			drops = append(drops, ImpactLocationSuggestionDrop{Quote: quote, Reason: "quote_too_long"})
			continue
		case strings.TrimSpace(proposal.ImpactReason) == "" || utf8.RuneCountInString(proposal.ImpactReason) > impactSuggestionMaxReasonRunes:
			drops = append(drops, ImpactLocationSuggestionDrop{Quote: quote, Reason: "impact_reason_missing"})
			continue
		case utf8.RuneCountInString(proposal.SuggestedAction) > impactSuggestionMaxActionRunes:
			drops = append(drops, ImpactLocationSuggestionDrop{Quote: quote, Reason: "suggested_action_too_long"})
			continue
		case proposal.Confidence < 0 || proposal.Confidence > 1:
			drops = append(drops, ImpactLocationSuggestionDrop{Quote: quote, Reason: "confidence_out_of_range"})
			continue
		case known[quote]:
			drops = append(drops, ImpactLocationSuggestionDrop{Quote: quote, Reason: "duplicate"})
			continue
		case !strings.Contains(body, quote):
			// The verbatim gate: a quote the server cannot find in the body
			// is a hallucinated location and is dropped and counted.
			drops = append(drops, ImpactLocationSuggestionDrop{Quote: quote, Reason: "quote_not_found"})
			continue
		}
		if len(suggestions)+countSuggestions(task) >= impactSuggestionMaxPerTask {
			drops = append(drops, ImpactLocationSuggestionDrop{Quote: quote, Reason: "overflow"})
			continue
		}
		start := strings.Index(body, quote)
		end := start + len(quote)
		suggestions = append(suggestions, ImpactLocationSuggestion{
			ID: uuid.NewString(), Quote: quote,
			ParagraphIndex:  paragraphIndexOf(paragraphs, start),
			StartOffset:     utf16OffsetOf(body, start),
			EndOffset:       utf16OffsetOf(body, end),
			Excerpt:         impactExcerpt(body, start, end),
			ImpactReason:    strings.TrimSpace(proposal.ImpactReason),
			SuggestedAction: strings.TrimSpace(proposal.SuggestedAction),
			Confidence:      proposal.Confidence,
			Status:          ImpactLocationSuggestionPending,
		})
		known[quote] = true
	}
	return suggestions, drops
}

func countSuggestions(task ImpactTask) int {
	count := 0
	for _, suggestion := range task.LocationSuggestions {
		if suggestion.Status != ImpactLocationSuggestionRejected {
			count++
		}
	}
	return count
}

func pendingSuggestions(task ImpactTask) []ImpactLocationSuggestion {
	out := []ImpactLocationSuggestion{}
	for _, suggestion := range task.LocationSuggestions {
		if suggestion.Status == ImpactLocationSuggestionPending {
			out = append(out, suggestion)
		}
	}
	return out
}

func suggestionsModelRun(suggestions []ImpactLocationSuggestion) string {
	for _, suggestion := range suggestions {
		return suggestion.ModelRunID
	}
	return ""
}
