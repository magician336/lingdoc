package workspace

import (
	"context"
	"slices"

	"github.com/Tencent/WeKnora/internal/evidence"
	"github.com/Tencent/WeKnora/internal/lingdoc/generation"
	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
)

// academicSnapshotIdentity is stored only on server-created rewrite records.
// It is useful for comparison, never a substitute for selecting the card
// again under the current caller's permissions.
type academicSnapshotIdentity struct {
	RawRef  core.AcademicOriginalSnapshotRef `json:"raw_ref"`
	TextRef core.AcademicOriginalSnapshotRef `json:"text_ref"`
}

func (s *SelectedRewriteService) fetchSourcesWithEvidence(ctx context.Context, actor Actor, projectID string, ids []string, selected core.EvidenceSelection) ([]evidence.Source, []RewriteSourceSnapshot, error) {
	academic, err := selectedAcademicSources(selected)
	if err != nil {
		return nil, nil, err
	}
	sources, _, err := s.fetchSources(ctx, actor, projectID, ids)
	if err != nil {
		return nil, nil, err
	}
	for i := range sources {
		if item, ok := academic[sources[i].ID]; ok {
			sources[i], err = overlayAcademicSource(projectID, sources[i], item)
			if err != nil {
				return nil, nil, err
			}
		}
	}
	snapshots := sourceSnapshots(sources, s.now())
	for i := range snapshots {
		if item, ok := academic[snapshots[i].SourceID]; ok {
			identity := item.identity
			snapshots[i].AcademicSnapshot = &identity
		}
	}
	return sources, snapshots, nil
}

func (s *SelectedRewriteService) recheckEvidenceSources(ctx context.Context, actor Actor, projectID, chapterID string, refs []generation.EvidenceCardRef, snapshots []RewriteSourceSnapshot) error {
	if !slices.ContainsFunc(snapshots, func(snapshot RewriteSourceSnapshot) bool { return snapshot.AcademicSnapshot != nil }) {
		return s.recheckSources(ctx, actor, projectID, snapshots)
	}
	var selected core.EvidenceSelection
	if len(refs) != 0 {
		var err error
		selected, err = selectFormalEvidence(ctx, s.selector, actor, projectID, chapterID, evidenceCardVersionRefs(refs))
		if err != nil {
			return ErrRewriteSourceDenied
		}
	}
	academic, err := selectedAcademicSources(selected)
	if err != nil {
		return ErrRewriteSourceDenied
	}
	for _, snapshot := range snapshots {
		live, err := s.sources.GetSource(ctx, actor, projectID, snapshot.SourceID)
		if err != nil || live.ProjectID != projectID || live.Status != evidence.SourceAvailable || live.AssetID != snapshot.AssetID || live.AssetRevision != snapshot.AssetRevision {
			return ErrRewriteSourceDenied
		}
		if item, ok := academic[snapshot.SourceID]; ok {
			if snapshot.AcademicSnapshot == nil || *snapshot.AcademicSnapshot != item.identity || snapshot.QuotedTextHash != item.ref.QuotedTextHash || snapshot.Locator != item.ref.Locator {
				return ErrRewriteSourceDenied
			}
		} else if snapshot.AcademicSnapshot != nil || live.Locator != snapshot.Locator || live.QuotedTextHash != snapshot.QuotedTextHash {
			return ErrRewriteSourceDenied
		}
	}
	return rewriteModelScope(ctx, s.sources, actor, projectID, snapshots)
}

func (s *SelectedRewriteService) recheckRowSources(ctx context.Context, actor Actor, projectID string, row selectedRewriteRow, snapshots []RewriteSourceSnapshot) error {
	refs, err := decodeRewriteEvidenceRefs(row.EvidenceCardRefsJSON)
	if err != nil {
		return ErrRewriteSourceDenied
	}
	return s.recheckEvidenceSources(ctx, actor, projectID, row.ChapterID, refs, snapshots)
}

