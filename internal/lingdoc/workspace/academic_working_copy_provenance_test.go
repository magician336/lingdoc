package workspace

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/lingdoc/candidateadoption"
	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
	"github.com/Tencent/WeKnora/internal/lingdoc/writingaction"
	"github.com/stretchr/testify/require"
)

func TestAcademicWorkingCopyCommitProvenanceRemainsUnverified(t *testing.T) {
	for _, change := range []string{"none", "card version", "body"} {
		t.Run(change, func(t *testing.T) {
			f := newAcademicAdoptionFixture(t, true)
			store := candidateadoption.NewSQLiteCandidateAdoptionStore(f.db)
			current, err := store.GenerationContext(f.ctx, "project-1", "chapter-2")
			require.NoError(t, err)
			current.Basis.AssetVersions = []candidateadoption.AssetVersion{{AssetID: f.assetID, AssetRevision: f.anchor.AssetRevision}}
			current.Basis.EvidenceCardRefs = []core.VersionRef{{Kind: "EvidenceCard", ID: f.card.ID, Version: f.card.Version}}
			body := "Synthetic draft [[source:" + f.sourceID + "]]"
			candidate := candidateadoption.Candidate{ID: "academic-draft-candidate", ProjectID: "project-1", ChapterID: "chapter-2", Validity: "fresh", RunID: "synthetic", BodyMarkdown: body, SourceIDs: []string{f.sourceID}, Basis: current.Basis, WritingAction: writingaction.ActionGenerate, Origin: writingaction.OriginGenerated}
			candidate.Claims = []candidateadoption.ClaimEvidence{{Text: body, References: []candidateadoption.ClaimEvidenceReference{{CardID: f.card.ID, CardVersion: f.card.Version, AnchorID: f.anchor.ID, AnchorVersion: f.anchor.Version, SourceID: f.sourceID, Locator: f.anchor.Locator, SelectionStart: f.anchor.SelectionStart, SelectionEnd: f.anchor.SelectionEnd, QuotedTextHash: f.anchor.QuotedTextHash}}}}
			require.NoError(t, store.UpsertCandidate(f.ctx, candidate))
			var copy struct {
				WorkingCopyRevision int64
				BodyMarkdown        string
			}
			require.NoError(t, f.db.Raw("SELECT working_copy_revision, body_markdown FROM lingdoc_working_copies WHERE project_id = ? AND chapter_id = ?", "project-1", "chapter-2").Scan(&copy).Error)
			input := candidateadoption.AcceptCandidateInput{ProjectID: "project-1", ChapterID: "chapter-2", CandidateID: candidate.ID, ActorID: f.actor.UserID, IdempotencyKey: "academic-draft-adoption", ExpectedChapterVersionID: current.ChapterVersionID, ExpectedSpecRevision: current.SpecRevision, ReplaceExisting: true, ExpectedWorkingCopyRevision: copy.WorkingCopyRevision, ExpectedBodyHash: workingCopyBodyHash(copy.BodyMarkdown), Confirmation: &candidateadoption.AdoptionConfirmation{WritingAction: candidate.WritingAction, Target: writingaction.TargetWorkingCopyWhole, ConfirmedAt: time.Now().UTC(), ConfirmedBy: f.actor.UserID, Origin: candidate.Origin}}
			adoption := candidateadoption.NewCandidateAdoptionService(store, f.runtime.CandidateAdoptionSourcePolicy(), candidateOneConnAuthorizer{f.projects})
			_, err = adoption.AcceptCandidate(f.ctx, input)
			require.NoError(t, err)
			copy.WorkingCopyRevision++
			if change == "body" {
				_, _, _, err = f.projects.SaveWorkingCopy(f.ctx, f.actor, "project-1", "chapter-2", "provenance-body-edit", core.SaveWorkingCopyInput{ExpectedSpecRevision: int64(current.SpecRevision), ExpectedWorkingCopyRevision: copy.WorkingCopyRevision, BaseChapterVersionID: current.ChapterVersionID, BodyMarkdown: body + " changed", SourceIDs: []string{f.sourceID}})
				require.NoError(t, err)
				copy.WorkingCopyRevision++
			}
			if change == "card version" {
				changed := f.card
				changed.Version++
				payload, e := json.Marshal(changed)
				require.NoError(t, e)
				require.NoError(t, f.db.Exec("UPDATE lingdoc_g2_objects SET version = version + 1, payload_json = ? WHERE project_id = ? AND kind = ? AND id = ?", string(payload), "project-1", core.EvidenceCardKind, f.card.ID).Error)
			}
			committed, err := f.projects.CommitWorkingCopyResult(f.ctx, f.actor, "project-1", "chapter-2", "academic-draft-commit", core.CommitWorkingCopyInput{ExpectedSpecRevision: int64(current.SpecRevision), ExpectedWorkingCopyRevision: copy.WorkingCopyRevision, ExpectedChapterVersionID: current.ChapterVersionID})
			require.NoError(t, err)
			var rows []core.DraftClaimProvenance
			require.NoError(t, f.db.Where("project_id = ? AND chapter_id = ? AND chapter_version_id = ?", "project-1", "chapter-2", committed.Version.ChapterVersionID).Find(&rows).Error)
			require.Len(t, rows, 1)
			if change == "none" {
				require.Equal(t, "needs_revalidation", rows[0].Status)
			} else {
				require.Equal(t, "stale", rows[0].Status)
			}
			// Provenance metadata does not grant permission to reuse evidence.
			_, err = f.projects.SelectGenerationEvidence(f.ctx, f.actor, "project-1", "chapter-2", candidate.Basis.EvidenceCardRefs, true)
			require.Error(t, err)
		})
	}
}
