package workspacecore

import (
	"context"
	"encoding/json"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
)

var workingCopySourceMarker = regexp.MustCompile(`\[\[source:([A-Za-z0-9_-]+)\]\]`)

func validateWorkingCopyBody(body string, sourceIDs []string) ([]string, error) {
	if sourceIDs == nil || len(body) > 200000 {
		return nil, ErrInvalidRequest
	}
	markers := workingCopySourceMarker.FindAllStringSubmatch(body, -1)
	if strings.Count(body, "[[source:") != len(markers) {
		return nil, ErrInvalidRequest
	}
	refs := make([]string, 0, len(markers))
	for _, marker := range markers {
		refs = append(refs, marker[1])
	}
	slices.Sort(refs)
	refs = slices.Compact(refs)
	declared := slices.Clone(sourceIDs)
	slices.Sort(declared)
	if len(slices.Compact(slices.Clone(declared))) != len(declared) || !slices.Equal(refs, declared) {
		return nil, ErrInvalidRequest
	}
	return declared, nil
}

func (s *Service) checkSources(ctx context.Context, actor Actor, projectID string, sourceIDs []string) error {
	if len(sourceIDs) == 0 {
		return nil
	}
	if s.sources == nil {
		return ErrSourceUnavailable
	}
	ids := slices.Clone(sourceIDs)
	slices.Sort(ids)
	return s.sources.Validate(ctx, projectID, actor.UserID, ids)
}

func (s *Service) GetWorkingCopy(ctx context.Context, actor Actor, projectID, chapterID string) (WorkingCopy, error) {
	var result WorkingCopy
	err := s.repository.Transaction(ctx, TransactionOptions{ReadOnly: true}, func(tx Transaction) error {
		if _, err := s.authorizeProject(ctx, tx, actor, projectID, "read"); err != nil {
			return err
		}
		chapter, err := tx.Chapter(projectID, chapterID)
		if err != nil {
			return err
		}
		result, err = tx.WorkingCopy(projectID, chapterID)
		if err != nil {
			return err
		}
		if !sameVersion(chapter.CurrentVersionID, result.BaseChapterVersionID) {
			return ErrVersionConflict
		}
		return nil
	})
	if err != nil {
		return WorkingCopy{}, err
	}
	if err := s.checkSources(ctx, actor, projectID, result.SourceIDs); err != nil {
		return WorkingCopy{}, err
	}
	return result, nil
}

func (s *Service) SaveWorkingCopy(ctx context.Context, actor Actor, projectID, chapterID, key string, input SaveWorkingCopyInput) (json.RawMessage, int, bool, error) {
	if input.ExpectedSpecRevision < 0 || input.ExpectedWorkingCopyRevision < 1 {
		return nil, 0, false, ErrInvalidRequest
	}
	declared, err := validateWorkingCopyBody(input.BodyMarkdown, input.SourceIDs)
	if err != nil {
		return nil, 0, false, err
	}
	input.SourceIDs = declared
	return s.operation(ctx, actor, "saveWorkingCopy", projectID+"/"+chapterID, key, input, projectID, "write:"+chapterID,
		func(tx Transaction, p Project) (any, int, error) {
			if p.Status != "active" || p.SpecRevision != input.ExpectedSpecRevision {
				return nil, 0, ErrVersionConflict
			}
			chapter, err := tx.Chapter(projectID, chapterID)
			if err != nil {
				return nil, 0, err
			}
			previous, err := tx.WorkingCopy(projectID, chapterID)
			if err != nil {
				return nil, 0, err
			}
			if !sameVersion(chapter.CurrentVersionID, input.BaseChapterVersionID) ||
				!sameVersion(previous.BaseChapterVersionID, input.BaseChapterVersionID) ||
				previous.SpecRevision != input.ExpectedSpecRevision ||
				previous.WorkingCopyRevision != input.ExpectedWorkingCopyRevision {
				return nil, 0, ErrVersionConflict
			}
			next := previous
			next.WorkingCopyRevision++
			next.BodyMarkdown = input.BodyMarkdown
			next.SourceIDs = slices.Clone(input.SourceIDs)
			// Review items are server-managed and survive autosave unchanged.
			next.UpdatedAt = time.Now().UTC()
			if err := tx.SaveWorkingCopy(previous, next); err != nil {
				return nil, 0, err
			}
			return next, 200, nil
		})
}

