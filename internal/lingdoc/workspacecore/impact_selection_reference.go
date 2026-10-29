package workspacecore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

// Issue #774 tier B: manual selection references.
//
// Tier A locates the values the server watches, literally; tier C asks a
// model to propose sentences and verifies every quote. Tier B is the human
// path: the responsible person selects a passage of the affected chapter
// themselves and anchors it to the task. The anchor reuses the source-anchor
// mechanism the project already has — a verbatim quote identified by a
// content hash — so a selection is a stable identity, not a loose string.
//
// A selection is established BY a human decision and therefore lands as a
// location hint directly (no pending/adopt round trip); the chapter-advance
// re-verification covers it like every other hint. It is an attachment, not
// a verdict.
const (
	// ImpactLocationSourceSelectionReference marks a hint that a responsible
	// person anchored by hand (#774 tier B).
	ImpactLocationSourceSelectionReference = "selection_reference"
)

// ImpactSelectionReferenceInput is the request body of the selection
// anchor.
type ImpactSelectionReferenceInput struct {
	// ExpectedVersion is the task version the caller read; the write is
	// refused when the task moved under the caller.
	ExpectedVersion int64 `json:"expected_version"`
	// Quote is the verbatim passage from the chapter body. The server
	// locates it; an out-of-range or absent quote is refused.
	Quote string `json:"quote"`
	// Reason records why this passage needs a human pass.
	Reason string `json:"reason"`
}

// SelectionHashOf is the selection identity: the SHA-256 hex of the quote,
// the same shape as the source anchors' QuotedTextHash. It records WHAT was
// anchored; dedup and locating stay on the verbatim string.
func SelectionHashOf(quote string) string {
	digest := sha256.Sum256([]byte(quote))
	return hex.EncodeToString(digest[:])
}

// AddImpactSelectionReference anchors one verbatim passage of the affected
// chapter to an impact task as a human-established location hint. The write
// is refused when the quote is out of contract, does not occur verbatim in
// the chapter body, or duplicates an existing hint/suggestion on the task.
func (s *Service) AddImpactSelectionReference(ctx context.Context, actor Actor, projectID, taskID, key string, input ImpactSelectionReferenceInput) (json.RawMessage, int, bool, error) {
	if err := RequireHumanDecision(ctx); err != nil {
		return nil, 0, false, err
	}
	if input.ExpectedVersion < 1 || strings.TrimSpace(input.Reason) == "" || len([]rune(input.Reason)) > 2000 {
		return nil, 0, false, ErrInvalidRequest
	}
	quote := strings.TrimSpace(input.Quote)
	if utf8.RuneCountInString(quote) < impactSuggestionMinQuoteRunes || utf8.RuneCountInString(quote) > impactSuggestionMaxQuoteRunes {
		return nil, 0, false, ErrInvalidRequest
	}
	return checkedImpactWrite(s, ctx, actor, projectID, operationAddImpactSelectionReference, projectID+"/"+taskID, key, input, nil, authorizationRequest(actor, projectID, AuthorizationWrite, ""), func(tx Transaction, p Project) (operationResult[ImpactTask], error) {
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
		// Anchoring a passage is part of working the task: the assignee may
		// anchor, and so may management (the same bar as adopt/reject).
		if _, err := s.authorizeProject(ctx, tx, authorizationRequest(actor, projectID, AuthorizationManage, "")); err != nil {
			if task.AssigneeID != actor.UserID {
				return operationResult[ImpactTask]{}, ErrNotFound
			}
		}
		if task.Target.Kind != "ChapterVersion" {
			return operationResult[ImpactTask]{}, ErrInvalidRequest
		}
		if slices.Contains(ImpactTaskClosedStatuses, task.Status) || task.Status == "STALE" {
			return operationResult[ImpactTask]{}, ErrInvalidState
		}
		chapter, err := tx.Chapter(p.ID, task.Target.ID)
		if err != nil {
			return operationResult[ImpactTask]{}, err
		}
		body := chapter.BodyMarkdown
		if !strings.Contains(body, quote) {
			// An absent quote is a caller mistake, not a locatable passage:
			// refuse without guessing a position.
			return operationResult[ImpactTask]{}, ErrInvalidRequest
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
		if known[quote] {
			return operationResult[ImpactTask]{}, ErrInvalidRequest
		}
		paragraphs := splitImpactParagraphs(body)
		start := strings.Index(body, quote)
		end := start + len(quote)
		old := task
		task.LocationHints = append(append([]ImpactLocationHint{}, task.LocationHints...), ImpactLocationHint{
			Source: ImpactLocationSourceSelectionReference, Value: quote,
			ParagraphIndex: paragraphIndexOf(paragraphs, start),
			StartOffset:    utf16OffsetOf(body, start),
			EndOffset:      utf16OffsetOf(body, end),
			Excerpt:        impactExcerpt(body, start, end),
			SelectionHash:  SelectionHashOf(quote),
			Reason:         strings.TrimSpace(input.Reason),
		})
		task.Version++
		task.UpdatedAt = time.Now().UTC()
		if err := store.UpdateImpactTask(old, task); err != nil {
			return operationResult[ImpactTask]{}, err
		}
		if err := recordAuditEvent(tx, actor, projectID, "impact_task.selection_anchored", task.ID, map[string]any{
			"quote": quote, "selection_hash": SelectionHashOf(quote), "reason": input.Reason}); err != nil {
			return operationResult[ImpactTask]{}, err
		}
		return operationResult[ImpactTask]{Value: task, Status: operationStatusOK}, nil
	})
}
