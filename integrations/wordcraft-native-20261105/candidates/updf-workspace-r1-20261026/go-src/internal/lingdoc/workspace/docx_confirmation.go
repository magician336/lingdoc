package workspace

import (
	"context"
	"errors"

	adoption "github.com/Tencent/WeKnora/internal/lingdoc/candidateadoption"
	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
	"gorm.io/gorm"
)

type docxTransactionAuthorizer struct {
	projects *core.Service
	tx       *gorm.DB
	actor    Actor
}

func (a docxTransactionAuthorizer) Authorize(ctx context.Context, user, project, capability string) error {
	if user != a.actor.UserID {
		return core.ErrNotFound
	}
	_, err := a.projects.AuthorizeInTransaction(ctx, core.NewTransactionAdapter(a.tx), a.actor, project, capability)
	return err
}

// ConfirmDOCXChapter connects the verified DOCX projection to the EXISTING
// ConfirmationService/store. The DOCX importer, not a browser, supplies guard.
// Production HTTP wiring must resolve guard from its immutable DOCX mapping.
func (r *SourceRuntime) ConfirmDOCXChapter(ctx context.Context, actor Actor, guard DOCXGuardInput, input adoption.ConfirmChapterInput) (adoption.Confirmation, bool, error) {
	var result adoption.Confirmation
	var replayed bool
	if len(guard.Chapters) != 1 || guard.ProjectID != input.ProjectID || guard.Chapters[0].ChapterID != input.ChapterID || guard.Chapters[0].ChapterVersionID != input.ExpectedChapterVersionID || input.ActorID != actor.UserID || guard.Capability != "confirm:"+input.ChapterID || guard.Scene != "write_check" {
		return result, false, errors.New("DOCX_CONFIRM_MAPPING_MISMATCH")
	}
	// A lost response must be recoverable after the document or binding moves on.
	// Reauthorize first, then replay the immutable receipt before current-state guards.
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		runtime := r.CloneWithDatabase(tx)
		projects := core.NewServiceWithSources(core.NewGORMRepository(tx), r.templates, runtime.WorkspaceSourcePolicy())
		runtime.ConnectProjects(projects)
		runtime.EnableFullWorkflowPolicy()
		if err := (docxTransactionAuthorizer{projects, tx, actor}).Authorize(ctx, input.ActorID, input.ProjectID, "confirm:"+input.ChapterID); err != nil {
			return err
		}
		previous, err := adoption.NewSQLiteCandidateAdoptionStore(tx).ReplayConfirmation(ctx, input)
		if err != nil {
			return err
		}
		if previous != nil {
			result, replayed = *previous, true
		}
		return nil
	})
	if err != nil || replayed {
		return result, replayed, err
	}
	err = r.GuardDOCX(ctx, actor, guard, func(tx *gorm.DB, proof DOCXGuardProof) error {
		if guard.SavedVersionID != "" {
			latest, latestErr := NewDOCXVersionStore(tx).Latest(ctx, guard.ProjectID)
			if latestErr != nil {
				return latestErr
			}
			if latest.ID != guard.SavedVersionID {
				return errors.New("VERSION_CHANGED")
			}
		}
		runtime := r.CloneWithDatabase(tx)
		projects := core.NewServiceWithSources(core.NewGORMRepository(tx), r.templates, runtime.WorkspaceSourcePolicy())
		runtime.ConnectProjects(projects)
		runtime.EnableFullWorkflowPolicy()
		policy, ok := runtime.CandidateAdoptionSourcePolicy().(adoption.ConfirmationSourcePolicy)
		if !ok {
			return errors.New("DOCX_CONFIRM_SOURCE_POLICY_MISSING")
		}
		service := adoption.NewConfirmationService(adoption.NewSQLiteCandidateAdoptionStore(tx), policy, docxTransactionAuthorizer{projects, tx, actor})
		var err error
		result, replayed, err = service.ConfirmChapter(ctx, input)
		return err
	})
	return result, replayed, err
}
