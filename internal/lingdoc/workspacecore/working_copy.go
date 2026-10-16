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
	var recovery *WorkingCopy
	err := s.repository.Transaction(ctx, TransactionOptions{ReadOnly: true}, func(tx Transaction) error {
		project, err := s.authorizeProject(ctx, tx, authorizationRequest(actor, projectID, AuthorizationRead, ""))
		if err != nil {
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
			// Repair legacy adoption rows only when the draft is identical to
			// its old formal base. Never discard independent draft edits.
			var base ChapterVersion
			if result.BaseChapterVersionID != nil {
				base, err = tx.ChapterVersion(projectID, chapterID, *result.BaseChapterVersionID)
				if err != nil {
					return err
				}
			}
			if result.BodyMarkdown != base.BodyMarkdown || !slices.Equal(result.SourceIDs, base.SourceIDs) ||
				!slices.Equal(result.ReviewItems, base.ReviewItems) || !slices.Equal(result.CitationUsages, base.CitationUsages) {
				return ErrVersionConflict
			}
			next := result
			next.BaseChapterVersionID = chapter.CurrentVersionID
			next.SpecRevision = project.SpecRevision
			next.WorkingCopyRevision++
			next.BodyMarkdown = chapter.BodyMarkdown
			next.SourceIDs = slices.Clone(chapter.SourceIDs)
			next.CitationUsages = slices.Clone(chapter.CitationUsages)
			next.ReviewItems = slices.Clone(chapter.ReviewItems)
			next.UpdatedAt = time.Now().UTC()
			recovery = &next
		}
		return nil
	})
	if err != nil {
		return WorkingCopy{}, err
	}
	if recovery != nil {
		// Source policy uses its own connection. Never invoke it while a database
		// transaction is holding the only SQLite connection.
		if err := s.checkSources(ctx, actor, projectID, recovery.SourceIDs); err != nil {
			return WorkingCopy{}, err
		}
		err = s.repository.Transaction(ctx, TransactionOptions{}, func(tx Transaction) error {
			project, err := s.authorizeProject(ctx, tx, authorizationRequest(actor, projectID, AuthorizationRead, ""))
			if err != nil {
				return err
			}
			chapter, err := tx.Chapter(projectID, chapterID)
			if err != nil {
				return err
			}
			if project.SpecRevision != recovery.SpecRevision || !sameVersion(chapter.CurrentVersionID, recovery.BaseChapterVersionID) {
				return ErrVersionConflict
			}
			// The repository CAS also matches the observed working-copy revision,
			// spec and base version. An intervening recovery or draft save returns
			// ErrVersionConflict instead of overwriting the newer row.
			return tx.SaveWorkingCopy(result, *recovery)
		})
		if err != nil {
			return WorkingCopy{}, err
		}
		result = *recovery
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
	usages, err := normalizeCitationUsages(input.CitationUsages, declared)
	if err != nil {
		return nil, 0, false, err
	}
	input.SourceIDs = declared
	return runOperation(s, ctx, actor, "saveWorkingCopy", projectID+"/"+chapterID, key, input, authorizationRequest(actor, projectID, AuthorizationWrite, chapterID), focusedMutationPort[chapterMutationTransaction],
		func(tx chapterMutationTransaction, p Project) (any, int, error) {
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
			next.CitationUsages = usages
			if input.CitationUsages == nil {
				next.CitationUsages = []CitationUsage{}
				for _, usage := range previous.CitationUsages {
					if slices.Contains(declared, usage.SourceID) {
						next.CitationUsages = append(next.CitationUsages, usage)
					}
				}
				next.CitationUsages = completeCitationUsages(next.CitationUsages, declared)
			}
			// Review items are server-managed and survive autosave unchanged.
			next.UpdatedAt = time.Now().UTC()
			if err := tx.SaveWorkingCopy(previous, next); err != nil {
				return nil, 0, err
			}
			return next, 200, nil
		})
}

