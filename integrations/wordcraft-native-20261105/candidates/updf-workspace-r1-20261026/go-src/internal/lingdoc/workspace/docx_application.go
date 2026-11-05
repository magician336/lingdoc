package workspace

import (
	"context"
	"errors"

	adoption "github.com/Tencent/WeKnora/internal/lingdoc/candidateadoption"
	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
)

type DOCXChapterConfirmationRequest struct {
	ProjectID                   string                    `json:"-"`
	SavedVersionID              string                    `json:"savedVersionId"`
	ExpectedDocSHA256           string                    `json:"expectedDocSha256"`
	ExpectedManifestHash        string                    `json:"expectedManifestHash"`
	ExpectedSectionDeliveryHash string                    `json:"expectedSectionDeliveryHash"`
	SectionID                   string                    `json:"sectionId"`
	IdempotencyKey              string                    `json:"-"`
	ExpectedSpecRevision        int                       `json:"expectedSpecRevision"`
	Decisions                   []adoption.ReviewDecision `json:"decisions"`
}

// ConfirmSavedDOCXChapter resolves all guard facts from the server-side saved
// DOCX mapping and current project binding. Browser expectations are compared,
// never trusted as proof.
func (r *SourceRuntime) ConfirmSavedDOCXChapter(ctx context.Context, actor Actor, request DOCXChapterConfirmationRequest) (adoption.Confirmation, bool, error) {
	var empty adoption.Confirmation
	if request.ProjectID == "" || request.SavedVersionID == "" || request.SectionID == "" {
		return empty, false, errors.New("DOCX_CONFIRM_MAPPING_MISMATCH")
	}
	store := NewDOCXVersionStore(r.db)
	version, err := store.Get(ctx, request.ProjectID, request.SavedVersionID)
	if err != nil {
		return empty, false, err
	}
	if version.DocSHA256 != request.ExpectedDocSHA256 || version.ManifestHash != request.ExpectedManifestHash {
		return empty, false, errors.New("VERSION_CHANGED")
	}
	found := false
	for _, section := range version.Projection.Sections {
		if section.SectionID == request.SectionID {
			found = section.SectionDeliveryHash == request.ExpectedSectionDeliveryHash
		}
	}
	if !found {
		return empty, false, errors.New("VERSION_CHANGED")
	}
	guard, err := r.guardForSavedVersion(ctx, actor, request.ProjectID, request.SavedVersionID, "", "write_check", []string{request.SectionID})
	if err != nil {
		return empty, false, err
	}
	if len(guard.Chapters) != 1 {
		return empty, false, errors.New("DOCX_CONFIRM_MAPPING_MISMATCH")
	}
	guard.Capability = "confirm:" + guard.Chapters[0].ChapterID
	guard.SavedVersionID = request.SavedVersionID
	input := adoption.ConfirmChapterInput{ProjectID: request.ProjectID, ChapterID: guard.Chapters[0].ChapterID, ActorID: actor.UserID, IdempotencyKey: request.IdempotencyKey, ExpectedChapterVersionID: guard.Chapters[0].ChapterVersionID, ExpectedSpecRevision: request.ExpectedSpecRevision, Decisions: request.Decisions}
	return r.ConfirmDOCXChapter(ctx, actor, guard, input)
}

func (r *SourceRuntime) guardForSavedVersion(ctx context.Context, actor Actor, projectID, savedVersionID, capability, scene string, sectionIDs []string) (DOCXGuardInput, error) {
	store := NewDOCXVersionStore(r.db)
	_, err := store.Get(ctx, projectID, savedVersionID)
	if err != nil {
		return DOCXGuardInput{}, err
	}
	chapters, err := store.GuardChapters(ctx, savedVersionID, sectionIDs)
	if err != nil {
		return DOCXGuardInput{}, err
	}
	runtime := r.CloneWithDatabase(r.db)
	projects := core.NewServiceWithSources(core.NewGORMRepository(r.db), r.templates, runtime.WorkspaceSourcePolicy())
	runtime.ConnectProjects(projects)
	runtime.EnableFullWorkflowPolicy()
	project, err := projects.GetProject(ctx, actor, projectID)
	if err != nil {
		return DOCXGuardInput{}, err
	}
	if project.TemplateCopy == nil {
		return DOCXGuardInput{}, errors.New("DOCX_BINDING_VERSION_CHANGED")
	}
	return DOCXGuardInput{ProjectID: projectID, Capability: capability, Scene: scene, ExpectedTemplateCopyVersion: project.TemplateCopy.Version, ExpectedRuleSetHash: project.TemplateCopy.RulesetHash, Chapters: chapters}, nil
}