func (s *SelectedRewriteService) recheckReviewSources(ctx context.Context, actor Actor, projectID string, row reviewReportRow) error {
	refs, err := decodeRewriteEvidenceRefs(row.EvidenceCardRefsJSON)
	if err != nil {
		return ErrRewriteSourceDenied
	}
	snapshots := mustDecodeSnapshots(row.AuthorizedSourcesJSON)
	if !slices.ContainsFunc(snapshots, func(snapshot RewriteSourceSnapshot) bool { return snapshot.AcademicSnapshot != nil }) {
		return s.recheckSources(ctx, actor, projectID, snapshots)
	}
	if len(refs) > 0 {
		selected, err := selectFormalEvidence(ctx, s.selector, actor, projectID, row.ChapterID, evidenceCardVersionRefs(refs))
		if err != nil {
			return ErrRewriteSourceDenied
		}
		academic, err := selectedAcademicSources(selected)
		if err != nil {
			return ErrRewriteSourceDenied
		}
		for _, snapshot := range snapshots {
			if item, ok := academic[snapshot.SourceID]; ok && (snapshot.AcademicSnapshot == nil || *snapshot.AcademicSnapshot != item.identity) {
				return ErrRewriteSourceDenied
			}
		}
	}
	return s.recheckEvidenceSources(ctx, actor, projectID, row.ChapterID, refs, snapshots)
}

func snapshotSourceIdentityEqual(a, b *academicSnapshotIdentity) bool {
	return a == nil && b == nil || a != nil && b != nil && *a == *b
}

type selectedAcademicSource struct {
	ref      core.AgentSourceRef
	identity academicSnapshotIdentity
}

func selectedAcademicSources(selected core.EvidenceSelection) (map[string]selectedAcademicSource, error) {
	out := make(map[string]selectedAcademicSource)
	for _, card := range selected.Cards {
		if len(card.AcademicAssociationRefs) == 0 {
			continue
		}
		if card.Confirmation == nil || len(card.Confirmation.AcademicSnapshots) == 0 {
			return nil, ErrRewriteSourceDenied
		}
		for _, binding := range card.Confirmation.AcademicSnapshots {
			var found core.AgentSourceRef
			count := 0
			for _, source := range selected.Sources {
				if source.SourceID == binding.SourceID {
					found = source
					count++
				}
			}
			if count != 1 || binding.SourceID == "" || binding.AssetID == "" || binding.AssetRevision < 1 ||
				found.AssetID != binding.AssetID || found.AssetRevision != binding.AssetRevision || found.Locator != binding.Locator ||
				found.QuotedTextHash != binding.TextRef.SHA256 || researchTextHash(found.QuotedText) != binding.TextRef.SHA256 ||
				int64(len([]byte(found.QuotedText))) != binding.TextRef.Size || !found.ModelUseAllowed {
				return nil, ErrRewriteSourceDenied
			}
			identity := academicSnapshotIdentity{RawRef: binding.RawRef, TextRef: binding.TextRef}
			if prior, ok := out[binding.SourceID]; ok && (prior.identity != identity || prior.ref != found) {
				return nil, ErrRewriteSourceDenied
			}
			out[binding.SourceID] = selectedAcademicSource{ref: found, identity: identity}
		}
	}
	return out, nil
}

// overlayAcademicSource keeps the independent live access/status check while
// taking content and locator exclusively from the authorized snapshot.
func overlayAcademicSource(projectID string, live evidence.Source, academic selectedAcademicSource) (evidence.Source, error) {
	if live.ProjectID != projectID || live.ID != academic.ref.SourceID || live.Status != evidence.SourceAvailable ||
		live.AssetID != academic.ref.AssetID || live.AssetRevision != academic.ref.AssetRevision {
		return evidence.Source{}, ErrRewriteSourceDenied
	}
	live.Locator = academic.ref.Locator
	live.QuotedText = academic.ref.QuotedText
	live.QuotedTextHash = academic.ref.QuotedTextHash
	// The live chunk anchor describes a different read; evidence anchors are
	// supplied separately by the revalidated card selection.
	live.Anchor = evidence.Anchor{}
	return live, nil
}
