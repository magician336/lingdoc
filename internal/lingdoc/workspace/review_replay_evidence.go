package workspace

import (
	"context"
	"encoding/json"
	"reflect"
	"slices"

	"github.com/Tencent/WeKnora/internal/lingdoc/candidateadoption"
)

// validateReviewReplay rechecks the actual stored result, not the sources
// newly resolved from an optional-context request after permissions changed.
func (s *SelectedRewriteService) validateReviewReplay(ctx context.Context, actor Actor, projectID string, row reviewReportRow) error {
	if row.ActorID != actor.UserID {
		return ErrReviewNotAuthorized
	}
	return s.validateStoredReviewEvidence(ctx, actor, projectID, row, "write:"+row.ChapterID)
}

func (s *SelectedRewriteService) validateStoredReviewEvidence(ctx context.Context, actor Actor, projectID string, row reviewReportRow, capability string) error {
	if row.TenantID != actor.TenantID || row.ProjectID != projectID || row.ChapterID == "" {
		return ErrReviewNotAuthorized
	}
	if err := s.workspace.Authorize(ctx, actor, projectID, capability); err != nil {
		return ErrReviewNotAuthorized
	}
	refs, err := decodeRewriteEvidenceRefs(row.EvidenceCardRefsJSON)
	if err != nil {
		return ErrReviewNotAuthorized
	}
	savedConstraints, err := decodeRewriteEvidenceConstraints(row.EvidenceConstraintsJSON)
	if err != nil {
		return ErrReviewNotAuthorized
	}
	var snapshots []RewriteSourceSnapshot
	var sourceIDs []string
	if json.Unmarshal([]byte(row.AuthorizedSourcesJSON), &snapshots) != nil || json.Unmarshal([]byte(row.SourceIDsJSON), &sourceIDs) != nil {
		return ErrReviewNotAuthorized
	}
	snapshotIDs := make([]string, 0, len(snapshots))
	for _, snapshot := range snapshots {
		snapshotIDs = append(snapshotIDs, snapshot.SourceID)
	}
	if len(stringSet(snapshotIDs)) != len(snapshotIDs) || len(stringSet(sourceIDs)) != len(sourceIDs) || !slices.Equal(stringSet(snapshotIDs), stringSet(sourceIDs)) {
		return ErrReviewNotAuthorized
	}
	var findings []candidateadoption.ReviewItem
	if err = json.Unmarshal([]byte(row.FindingsJSON), &findings); err != nil {
		return ErrReviewNotAuthorized
	}
	baseline := ReviewBaseline{WorkingCopyRevision: row.WorkingCopyRevision, BaseChapterVersionID: row.BaseChapterVersionID, SpecRevision: row.SpecRevision, BodyHash: row.BodyHash}
	version, err := reviewReportVersion(findings, baseline, candidateadoption.RunMode(row.RunMode), sourceIDs)
	if err != nil || version != row.ReportVersion {
		return ErrReviewNotAuthorized
	}
	if len(refs) == 0 {
		if len(savedConstraints) != 0 {
			return ErrReviewNotAuthorized
		}
	} else {
		selected, err := selectFormalEvidence(ctx, s.selector, actor, projectID, row.ChapterID, evidenceCardVersionRefs(refs))
		if err != nil {
			return ErrReviewNotAuthorized
		}
		constraints, err := selectedEvidenceConstraints(selected, refs)
		if err != nil || !reflect.DeepEqual(constraints, savedConstraints) {
			return ErrReviewNotAuthorized
		}
		academic, err := selectedAcademicSources(selected)
		if err != nil {
			return ErrReviewNotAuthorized
		}
		for id, item := range academic {
			index := slices.IndexFunc(snapshots, func(snapshot RewriteSourceSnapshot) bool { return snapshot.SourceID == id })
			if index < 0 || snapshots[index].AcademicSnapshot == nil || *snapshots[index].AcademicSnapshot != item.identity || snapshots[index].Locator != item.ref.Locator || snapshots[index].QuotedTextHash != item.ref.QuotedTextHash {
				return ErrReviewNotAuthorized
			}
		}
	}
	if err := s.recheckReviewSources(ctx, actor, projectID, row); err != nil {
		return ErrReviewNotAuthorized
	}
	if err := s.workspace.Authorize(ctx, actor, projectID, capability); err != nil {
		return ErrReviewNotAuthorized
	}
	return nil
}
