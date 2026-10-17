package generation

import (
	"context"
	"encoding/json"
	"errors"
	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
	"gorm.io/gorm"
)

// ReplayInputReader exposes immutable server input only inside the application;
// it never appears in HTTP responses.
type ReplayInputReader interface {
	LoadGenerationInput(context.Context, Actor, string, string) (Input, error)
}

func (s *SQLiteRepository) LoadGenerationInput(ctx context.Context, actor Actor, projectID, runID string) (Input, error) {
	var row generationRow
	err := s.db.WithContext(ctx).Where("id = ? AND tenant_id = ? AND actor_id = ? AND project_id = ?", runID, actor.TenantID, actor.UserID, projectID).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return Input{}, ErrNotFound
	}
	if err != nil {
		return Input{}, err
	}
	var input Input
	if err := json.Unmarshal([]byte(row.InputJSON), &input); err != nil {
		return Input{}, ErrStaleInput
	}
	return input, nil
}
func (s *Service) validateReplayInput(ctx context.Context, actor Actor, projectID string, run Run) error {
	reader, ok := s.Repository.(ReplayInputReader)
	if !ok {
		return ErrDependencyUnavailable
	}
	input, err := reader.LoadGenerationInput(ctx, actor, projectID, run.ID)
	if err != nil {
		return err
	}
	if !input.HumanInitiated || input.Actor.TenantID != actor.TenantID || input.Actor.UserID != actor.UserID || input.Workspace.ProjectID != projectID || input.Workspace.ChapterID != run.ChapterID {
		return ErrForbidden
	}
	if s.Currentness == nil || s.Sources == nil {
		return ErrDependencyUnavailable
	}
	current, err := s.Currentness.GenerationInputIsCurrent(ctx, actor, projectID, run.ChapterID, input.Basis)
	if err != nil {
		return err
	}
	if !current {
		return ErrStaleInput
	}
	if err := s.Sources.ValidateGenerationSources(ctx, actor, projectID, input.Sources); err != nil {
		return err
	}
	return s.validateEvidence(ctx, actor, projectID, run.ChapterID, input)
}
func copyEvidenceBasis(input *Input, refs []EvidenceCardRef) {
	input.Basis.EvidenceCardRefs = nil
	for _, ref := range refs {
		input.Basis.EvidenceCardRefs = append(input.Basis.EvidenceCardRefs, core.G2VersionRef{Kind: "EvidenceCard", ID: ref.ID, Version: ref.Version})
	}
	input.Workspace.Basis = input.Basis
}
