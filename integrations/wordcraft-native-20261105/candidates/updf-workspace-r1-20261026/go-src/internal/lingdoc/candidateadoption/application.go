package candidateadoption

import "context"

// Application is the transport-independent candidate adoption/read boundary.
type Application interface {
	AcceptCandidate(context.Context, AcceptCandidateInput) (AcceptResult, error)
	ReadCandidate(context.Context, string, string, string) (Candidate, error)
	ReadChapters(context.Context, string, string) ([]Chapter, error)
}
type ConfirmationApplication interface {
	ConfirmChapter(context.Context, ConfirmChapterInput) (Confirmation, bool, error)
}

func (s *CandidateAdoptionService) ReadCandidate(ctx context.Context, actor, projectID, candidateID string) (Candidate, error) {
	if s.Candidates == nil {
		return Candidate{}, ErrInvalidState
	}
	if err := s.authorize(ctx, actor, projectID, "read"); err != nil {
		return Candidate{}, err
	}
	candidate, err := s.Candidates.GetCandidate(ctx, projectID, candidateID)
	if err != nil {
		return Candidate{}, err
	}
	if err := s.validateSources(ctx, projectID, actor, candidate.SourceIDs); err != nil {
		return Candidate{}, err
	}
	return candidate, nil
}
func (s *CandidateAdoptionService) ReadChapters(ctx context.Context, actor, projectID string) ([]Chapter, error) {
	reader, ok := s.Workspace.(ChapterReader)
	if !ok {
		return nil, ErrInvalidState
	}
	if err := s.authorize(ctx, actor, projectID, "read"); err != nil {
		return nil, err
	}
	chapters, err := reader.ListChapters(ctx, projectID)
	if err != nil {
		return nil, err
	}
	for _, chapter := range chapters {
		if err := s.validateSources(ctx, projectID, actor, chapter.SourceIDs); err != nil {
			return nil, err
		}
	}
	return chapters, nil
}

var _ Application = (*CandidateAdoptionService)(nil)
var _ ConfirmationApplication = (*ConfirmationService)(nil)
