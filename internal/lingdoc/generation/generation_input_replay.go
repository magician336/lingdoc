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

// replayNeedsCurrentness says whether the currentness check still governs the
// replay of a run in this state.
//
// The check exists to stop a generation that is ABOUT TO RUN from being based
// on inputs that have moved on since they were captured. A terminal run has
// already run: its outcome is a fact, and replaying it only reads that fact
// back, so the check has nothing left to protect. The runs that can still
// turn into a provider call keep the check, so a stale queued run is refused
// before anything is enqueued.
//
// The terminal states are the ones the repository can no longer claim: only a
// queued run is claimable, so a succeeded, failed or interrupted run (an
// interrupted run is how this codebase records a cancellation) can never reach
// the model again.
func replayNeedsCurrentness(status Status) bool {
	switch status {
	case StatusSucceeded, StatusFailed, StatusInterrupted:
		return false
	default:
		return true
	}
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
	if s.Sources == nil {
		return ErrDependencyUnavailable
	}
	// The currentness capability is read through the service's own seam, the
	// same one Execute and the candidate validity read. The production
	// assembly carries the shared access policy, and the legacy currentness
	// port only exists for adapters that are migrated independently; reading
	// the port directly made every production replay answer "dependency
	// unavailable" (503) before it had even looked at the run.
	if replayNeedsCurrentness(run.Status) {
		current, err := s.current(ctx, actor, projectID, run.ChapterID, input.Basis)
		if err != nil {
			return err
		}
		if !current {
			return ErrStaleInput
		}
	}
	if err := s.Sources.ValidateGenerationSources(ctx, actor, projectID, input.Sources); err != nil {
		return err
	}
	return s.validateEvidence(ctx, actor, projectID, run.ChapterID, input)
}
func copyEvidenceBasis(input *Input, refs []EvidenceCardRef) {
	input.Basis.EvidenceCardRefs = nil
	for _, ref := range refs {
		input.Basis.EvidenceCardRefs = append(input.Basis.EvidenceCardRefs, core.VersionRef{Kind: "EvidenceCard", ID: ref.ID, Version: ref.Version})
	}
	input.Workspace.Basis = input.Basis
}
