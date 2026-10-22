package workspacecore

import (
	"context"
	"encoding/json"
	"github.com/Tencent/WeKnora/internal/lingdoc/reviewanchor"
	"github.com/google/uuid"
	"slices"
	"strings"
	"time"
)

type ChapterReviewState struct {
	ChapterID        string    `json:"chapter_id"`
	ChapterVersionID string    `json:"chapter_version_id"`
	ContextRevision  int64     `json:"context_revision"`
	SpecRevision     int64     `json:"spec_revision"`
	Version          int64     `json:"version"`
	Status           string    `json:"status"`
	ActorID          string    `json:"actor_id"`
	Reason           string    `json:"reason,omitempty"`
	UpdatedAt        time.Time `json:"updated_at"`
}
type ChapterReviewInput struct {
	ExpectedProjectVersion  int64  `json:"expected_project_version"`
	ExpectedContextRevision int64  `json:"expected_context_revision"`
	ChapterVersionID        string `json:"chapter_version_id"`
	ExpectedReviewVersion   int64  `json:"expected_review_version"`
	Action                  string `json:"action"`
	Reason                  string `json:"reason"`
}
type ChapterComment struct {
	ID               string                  `json:"id"`
	ChapterID        string                  `json:"chapter_id"`
	ChapterVersionID string                  `json:"chapter_version_id"`
	ContextRevision  int64                   `json:"context_revision"`
	Version          int64                   `json:"version"`
	ActorID          string                  `json:"actor_id"`
	Body             string                  `json:"body"`
	Selection        *reviewanchor.Selection `json:"selection,omitempty"`
	Resolved         bool                    `json:"resolved"`
	CreatedAt        time.Time               `json:"created_at"`
	UpdatedAt        time.Time               `json:"updated_at"`
}
type ChapterCommentInput struct {
	ExpectedProjectVersion  int64                   `json:"expected_project_version"`
	ExpectedContextRevision int64                   `json:"expected_context_revision"`
	ChapterVersionID        string                  `json:"chapter_version_id"`
	ID                      string                  `json:"id,omitempty"`
	ExpectedVersion         int64                   `json:"expected_version"`
	Body                    string                  `json:"body"`
	Selection               *reviewanchor.Selection `json:"selection,omitempty"`
	Resolve                 bool                    `json:"resolve"`
}

