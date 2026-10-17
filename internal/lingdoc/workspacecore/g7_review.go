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
		p, err := s.authorizeProject(ctx, tx, actor, pid, "read")
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
		store, ok := tx.(g5Transaction)
		if !ok {
			return ErrInvalidState
		}
		var saved ChapterReviewState
		_, err = store.G5Get(pid, "chapter_review", cid, &saved)
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
	capability := "write:" + cid
	if input.Action == "return" {
		capability = "review:" + cid
	}
	return s.operation(ctx, actor, "chapterReview", pid+"/"+cid, key, input, pid, capability, func(tx Transaction, p Project) (any, int, error) {
		if p.ProjectVersion != input.ExpectedProjectVersion || p.CurrentContextRevision != input.ExpectedContextRevision || p.SpecRevision != gc.SpecRevision || p.Status != "active" {
			return nil, 0, ErrVersionConflict
		}
		c, err := tx.Chapter(pid, cid)
		if err != nil {
			return nil, 0, err
		}
		if c.CurrentVersionID == nil || *c.CurrentVersionID != input.ChapterVersionID {
			return nil, 0, ErrVersionConflict
		}
		store, ok := tx.(g5Transaction)
		if !ok {
			return nil, 0, ErrInvalidState
		}
		var old ChapterReviewState
		version, err := store.G5Get(pid, "chapter_review", cid, &old)
		if err != nil && err != ErrNotFound {
			return nil, 0, err
		}
		if err == ErrNotFound {
			version = 0
		}
		if version != input.ExpectedReviewVersion {
			return nil, 0, ErrVersionConflict
		}
		if input.Action == "return" && (old.Status != "IN_REVIEW" || old.ChapterVersionID != input.ChapterVersionID) {
			return nil, 0, ErrInvalidState
		}
		status := "IN_REVIEW"
		if input.Action == "return" {
			status = "DRAFT"
		}
		next := ChapterReviewState{ChapterID: cid, ChapterVersionID: input.ChapterVersionID, ContextRevision: p.CurrentContextRevision, SpecRevision: p.SpecRevision, Version: version + 1, Status: status, ActorID: actor.UserID, Reason: input.Reason, UpdatedAt: time.Now().UTC()}
		if err := store.G5Put(pid, "chapter_review", cid, version, next); err != nil {
			return nil, 0, err
		}
		if err := g5Audit(tx, actor, pid, "chapter."+input.Action, cid, input.Reason); err != nil {
			return nil, 0, err
		}
		return next, 200, nil
	})
}
func (s *Service) SaveChapterComment(ctx context.Context, actor Actor, pid, cid, key string, input ChapterCommentInput) (json.RawMessage, int, bool, error) {
	if input.ExpectedProjectVersion < 1 || input.ExpectedContextRevision < 1 || input.ChapterVersionID == "" || len([]rune(input.Body)) > 4000 || !input.Resolve && strings.TrimSpace(input.Body) == "" {
		return nil, 0, false, ErrInvalidRequest
	}
	var historical ChapterVersion
	err := s.repository.Transaction(ctx, TransactionOptions{ReadOnly: true}, func(tx Transaction) error {
		if _, err := s.authorizeProject(ctx, tx, actor, pid, "read"); err != nil {
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
	capability := "review:" + cid
	if err := s.Authorize(ctx, actor, pid, capability); err != nil {
		capability = "write:" + cid
	}
	return s.operation(ctx, actor, "chapterComment", pid+"/"+cid, key, input, pid, capability, func(tx Transaction, p Project) (any, int, error) {
		if p.ProjectVersion != input.ExpectedProjectVersion || p.CurrentContextRevision != input.ExpectedContextRevision || p.Status != "active" {
			return nil, 0, ErrVersionConflict
		}
		version, err := tx.ChapterVersion(pid, cid, input.ChapterVersionID)
		if err != nil {
			return nil, 0, err
		}
		if version.BodyMarkdown != historical.BodyMarkdown {
			return nil, 0, ErrVersionConflict
		}
		store, ok := tx.(g5Transaction)
		if !ok {
			return nil, 0, ErrInvalidState
		}
		now := time.Now().UTC()
		comment := ChapterComment{ID: uuid.NewString(), ChapterID: cid, ChapterVersionID: input.ChapterVersionID, ContextRevision: p.CurrentContextRevision, Version: 1, ActorID: actor.UserID, Body: input.Body, Selection: input.Selection, CreatedAt: now, UpdatedAt: now}
		if input.ID != "" {
			_, err = store.G5Get(pid, "chapter_comment", input.ID, &comment)
			if err != nil {
				return nil, 0, err
			}
			if comment.ChapterID != cid || comment.ChapterVersionID != input.ChapterVersionID || comment.Version != input.ExpectedVersion {
				return nil, 0, ErrVersionConflict
			}
			if comment.ActorID != actor.UserID && !projectOwner(p, actor.UserID) {
				return nil, 0, ErrNotFound
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
		if err := store.G5Put(pid, "chapter_comment", comment.ID, input.ExpectedVersion, comment); err != nil {
			return nil, 0, err
		}
		if err := g5Audit(tx, actor, pid, "chapter.comment", comment.ID, ""); err != nil {
			return nil, 0, err
		}
		return comment, 201, nil
	})
}
func (s *Service) ListChapterComments(ctx context.Context, actor Actor, pid, cid, versionID string) ([]ChapterComment, error) {
	var sourceIDs []string
	var result []ChapterComment
	err := s.repository.Transaction(ctx, TransactionOptions{ReadOnly: true}, func(tx Transaction) error {
		if _, err := s.authorizeProject(ctx, tx, actor, pid, "read"); err != nil {
			return err
		}
		if _, err := tx.Chapter(pid, cid); err != nil {
			return err
		}
		store, ok := tx.(g5Transaction)
		if !ok {
			return ErrInvalidState
		}
		rows, err := store.G5List(pid, "chapter_comment")
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
