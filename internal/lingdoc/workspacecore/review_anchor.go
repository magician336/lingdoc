package workspacecore

import (
	"context"
	"encoding/json"
	"github.com/Tencent/WeKnora/internal/lingdoc/reviewanchor"
	"reflect"
	"slices"
	"strings"
	"time"
)

type SetReviewAnchorInput struct {
	ExpectedChapterVersionID    *string                 `json:"expected_chapter_version_id"`
	ExpectedSpecRevision        int64                   `json:"expected_spec_revision"`
	ExpectedWorkingCopyRevision int64                   `json:"expected_working_copy_revision"`
	Anchor                      *reviewanchor.Selection `json:"anchor"`
}

func rebaseReviewItems(oldBody, body string, items []ReviewItem, revision int64, version *string) []ReviewItem {
	out := slices.Clone(items)
	for i := range out {
		out[i].Anchor = reviewanchor.Rebase(oldBody, body, items[i].Anchor, revision, version)
	}
	return out
}

func (s *Service) SetReviewAnchor(ctx context.Context, actor Actor, projectID, chapterID, itemID, key string, input SetReviewAnchorInput) (json.RawMessage, int, bool, error) {
	if itemID == "" || input.ExpectedSpecRevision < 0 || input.ExpectedWorkingCopyRevision < 1 {
		return nil, 0, false, ErrInvalidRequest
	}
	return s.operation(ctx, actor, "setReviewAnchor", projectID+"/"+chapterID+"/"+itemID, key, input, projectID, "write:"+chapterID, func(tx Transaction, p Project) (any, int, error) {
		chapter, err := tx.Chapter(projectID, chapterID)
		if err != nil {
			return nil, 0, err
		}
		previous, err := tx.WorkingCopy(projectID, chapterID)
		if err != nil {
			return nil, 0, err
		}
		if p.Status != "active" || p.SpecRevision != input.ExpectedSpecRevision || previous.SpecRevision != input.ExpectedSpecRevision || previous.WorkingCopyRevision != input.ExpectedWorkingCopyRevision || !sameVersion(chapter.CurrentVersionID, input.ExpectedChapterVersionID) || !sameVersion(previous.BaseChapterVersionID, input.ExpectedChapterVersionID) {
			return nil, 0, ErrVersionConflict
		}
		if input.Anchor != nil && !reviewanchor.Valid(previous.BodyMarkdown, *input.Anchor) {
			return nil, 0, ErrInvalidRequest
		}
		next := previous
		next.WorkingCopyRevision++
		next.UpdatedAt = time.Now().UTC()
		next.ReviewItems = rebaseReviewItems(previous.BodyMarkdown, previous.BodyMarkdown, previous.ReviewItems, next.WorkingCopyRevision, next.BaseChapterVersionID)
		found := false
		for i := range next.ReviewItems {
			if next.ReviewItems[i].ID == itemID {
				found = true
				if input.Anchor == nil {
					next.ReviewItems[i].Anchor = nil
				} else {
					next.ReviewItems[i].Anchor = reviewanchor.New(*input.Anchor, next.WorkingCopyRevision, next.BaseChapterVersionID)
				}
			}
		}
		if !found {
			return nil, 0, ErrNotFound
		}
		if err := tx.SaveWorkingCopy(previous, next); err != nil {
			return nil, 0, err
		}
		return next, 200, nil
	})
}

type confirmationReadTransaction interface {
	CurrentConfirmation(string, string, string, int64, string) (json.RawMessage, error)
}