func (s *Service) GetChapterReview(ctx context.Context, actor Actor, pid, cid string) (ChapterReviewState, error) {
	gc, err := s.GenerationContext(ctx, actor, pid, cid)
	if err != nil {
		return ChapterReviewState{}, err
	}
	result := ChapterReviewState{ChapterID: cid, ContextRevision: 0, SpecRevision: gc.SpecRevision, Status: "DRAFT"}
	err = s.repository.Transaction(ctx, TransactionOptions{ReadOnly: true}, func(tx Transaction) error {
		p, err := s.authorizeProject(ctx, tx, authorizationRequest(actor, pid, AuthorizationRead, ""))
		if err != nil {
			return err
		}
		chapter, err := tx.Chapter(pid, cid)
		if err != nil {
			return err
		}
		result.ContextRevision = p.CurrentContextRevision
		if chapter.CurrentVersionID == nil {
			return nil
		}
		result.ChapterVersionID = *chapter.CurrentVersionID
		store, ok := tx.(projectRecordTransaction)
		if !ok {
			return ErrInvalidState
		}
		var saved ChapterReviewState
		_, err = store.GetProjectRecord(pid, "chapter_review", cid, &saved)
		if err != nil && err != ErrNotFound {
			return err
		}
		if err == nil {
			result = saved
			if saved.ChapterVersionID != *chapter.CurrentVersionID || saved.SpecRevision != p.SpecRevision {
				result.Status = "STALE"
			}
		}
		if chapter.ConfirmationValid && result.Status != "STALE" {
			result.Status = "CONFIRMED"
		}
		if p.Status == "archived" {
			result.Status = "ARCHIVED"
		}
		return nil
	})
	return result, err
}
func (s *Service) ChangeChapterReview(ctx context.Context, actor Actor, pid, cid, key string, input ChapterReviewInput) (json.RawMessage, int, bool, error) {
	if input.ExpectedProjectVersion < 1 || input.ExpectedContextRevision < 1 || input.ChapterVersionID == "" || !slices.Contains([]string{"submit", "return"}, input.Action) || input.Action == "return" && strings.TrimSpace(input.Reason) == "" {
		return nil, 0, false, ErrInvalidRequest
	}
	gc, err := s.GenerationContext(ctx, actor, pid, cid)
	if err != nil {
		return nil, 0, false, err
	}
	if gc.ChapterVersionID == nil || *gc.ChapterVersionID != input.ChapterVersionID {
		return nil, 0, false, ErrVersionConflict
	}
	reviewAction := AuthorizationWrite
	if input.Action == "return" {
		reviewAction = AuthorizationReview
	}
	return runOperation(s, ctx, actor, sourceOperationInput(operationChapterReview, pid+"/"+cid, key, input, authorizationRequest(actor, pid, reviewAction, cid), chapterReviewSourceIDs), mutationPort[Transaction], func(tx Transaction, p Project) (operationResult[ChapterReviewState], error) {
		if p.ProjectVersion != input.ExpectedProjectVersion || p.CurrentContextRevision != input.ExpectedContextRevision || p.SpecRevision != gc.SpecRevision || p.Status != "active" {
			return operationResult[ChapterReviewState]{}, ErrVersionConflict
		}
		c, err := tx.Chapter(pid, cid)
		if err != nil {
			return operationResult[ChapterReviewState]{}, err
		}
		if c.CurrentVersionID == nil || *c.CurrentVersionID != input.ChapterVersionID {
			return operationResult[ChapterReviewState]{}, ErrVersionConflict
		}
		store, ok := tx.(projectRecordTransaction)
		if !ok {
			return operationResult[ChapterReviewState]{}, ErrInvalidState
		}
		var old ChapterReviewState
		version, err := store.GetProjectRecord(pid, "chapter_review", cid, &old)
		if err != nil && err != ErrNotFound {
			return operationResult[ChapterReviewState]{}, err
		}
		if err == ErrNotFound {
			version = 0
		}
		if version != input.ExpectedReviewVersion {
			return operationResult[ChapterReviewState]{}, ErrVersionConflict
		}
		if input.Action == "return" && (old.Status != "IN_REVIEW" || old.ChapterVersionID != input.ChapterVersionID) {
			return operationResult[ChapterReviewState]{}, ErrInvalidState
		}
		status := "IN_REVIEW"
		if input.Action == "return" {
			status = "DRAFT"
		}
		next := ChapterReviewState{ChapterID: cid, ChapterVersionID: input.ChapterVersionID, ContextRevision: p.CurrentContextRevision, SpecRevision: p.SpecRevision, Version: version + 1, Status: status, ActorID: actor.UserID, Reason: input.Reason, UpdatedAt: time.Now().UTC()}
		if err := store.PutProjectRecord(pid, "chapter_review", cid, version, next); err != nil {
			return operationResult[ChapterReviewState]{}, err
		}
		if err := recordProjectAuditEvent(tx, actor, pid, "chapter."+input.Action, cid, input.Reason); err != nil {
			return operationResult[ChapterReviewState]{}, err
		}
		return operationResult[ChapterReviewState]{Value: next, Status: operationStatusOK}, nil
	})
}
func (s *Service) SaveChapterComment(ctx context.Context, actor Actor, pid, cid, key string, input ChapterCommentInput) (json.RawMessage, int, bool, error) {
	if input.ExpectedProjectVersion < 1 || input.ExpectedContextRevision < 1 || input.ChapterVersionID == "" || len([]rune(input.Body)) > 4000 || !input.Resolve && strings.TrimSpace(input.Body) == "" {
		return nil, 0, false, ErrInvalidRequest
	}
	var historical ChapterVersion
	err := s.repository.Transaction(ctx, TransactionOptions{ReadOnly: true}, func(tx Transaction) error {
		if _, err := s.authorizeProject(ctx, tx, authorizationRequest(actor, pid, AuthorizationRead, "")); err != nil {
			return err
		}
		var err error
		historical, err = tx.ChapterVersion(pid, cid, input.ChapterVersionID)
		return err
	})
	if err != nil {
		return nil, 0, false, err
	}
	if err := s.checkSources(ctx, actor, pid, historical.SourceIDs); err != nil {
		return nil, 0, false, err
	}
	if input.Selection != nil && !reviewanchor.Valid(historical.BodyMarkdown, *input.Selection) {
		return nil, 0, false, ErrInvalidRequest
	}
	reviewAction := AuthorizationReview
	if err := s.AuthorizeRequest(ctx, authorizationRequest(actor, pid, reviewAction, cid)); err != nil {
		reviewAction = AuthorizationWrite
	}
	return runOperation(s, ctx, actor, sourceOperationInput(operationChapterComment, pid+"/"+cid, key, input, authorizationRequest(actor, pid, reviewAction, cid), chapterCommentSourceIDs), mutationPort[Transaction], func(tx Transaction, p Project) (operationResult[ChapterComment], error) {
		if p.ProjectVersion != input.ExpectedProjectVersion || p.CurrentContextRevision != input.ExpectedContextRevision || p.Status != "active" {
			return operationResult[ChapterComment]{}, ErrVersionConflict
		}
		version, err := tx.ChapterVersion(pid, cid, input.ChapterVersionID)
		if err != nil {
			return operationResult[ChapterComment]{}, err
		}
		if version.BodyMarkdown != historical.BodyMarkdown {
			return operationResult[ChapterComment]{}, ErrVersionConflict
		}
		store, ok := tx.(projectRecordTransaction)
		if !ok {
			return operationResult[ChapterComment]{}, ErrInvalidState
		}
		now := time.Now().UTC()
		comment := ChapterComment{ID: uuid.NewString(), ChapterID: cid, ChapterVersionID: input.ChapterVersionID, ContextRevision: p.CurrentContextRevision, Version: 1, ActorID: actor.UserID, Body: input.Body, Selection: input.Selection, CreatedAt: now, UpdatedAt: now}
		if input.ID != "" {
			_, err = store.GetProjectRecord(pid, "chapter_comment", input.ID, &comment)
			if err != nil {
				return operationResult[ChapterComment]{}, err
			}
			if comment.ChapterID != cid || comment.ChapterVersionID != input.ChapterVersionID || comment.Version != input.ExpectedVersion {
				return operationResult[ChapterComment]{}, ErrVersionConflict
			}
			if comment.ActorID != actor.UserID && !projectOwner(p, actor.UserID) {
				return operationResult[ChapterComment]{}, ErrNotFound
			}
			comment.Version++
			comment.UpdatedAt = now
			if input.Resolve {
				comment.Resolved = true
			} else {
				comment.Body = input.Body
				comment.Selection = input.Selection
			}
		}
		if err := store.PutProjectRecord(pid, "chapter_comment", comment.ID, input.ExpectedVersion, comment); err != nil {
			return operationResult[ChapterComment]{}, err
		}
		if err := recordProjectAuditEvent(tx, actor, pid, "chapter.comment", comment.ID, ""); err != nil {
			return operationResult[ChapterComment]{}, err
		}
		return operationResult[ChapterComment]{Value: comment, Status: operationStatusCreated}, nil
	})
}
func (s *Service) ListChapterComments(ctx context.Context, actor Actor, pid, cid, versionID string) ([]ChapterComment, error) {
	var sourceIDs []string
	var result []ChapterComment
	err := s.repository.Transaction(ctx, TransactionOptions{ReadOnly: true}, func(tx Transaction) error {
		if _, err := s.authorizeProject(ctx, tx, authorizationRequest(actor, pid, AuthorizationRead, "")); err != nil {
			return err
		}
		if _, err := tx.Chapter(pid, cid); err != nil {
			return err
		}
		store, ok := tx.(projectRecordTransaction)
		if !ok {
			return ErrInvalidState
		}
		rows, err := store.ListProjectRecords(pid, "chapter_comment")
		if err != nil {
			return err
		}
		result = []ChapterComment{}
		for _, row := range rows {
			var item ChapterComment
			if err := json.Unmarshal([]byte(row.PayloadJSON), &item); err != nil {
				return err
			}
			if item.ChapterID == cid && (versionID == "" || versionID == item.ChapterVersionID) {
				version, err := tx.ChapterVersion(pid, cid, item.ChapterVersionID)
				if err != nil {
					return err
				}
				sourceIDs = append(sourceIDs, version.SourceIDs...)
				result = append(result, item)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sourceIDs = slices.Compact(sortedImpactSourceIDs(sourceIDs))
	if err := s.checkSources(ctx, actor, pid, sourceIDs); err != nil {
		return nil, err
	}
	return result, nil
}