// ApplyRewrite replaces a selected range prepared by the rewrite application.
// Review items are merged server-side so a browser cannot erase or fabricate
// provenance while applying generated text.
func (s *Service) ApplyRewrite(ctx context.Context, actor Actor, projectID, chapterID, key string, input SaveWorkingCopyInput, reviewItems []ReviewItem) (json.RawMessage, int, bool, error) {
	if input.ExpectedSpecRevision < 0 || input.ExpectedWorkingCopyRevision < 1 {
		return nil, 0, false, ErrInvalidRequest
	}
	declared, err := validateWorkingCopyBody(input.BodyMarkdown, input.SourceIDs)
	if err != nil {
		return nil, 0, false, err
	}
	input.SourceIDs = declared
	for _, item := range reviewItems {
		if strings.TrimSpace(item.ID) == "" || strings.TrimSpace(item.Statement) == "" || strings.TrimSpace(item.OriginCandidateID) == "" {
			return nil, 0, false, ErrInvalidRequest
		}
	}
	body := ApplyRewriteInput{WorkingCopy: input, ReviewItems: slices.Clone(reviewItems)}
	return s.operation(ctx, actor, "applySelectedRewrite", projectID+"/"+chapterID, key, body, projectID, "write:"+chapterID,
		func(tx Transaction, p Project) (any, int, error) {
			if p.Status != "active" || p.SpecRevision != input.ExpectedSpecRevision {
				return nil, 0, ErrVersionConflict
			}
			chapter, err := tx.Chapter(projectID, chapterID)
			if err != nil {
				return nil, 0, err
			}
			previous, err := tx.WorkingCopy(projectID, chapterID)
			if err != nil {
				return nil, 0, err
			}
			if !sameVersion(chapter.CurrentVersionID, input.BaseChapterVersionID) ||
				!sameVersion(previous.BaseChapterVersionID, input.BaseChapterVersionID) ||
				previous.SpecRevision != input.ExpectedSpecRevision || previous.WorkingCopyRevision != input.ExpectedWorkingCopyRevision {
				return nil, 0, ErrVersionConflict
			}
			next := previous
			next.WorkingCopyRevision++
			next.SpecRevision = input.ExpectedSpecRevision
			next.BodyMarkdown = input.BodyMarkdown
			next.SourceIDs = slices.Clone(input.SourceIDs)
			seen := make(map[string]struct{}, len(next.ReviewItems)+len(reviewItems))
			for _, item := range next.ReviewItems {
				seen[item.ID] = struct{}{}
			}
			for _, item := range reviewItems {
				if _, exists := seen[item.ID]; exists {
					return nil, 0, ErrInvalidRequest
				}
				seen[item.ID] = struct{}{}
				next.ReviewItems = append(next.ReviewItems, item)
			}
			next.UpdatedAt = time.Now().UTC()
			if err := tx.SaveWorkingCopy(previous, next); err != nil {
				return nil, 0, err
			}
			return next, 200, nil
		})
}

func (s *Service) CommitWorkingCopy(ctx context.Context, actor Actor, projectID, chapterID, key string, input CommitWorkingCopyInput) (json.RawMessage, int, bool, error) {
	if input.ExpectedSpecRevision < 0 || input.ExpectedWorkingCopyRevision < 1 {
		return nil, 0, false, ErrInvalidRequest
	}
	return s.operation(ctx, actor, "commitWorkingCopy", projectID+"/"+chapterID, key, input, projectID, "write:"+chapterID,
		func(tx Transaction, p Project) (any, int, error) {
			if p.Status != "active" || p.SpecRevision != input.ExpectedSpecRevision {
				return nil, 0, ErrVersionConflict
			}
			chapter, err := tx.Chapter(projectID, chapterID)
			if err != nil {
				return nil, 0, err
			}
			workingCopy, err := tx.WorkingCopy(projectID, chapterID)
			if err != nil {
				return nil, 0, err
			}
			if !sameVersion(chapter.CurrentVersionID, input.ExpectedChapterVersionID) ||
				!sameVersion(workingCopy.BaseChapterVersionID, input.ExpectedChapterVersionID) ||
				workingCopy.SpecRevision != input.ExpectedSpecRevision ||
				workingCopy.WorkingCopyRevision != input.ExpectedWorkingCopyRevision {
				return nil, 0, ErrVersionConflict
			}
			id := uuid.NewString()
			nextChapter := chapter
			nextChapter.CurrentVersionID = &id
			nextChapter.BodyMarkdown = workingCopy.BodyMarkdown
			nextChapter.SourceIDs = slices.Clone(workingCopy.SourceIDs)
			nextChapter.ReviewItems = slices.Clone(workingCopy.ReviewItems)
			nextChapter.ConfirmationValid = false
			nextProject := p
			nextProject.ProjectVersion++
			if err := tx.UpdateProject(actor.TenantID, p, nextProject); err != nil {
				return nil, 0, err
			}
			if err := tx.AppendChapter(chapter, nextChapter, p.SpecRevision); err != nil {
				return nil, 0, err
			}
			nextCopy := workingCopy
			nextCopy.BaseChapterVersionID = &id
			nextCopy.WorkingCopyRevision++
			nextCopy.SpecRevision = p.SpecRevision
			nextCopy.UpdatedAt = time.Now().UTC()
			if err := tx.SaveWorkingCopy(workingCopy, nextCopy); err != nil {
				return nil, 0, err
			}
			return CommittedChapterVersion{
				ChapterVersionID: id, ParentChapterVersionID: chapter.CurrentVersionID,
				CommittedWorkingCopyRevision: workingCopy.WorkingCopyRevision,
				NextWorkingCopyRevision:      nextCopy.WorkingCopyRevision, SpecRevision: p.SpecRevision,
				BodyMarkdown: workingCopy.BodyMarkdown, SourceIDs: slices.Clone(workingCopy.SourceIDs),
				ReviewItems: slices.Clone(workingCopy.ReviewItems),
			}, 201, nil
		})
}

