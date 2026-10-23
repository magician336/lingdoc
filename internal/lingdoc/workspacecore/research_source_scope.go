package workspacecore

import "context"

// readResearchSource reads one research source, with the chapter scope when
// the operation has one.
//
// A chapter-scoped read goes through the stronger ChapterSourceReader seam: an
// adapter that does not implement it must not fall back to a project-wide read,
// because that would widen the caller's scope.
func (s *Service) readResearchSource(ctx context.Context, actor Actor, projectID, chapterID, sourceID string) (ResearchSource, error) {
	if s == nil || s.agentRuntime.Sources == nil {
		return ResearchSource{}, ErrDependencyUnavailable
	}
	if chapterID == "" {
		return s.agentRuntime.Sources.ReadResearchSource(ctx, actor, projectID, sourceID)
	}
	reader, ok := s.agentRuntime.Sources.(ChapterSourceReader)
	if !ok {
		return ResearchSource{}, ErrPermissionSchemaUnavailable
	}
	return reader.ReadResearchSourceForChapter(ctx, actor, projectID, chapterID, sourceID)
}

// checkAssetScope checks the asset scope of one operation, with the chapter
// scope when the operation has one.
func (s *Service) checkAssetScope(ctx context.Context, actor Actor, projectID, chapterID string, assetIDs []string) ([]ProjectAssetVersion, error) {
	if s == nil || s.agentRuntime.Sources == nil {
		return nil, ErrDependencyUnavailable
	}
	if chapterID == "" {
		return s.agentRuntime.Sources.CheckAssetScope(ctx, actor, projectID, assetIDs)
	}
	reader, ok := s.agentRuntime.Sources.(ChapterSourceReader)
	if !ok {
		return nil, ErrPermissionSchemaUnavailable
	}
	return reader.CheckAssetScopeForChapter(ctx, actor, projectID, chapterID, assetIDs)
}