// Raw details preserve the complete existing confirmation/decisions contract.
func (s *Service) GetCurrentConfirmation(ctx context.Context, actor Actor, projectID, chapterID string) (json.RawMessage, error) {
	result := json.RawMessage("null")
	var sources []string
	err := s.repository.Transaction(ctx, TransactionOptions{ReadOnly: true}, func(tx Transaction) error {
		p, err := s.authorizeProject(ctx, tx, actor, projectID, "read")
		if err != nil {
			return err
		}
		ch, err := tx.Chapter(projectID, chapterID)
		if err != nil {
			return err
		}
		sources = slices.Clone(ch.SourceIDs)
		if ch.CurrentVersionID == nil || !ch.ConfirmationValid {
			return nil
		}
		reader, ok := tx.(confirmationReadTransaction)
		if !ok {
			return ErrInvalidState
		}
		result, err = reader.CurrentConfirmation(projectID, chapterID, *ch.CurrentVersionID, p.SpecRevision, p.TemplateVersion)
		if err != nil {
			return err
		}
		if string(result) != "null" {
			var record struct {
				Decisions []struct {
					ID          string `json:"review_item_id"`
					Disposition string `json:"disposition"`
					Reason      string `json:"reason"`
				} `json:"review_decisions"`
			}
			if err := json.Unmarshal(result, &record); err != nil {
				return err
			}
			pending := map[string]bool{}
			for _, item := range ch.ReviewItems {
				pending[item.ID] = true
			}
			valid := len(record.Decisions) == len(ch.ReviewItems)
			for _, decision := range record.Decisions {
				if !pending[decision.ID] || (decision.Disposition != "resolved" && decision.Disposition != "retained_warning") || strings.TrimSpace(decision.Reason) == "" {
					valid = false
				}
				delete(pending, decision.ID)
			}
			if !valid || len(pending) > 0 {
				result = json.RawMessage("null")
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if err := s.checkSources(ctx, actor, projectID, sources); err != nil {
		return nil, err
	}
	return result, nil
}

func (t gormTransaction) CurrentConfirmation(projectID, chapterID, versionID string, specRevision int64, templateVersion string) (json.RawMessage, error) {
	var rows []chapterConfirmationRow
	if err := t.db.Where("chapter_id = ? AND chapter_version_id = ? AND valid = ?", chapterID, versionID, true).Order("created_at DESC, id DESC").Find(&rows).Error; err != nil {
		return nil, err
	}
	for _, row := range rows {
		var meta struct {
			ID               string `json:"id"`
			ChapterID        string `json:"chapter_id"`
			ChapterVersionID string `json:"chapter_version_id"`
			SpecRevision     int64  `json:"spec_revision"`
			TemplateVersion  string `json:"template_version"`
			Valid            bool   `json:"valid"`
		}
		if err := json.Unmarshal([]byte(row.DetailsJSON), &meta); err != nil {
			return nil, err
		}
		if meta.Valid && meta.ID == row.ID && meta.ChapterID == chapterID && meta.ChapterVersionID == versionID && meta.SpecRevision == specRevision && meta.TemplateVersion == templateVersion {
			// Legacy zero-item records may encode null; normalize this view only.
			// Stored confirmations/digests and nonempty decisions are never rewritten.
			var view map[string]json.RawMessage
			if err := json.Unmarshal([]byte(row.DetailsJSON), &view); err != nil {
				return nil, err
			}
			if len(view["review_decisions"]) == 0 || string(view["review_decisions"]) == "null" {
				view["review_decisions"] = json.RawMessage("[]")
				return json.Marshal(view)
			}
			return json.RawMessage(row.DetailsJSON), nil
		}
	}
	return json.RawMessage("null"), nil
}

// Copy revision advances on save/commit without changing the review meaning.
func reviewItemsEqual(a, b ReviewItem) bool {
	if a.ID != b.ID || a.Statement != b.Statement || a.OriginCandidateID != b.OriginCandidateID {
		return false
	}
	if a.Anchor == nil || b.Anchor == nil {
		return a.Anchor == nil && b.Anchor == nil
	}
	left, right := *a.Anchor, *b.Anchor
	left.WorkingCopyRevision = 0
	right.WorkingCopyRevision = 0
	return reflect.DeepEqual(left, right)
}