func (s *Service) ListChapterVersions(ctx context.Context, actor Actor, projectID, chapterID string) ([]ChapterVersion, error) {
	var result []ChapterVersion
	err := s.repository.Transaction(ctx, TransactionOptions{ReadOnly: true}, func(tx Transaction) error {
		if _, err := s.authorizeProject(ctx, tx, actor, projectID, "read"); err != nil {
			return err
		}
		if _, err := tx.Chapter(projectID, chapterID); err != nil {
			return err
		}
		var err error
		result, err = tx.ChapterVersions(projectID, chapterID)
		return err
	})
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0)
	for _, version := range result {
		ids = append(ids, version.SourceIDs...)
	}
	if err := s.checkSources(ctx, actor, projectID, ids); err != nil {
		return nil, err
	}
	return result, nil
}

func (s *Service) RestoreWorkingCopy(ctx context.Context, actor Actor, projectID, chapterID, key string, input RestoreWorkingCopyInput) (json.RawMessage, int, bool, error) {
	if strings.TrimSpace(input.ChapterVersionID) == "" || input.ExpectedSpecRevision < 0 || input.ExpectedWorkingCopyRevision < 1 {
		return nil, 0, false, ErrInvalidRequest
	}
	return s.operation(ctx, actor, "restoreWorkingCopy", projectID+"/"+chapterID, key, input, projectID, "write:"+chapterID,
		func(tx Transaction, p Project) (any, int, error) {
			if p.Status != "active" || p.SpecRevision != input.ExpectedSpecRevision {
				return nil, 0, ErrVersionConflict
			}
			chapter, err := tx.Chapter(projectID, chapterID)
			if err != nil {
				return nil, 0, err
			}
			workingCopy, err := tx.WorkingCopy(projectID, chapterID)
			if err != nil {
				return nil, 0, err
			}
			version, err := tx.ChapterVersion(projectID, chapterID, input.ChapterVersionID)
			if err != nil {
				return nil, 0, err
			}
			if !sameVersion(chapter.CurrentVersionID, input.ExpectedChapterVersionID) ||
				!sameVersion(workingCopy.BaseChapterVersionID, input.ExpectedChapterVersionID) ||
				workingCopy.WorkingCopyRevision != input.ExpectedWorkingCopyRevision ||
				workingCopy.SpecRevision != input.ExpectedSpecRevision {
				return nil, 0, ErrVersionConflict
			}
			next := workingCopy
			next.WorkingCopyRevision++
			next.SpecRevision = p.SpecRevision
			next.BodyMarkdown = version.BodyMarkdown
			next.SourceIDs = slices.Clone(version.SourceIDs)
			next.ReviewItems = slices.Clone(version.ReviewItems)
			next.UpdatedAt = time.Now().UTC()
			if err := tx.SaveWorkingCopy(workingCopy, next); err != nil {
				return nil, 0, err
			}
			return next, 200, nil
		})
}