// SaveWorkingCopyResult exposes the updated working copy without transport
// fields.
func (s *Service) SaveWorkingCopyResult(ctx context.Context, actor Actor, projectID, chapterID, key string, input SaveWorkingCopyInput) (WorkingCopyMutationResult, error) {
	workingCopy, replayed, err := adaptMutationResult[WorkingCopy](func() (json.RawMessage, int, bool, error) {
		return s.SaveWorkingCopy(ctx, actor, projectID, chapterID, key, input)
	}, "working copy mutation result")
	if err != nil {
		return WorkingCopyMutationResult{Replayed: replayed}, err
	}
	return WorkingCopyMutationResult{WorkingCopy: workingCopy, Replayed: replayed}, nil
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
	usages, err := normalizeCitationUsages(input.CitationUsages, declared)
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
	return runOperation(s, ctx, actor, "applySelectedRewrite", projectID+"/"+chapterID, key, body, authorizationRequest(actor, projectID, AuthorizationWrite, chapterID), focusedMutationPort[chapterMutationTransaction],
		func(tx chapterMutationTransaction, p Project) (any, int, error) {
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
			next.CitationUsages = usages
			if input.CitationUsages == nil {
				next.CitationUsages = []CitationUsage{}
				for _, usage := range previous.CitationUsages {
					if slices.Contains(declared, usage.SourceID) {
						next.CitationUsages = append(next.CitationUsages, usage)
					}
				}
				next.CitationUsages = completeCitationUsages(next.CitationUsages, declared)
			}
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

// ApplyRewriteResult exposes the updated working copy without transport
// fields.
func (s *Service) ApplyRewriteResult(ctx context.Context, actor Actor, projectID, chapterID, key string, input SaveWorkingCopyInput, reviewItems []ReviewItem) (WorkingCopyMutationResult, error) {
	workingCopy, replayed, err := adaptMutationResult[WorkingCopy](func() (json.RawMessage, int, bool, error) {
		return s.ApplyRewrite(ctx, actor, projectID, chapterID, key, input, reviewItems)
	}, "working copy mutation result")
	if err != nil {
		return WorkingCopyMutationResult{Replayed: replayed}, err
	}
	return WorkingCopyMutationResult{WorkingCopy: workingCopy, Replayed: replayed}, nil
}

func (s *Service) CommitWorkingCopy(ctx context.Context, actor Actor, projectID, chapterID, key string, input CommitWorkingCopyInput) (json.RawMessage, int, bool, error) {
	if input.ExpectedSpecRevision < 0 || input.ExpectedWorkingCopyRevision < 1 {
		return nil, 0, false, ErrInvalidRequest
	}
	return runOperation(s, ctx, actor, "commitWorkingCopy", projectID+"/"+chapterID, key, input, authorizationRequest(actor, projectID, AuthorizationWrite, chapterID), focusedMutationPort[chapterCommitTransaction],
		func(tx chapterCommitTransaction, p Project) (any, int, error) {
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
			nextChapter.CitationUsages = slices.Clone(workingCopy.CitationUsages)
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

// CommitWorkingCopyResult exposes the committed chapter version without
// transport fields.
func (s *Service) CommitWorkingCopyResult(ctx context.Context, actor Actor, projectID, chapterID, key string, input CommitWorkingCopyInput) (CommittedChapterMutationResult, error) {
	version, replayed, err := adaptMutationResult[CommittedChapterVersion](func() (json.RawMessage, int, bool, error) {
		return s.CommitWorkingCopy(ctx, actor, projectID, chapterID, key, input)
	}, "committed chapter result")
	if err != nil {
		return CommittedChapterMutationResult{Replayed: replayed}, err
	}
	return CommittedChapterMutationResult{Version: version, Replayed: replayed}, nil
}

func (s *Service) ListChapterVersions(ctx context.Context, actor Actor, projectID, chapterID string) ([]ChapterVersion, error) {
	var result []ChapterVersion
	err := s.repository.Transaction(ctx, TransactionOptions{ReadOnly: true}, func(tx Transaction) error {
		if _, err := s.authorizeProject(ctx, tx, authorizationRequest(actor, projectID, AuthorizationRead, "")); err != nil {
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
	return runOperation(s, ctx, actor, "restoreWorkingCopy", projectID+"/"+chapterID, key, input, authorizationRequest(actor, projectID, AuthorizationWrite, chapterID), focusedMutationPort[chapterMutationTransaction],
		func(tx chapterMutationTransaction, p Project) (any, int, error) {
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
			next.CitationUsages = slices.Clone(version.CitationUsages)
			next.ReviewItems = slices.Clone(version.ReviewItems)
			next.UpdatedAt = time.Now().UTC()
			if err := tx.SaveWorkingCopy(workingCopy, next); err != nil {
				return nil, 0, err
			}
			return next, 200, nil
		})
}

// RestoreWorkingCopyResult exposes the restored working copy without
// transport fields.
func (s *Service) RestoreWorkingCopyResult(ctx context.Context, actor Actor, projectID, chapterID, key string, input RestoreWorkingCopyInput) (WorkingCopyMutationResult, error) {
	workingCopy, replayed, err := adaptMutationResult[WorkingCopy](func() (json.RawMessage, int, bool, error) {
		return s.RestoreWorkingCopy(ctx, actor, projectID, chapterID, key, input)
	}, "working copy mutation result")
	if err != nil {
		return WorkingCopyMutationResult{Replayed: replayed}, err
	}
	return WorkingCopyMutationResult{WorkingCopy: workingCopy, Replayed: replayed}, nil
}
