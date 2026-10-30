package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/lingdoc/candidateadoption"
	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
	"github.com/Tencent/WeKnora/internal/lingdoc/writingaction"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type academicAdoptionFixture struct {
	academicSnapshotHTTPFixture
	ctx         context.Context
	card        core.EvidenceCard
	anchor      core.SourceAnchor
	association core.AcademicSourceAssociation
	candidate   core.ImportCandidate
}

func newAcademicAdoptionFixture(t *testing.T, pdf bool) academicAdoptionFixture {
	t.Helper()
	return buildAcademicAdoptionFixture(t, newAcademicSnapshotHTTPFixture(t, pdf))
}

func buildAcademicAdoptionFixture(t *testing.T, f academicSnapshotHTTPFixture) academicAdoptionFixture {
	t.Helper()
	ctx := types.WithExecutionTenant(policyContext(), f.actor.TenantID)
	project, err := f.projects.GetProject(ctx, f.actor, "project-1")
	require.NoError(t, err)
	expectation := func(version int64) string {
		return fmt.Sprintf(`"expected_project_version":%d,"expected_context_revision":%d,"expected_version":%d`, project.ProjectVersion, project.CurrentContextRevision, version)
	}
	const route = "/api/projects/project-1"
	var catalog core.AcademicSourceCatalog
	academicSnapshotHTTPData(t, f.request("reader", http.MethodPut, route+"/academic-source-catalog", "adoption-catalog", `{`+expectation(0)+`,"rules":[{"id":"journal","source_type":"journal","venue_name":"Synthetic","venue_ids":["1234-5678"]}]}`), http.StatusOK, &catalog)
	policyVersion := academicCatalogSnapshot(syntheticGlobalAcademicCatalog(), catalog).Version
	candidate := core.ImportCandidate{ID: "adoption-academic-candidate", ProjectID: "project-1", Version: 1, DOI: "10.5555/adoption", Status: "imported", SelectionType: "academic_metadata", SaveMode: "metadata_only", CatalogVersion: catalog.SourceCatalogVersion, PolicySnapshotVersion: policyVersion, PolicyStatus: "approved", RetractionStatus: "unknown", DuplicateStatus: "unique", AcquisitionStatus: "metadata_only", Basis: core.VersionBasis{ProjectVersion: project.ProjectVersion, SpecRevision: project.SpecRevision, ContextRevision: project.CurrentContextRevision}}
	payload, err := json.Marshal(candidate)
	require.NoError(t, err)
	require.NoError(t, core.NewTransactionAdapter(f.db).(core.ObjectStore).InsertObject(core.StoredObject{ProjectID: "project-1", Kind: core.ImportCandidateKind, ID: candidate.ID, Version: 1, Payload: payload}))
	var anchor core.SourceAnchor
	academicSnapshotHTTPData(t, f.request("reader", http.MethodPost, route+"/source-anchors", "adoption-anchor", `{`+expectation(0)+`,"source_id":`+strconv.Quote(f.sourceID)+`,"selection_start":0,"selection_end":`+strconv.Itoa(len([]rune(sourcePolicyQuote)))+`,"extraction_type":"native_text","confirmed":true}`), http.StatusCreated, &anchor)
	var claim core.ResearchClaim
	academicSnapshotHTTPData(t, f.request("reader", http.MethodPost, route+"/research-claims", "adoption-claim", `{`+expectation(0)+`,"text":"Synthetic conclusion","scope":"synthetic","reason":"test","confirmed":true}`), http.StatusOK, &claim)
	var association core.AcademicSourceAssociation
	body := `{` + expectation(1) + `,"candidate_id":"` + candidate.ID + `","source_id":` + strconv.Quote(f.sourceID) + `,"expected_asset_revision":` + strconv.Itoa(anchor.AssetRevision) + `,"expected_quoted_text_hash":"` + anchor.QuotedTextHash + `","expected_locator":` + strconv.Quote(anchor.Locator) + `,"confirmed":true,"reason":"synthetic human review"}`
	academicSnapshotHTTPData(t, f.request("reader", http.MethodPost, route+"/academic-source-associations", "adoption-association", body), http.StatusCreated, &association)
	var card core.EvidenceCard
	body = `{` + expectation(0) + `,"claim_ref":{"id":"` + claim.ID + `","version":` + strconv.FormatInt(claim.Version, 10) + `},"anchor_refs":[{"id":"` + anchor.ID + `","version":` + strconv.FormatInt(anchor.Version, 10) + `}],"academic_association_refs":[{"id":"` + association.ID + `","version":1}],"support_type":"supports","scope":"synthetic","scope_chapter_ids":["chapter-2"],"limitations":"synthetic only","confirmed":true}`
	academicSnapshotHTTPData(t, f.request("reader", http.MethodPost, route+"/evidence-cards", "adoption-card", body), http.StatusCreated, &card)
	body = `{` + expectation(card.Version) + `,"confirmed":true,"reason":"synthetic human review"}`
	academicSnapshotHTTPData(t, f.request("reader", http.MethodPost, route+"/evidence-cards/"+card.ID+"/confirm", "adoption-card-confirm", body), http.StatusOK, &card)
	return academicAdoptionFixture{academicSnapshotHTTPFixture: f, ctx: ctx, card: card, anchor: anchor, association: association, candidate: candidate}
}

func TestAcademicCandidateAdoptionNativeProofRechecksUncommittedChanges(t *testing.T) {
	for _, pdf := range []bool{false, true} {
		t.Run(fmt.Sprintf("pdf=%v", pdf), func(t *testing.T) {
			f := newAcademicAdoptionFixture(t, pdf)
			refs := []core.VersionRef{{Kind: "EvidenceCard", ID: f.card.ID, Version: f.card.Version}}
			prepared, selected, err := f.projects.PrepareCandidateGenerationEvidence(f.ctx, f.actor, "project-1", "chapter-2", refs, []string{f.sourceID})
			require.NoError(t, err)
			require.Equal(t, sourcePolicyQuote, selected.Sources[0].QuotedText)
			abort := errors.New("rollback synthetic mutation")
			for _, scenario := range []string{"retraction", "source disabled", "tenant revoked", "snapshot removed"} {
				t.Run(scenario, func(t *testing.T) {
					err := f.db.Transaction(func(tx *gorm.DB) error {
						port := core.NewTransactionAdapter(tx)
						before, e := f.projects.SelectGenerationEvidenceInTransaction(prepared, port, f.actor, "project-1", "chapter-2", refs, true, []string{f.sourceID})
						require.NoError(t, e)
						require.Equal(t, sourcePolicyQuote, before.Sources[0].QuotedText)
						switch scenario {
						case "retraction":
							id := sha256Hex([]byte(strings.ToLower(f.candidate.DOI)))
							payload, _ := json.Marshal(map[string]string{"doi": f.candidate.DOI})
							require.NoError(t, port.(core.ObjectStore).InsertObject(core.StoredObject{ProjectID: "project-1", Kind: "academic_retraction", ID: id, Version: 1, Payload: payload}))
						case "source disabled":
							require.NoError(t, tx.Exec("UPDATE chunks SET is_enabled = ? WHERE id = ?", false, f.sourceID).Error)
						case "tenant revoked":
							require.NoError(t, tx.Exec("UPDATE tenant_members SET status = 'inactive' WHERE tenant_id = ? AND user_id = ?", f.actor.TenantID, f.actor.UserID).Error)
						case "snapshot removed":
							require.NoError(t, tx.Exec("DELETE FROM lingdoc_academic_original_snapshots WHERE tenant_id = ? AND project_id = ? AND sha256 = ?", f.actor.TenantID, "project-1", f.card.Confirmation.AcademicSnapshots[0].RawRef.SHA256).Error)
						}
						after, e := f.projects.SelectGenerationEvidenceInTransaction(prepared, port, f.actor, "project-1", "chapter-2", refs, true, []string{f.sourceID})
						require.Error(t, e)
						require.Empty(t, after.Cards)
						require.Empty(t, after.Sources)
						return abort
					})
					require.ErrorIs(t, err, abort)
				})
			}
		})
	}
}

func TestAcademicCandidateAdoptionWithOneConnection(t *testing.T) {
	for _, distinctSource := range []bool{false, true} {
		t.Run(fmt.Sprintf("distinct_source=%v", distinctSource), func(t *testing.T) {
			testAcademicCandidateAdoptionWithOneConnection(t, newAcademicAdoptionFixture(t, true), distinctSource)
		})
	}
}

func testAcademicCandidateAdoptionWithOneConnection(t *testing.T, f academicAdoptionFixture, distinctSource bool) {
	ordinarySourceID := f.sourceID
	if distinctSource {
		ordinarySourceID = "ordinary-source-2"
		seedChunk(t, f.db, ordinarySourceID, "knowledge-1", sourcePolicyQuote, 0, 0, len([]rune(sourcePolicyQuote)))
		require.NoError(t, f.db.Exec("UPDATE chunks SET source_content = content WHERE id = ?", ordinarySourceID).Error)
		require.NoError(t, f.db.Create(&types.ChunkOriginSHA256{ChunkID: ordinarySourceID, TenantID: 7, KnowledgeID: "knowledge-1", SHA256: sha256Hex(f.raw)}).Error)
		if strings.HasPrefix(string(f.raw), "%PDF-") {
			require.NoError(t, f.db.Create(&types.ChunkPDFPage{ChunkID: ordinarySourceID, TenantID: 7, KnowledgeID: "knowledge-1", Page: 1, TextSHA256: sha256Hex([]byte(sourcePolicyQuote))}).Error)
		}
	}
	store := candidateadoption.NewSQLiteCandidateAdoptionStore(f.db)
	current, err := store.GenerationContext(f.ctx, "project-1", "chapter-2")
	require.NoError(t, err)
	current.Basis.AssetVersions = []candidateadoption.AssetVersion{{AssetID: f.assetID, AssetRevision: f.anchor.AssetRevision}}
	current.Basis.EvidenceCardRefs = []core.VersionRef{{Kind: "EvidenceCard", ID: f.card.ID, Version: f.card.Version}}
	body := "Synthetic draft [[source:" + f.sourceID + "]]"
	candidate := candidateadoption.Candidate{ID: "academic-writing-candidate", ProjectID: "project-1", ChapterID: "chapter-2", Validity: "fresh", RunID: "synthetic", BodyMarkdown: body, SourceIDs: []string{f.sourceID}, Basis: current.Basis, WritingAction: writingaction.ActionGenerate, Origin: writingaction.OriginGenerated}
	candidate.Claims = []candidateadoption.ClaimEvidence{{Text: body, References: []candidateadoption.ClaimEvidenceReference{{CardID: f.card.ID, CardVersion: f.card.Version, AnchorID: f.anchor.ID, AnchorVersion: f.anchor.Version, SourceID: f.sourceID, Locator: f.anchor.Locator, SelectionStart: f.anchor.SelectionStart, SelectionEnd: f.anchor.SelectionEnd, QuotedTextHash: f.anchor.QuotedTextHash}}}}
	require.NoError(t, store.UpsertCandidate(f.ctx, candidate))
	var copy struct {
		WorkingCopyRevision int64
		BodyMarkdown        string
	}
	require.NoError(t, f.db.Raw("SELECT working_copy_revision, body_markdown FROM lingdoc_working_copies WHERE project_id = ? AND chapter_id = ?", "project-1", "chapter-2").Scan(&copy).Error)
	input := candidateadoption.AcceptCandidateInput{ProjectID: "project-1", ChapterID: "chapter-2", CandidateID: candidate.ID, ActorID: f.actor.UserID, IdempotencyKey: "academic-writing-adoption", ExpectedChapterVersionID: current.ChapterVersionID, ExpectedSpecRevision: current.SpecRevision, ReplaceExisting: true, ExpectedWorkingCopyRevision: copy.WorkingCopyRevision, ExpectedBodyHash: workingCopyBodyHash(copy.BodyMarkdown), Confirmation: &candidateadoption.AdoptionConfirmation{WritingAction: candidate.WritingAction, Target: writingaction.TargetFormalVersionWhole, ConfirmedAt: time.Now().UTC(), ConfirmedBy: f.actor.UserID, Origin: candidate.Origin}}
	db, err := f.db.DB()
	require.NoError(t, err)
	db.SetMaxOpenConns(1)
	ctx, cancel := context.WithTimeout(f.ctx, 45*time.Second)
	defer cancel()
	adoption := candidateadoption.NewCandidateAdoptionService(store, f.runtime.CandidateAdoptionSourcePolicy(), candidateOneConnAuthorizer{f.projects})
	project, err := f.projects.GetProject(ctx, f.actor, "project-1")
	require.NoError(t, err)
	expectation := fmt.Sprintf(`"expected_project_version":%d,"expected_context_revision":%d,"expected_version":0`, project.ProjectVersion, project.CurrentContextRevision)
	ordinaryAnchor := f.anchor
	if distinctSource {
		anchorBody := `{` + expectation + `,"source_id":` + strconv.Quote(ordinarySourceID) + `,"selection_start":0,"selection_end":` + strconv.Itoa(len([]rune(sourcePolicyQuote))) + `,"extraction_type":"native_text","confirmed":true}`
		academicSnapshotHTTPData(t, f.request("reader", http.MethodPost, "/api/projects/project-1/source-anchors", "adoption-ordinary-anchor", anchorBody), http.StatusCreated, &ordinaryAnchor)
		candidate.SourceIDs = append(candidate.SourceIDs, ordinarySourceID)
		candidate.BodyMarkdown += " [[source:" + ordinarySourceID + "]]"
		candidate.Claims[0].Text = candidate.BodyMarkdown
	}
	var ordinary core.EvidenceCard
	ordinaryBody := `{` + expectation + `,"claim_ref":{"id":"` + f.card.ClaimRef.ID + `","version":` + strconv.FormatInt(f.card.ClaimRef.Version, 10) + `},"anchor_refs":[{"id":"` + ordinaryAnchor.ID + `","version":` + strconv.FormatInt(ordinaryAnchor.Version, 10) + `}],"support_type":"supports","scope":"synthetic","scope_chapter_ids":["chapter-2"],"limitations":"synthetic only","confirmed":true}`
	academicSnapshotHTTPData(t, f.request("reader", http.MethodPost, "/api/projects/project-1/evidence-cards", "adoption-ordinary-card", ordinaryBody), http.StatusCreated, &ordinary)
	confirmBody := fmt.Sprintf(`{"expected_project_version":%d,"expected_context_revision":%d,"expected_version":%d,"confirmed":true,"reason":"synthetic review"}`, project.ProjectVersion, project.CurrentContextRevision, ordinary.Version)
	academicSnapshotHTTPData(t, f.request("reader", http.MethodPost, "/api/projects/project-1/evidence-cards/"+ordinary.ID+"/confirm", "adoption-ordinary-confirm", confirmBody), http.StatusOK, &ordinary)
	academicRefs := candidate.Basis.EvidenceCardRefs
	candidate.Basis.EvidenceCardRefs = append(append([]core.VersionRef{}, academicRefs...), core.VersionRef{Kind: "EvidenceCard", ID: ordinary.ID, Version: ordinary.Version})
	candidate.Claims[0].References = append(candidate.Claims[0].References, candidateadoption.ClaimEvidenceReference{CardID: ordinary.ID, CardVersion: ordinary.Version, AnchorID: ordinaryAnchor.ID, AnchorVersion: ordinaryAnchor.Version, SourceID: ordinarySourceID, Locator: ordinaryAnchor.Locator, SelectionStart: ordinaryAnchor.SelectionStart, SelectionEnd: ordinaryAnchor.SelectionEnd, QuotedTextHash: ordinaryAnchor.QuotedTextHash})
	require.NoError(t, store.UpsertCandidate(ctx, candidate))
	preparedMixed, _, err := f.projects.PrepareCandidateGenerationEvidence(ctx, f.actor, "project-1", "chapter-2", candidate.Basis.EvidenceCardRefs, candidate.SourceIDs)
	require.NoError(t, err, "mixed preview must remain current before adoption")
	if f.db.Name() == "postgres" {
		db.SetMaxOpenConns(2)
		for _, target := range []struct {
			query string
			args  []any
		}{
			{"UPDATE chunks SET content = content WHERE id = ?", []any{ordinarySourceID}},
			{"UPDATE lingdoc_g2_objects SET payload_json = payload_json WHERE project_id = ? AND kind = ? AND id = ?", []any{"project-1", core.EvidenceCardKind, ordinary.ID}},
		} {
			locked, release, finished := make(chan struct{}), make(chan struct{}), make(chan error, 1)
			go func() {
				finished <- f.db.Transaction(func(tx *gorm.DB) error {
					_, e := f.projects.SelectGenerationEvidenceInTransaction(preparedMixed, core.NewTransactionAdapter(tx), f.actor, "project-1", "chapter-2", candidate.Basis.EvidenceCardRefs, true, candidate.SourceIDs)
					if e != nil {
						return e
					}
					close(locked)
					<-release
					return nil
				})
			}()
			select {
			case <-locked:
			case e := <-finished:
				t.Fatalf("mixed validation failed before acquiring locks: %v", e)
			case <-time.After(10 * time.Second):
				close(release)
				t.Fatal("mixed validation did not acquire locks")
			}
			updated := make(chan error, 1)
			go func() { updated <- f.db.Exec(target.query, target.args...).Error }()
			select {
			case e := <-updated:
				close(release)
				t.Fatalf("native update escaped mixed evidence lock: %v", e)
			case <-time.After(200 * time.Millisecond):
			}
			close(release)
			require.NoError(t, <-finished)
			select {
			case e := <-updated:
				require.NoError(t, e)
			case <-time.After(10 * time.Second):
				t.Fatal("native update remained blocked after transaction")
			}
		}
		db.SetMaxOpenConns(1)
	}
	abort := errors.New("rollback mixed source mutation")
	err = f.db.Transaction(func(tx *gorm.DB) error {
		require.NoError(t, tx.Exec("UPDATE chunks SET content = ? WHERE id = ?", "changed after preview", ordinarySourceID).Error)
		selection, e := f.projects.SelectGenerationEvidenceInTransaction(preparedMixed, core.NewTransactionAdapter(tx), f.actor, "project-1", "chapter-2", candidate.Basis.EvidenceCardRefs, true, candidate.SourceIDs)
		require.Error(t, e)
		require.Empty(t, selection.Cards)
		require.Empty(t, selection.Sources)
		return abort
	})
	require.ErrorIs(t, err, abort)
	require.NoError(t, f.runtime.CandidateAdoptionSourcePolicy().Validate(ctx, "project-1", f.actor.UserID, candidate.SourceIDs), "current source authorization")
	currentAfterCards, err := store.GenerationContext(ctx, "project-1", "chapter-2")
	require.NoError(t, err)
	require.Equal(t, current.Basis.AssetVersions, currentAfterCards.Basis.AssetVersions)
	persisted, err := store.GetCandidate(ctx, "project-1", candidate.ID)
	require.NoError(t, err)
	require.Equal(t, candidate.Validity, persisted.Validity)
	result, err := adoption.AcceptCandidate(ctx, input)
	require.NoError(t, err)
	require.False(t, result.Replayed)
	replayed, err := adoption.AcceptCandidate(ctx, input)
	require.NoError(t, err)
	require.True(t, replayed.Replayed)
	require.Equal(t, result.Chapter.CurrentVersionID, replayed.Chapter.CurrentVersionID)
	var count int64
	require.NoError(t, f.db.Table("lingdoc_chapter_versions").Where("candidate_id = ?", candidate.ID).Count(&count).Error)
	require.EqualValues(t, 1, count, "replay must not create a second chapter version")

	// The historical path is only a read of the exact committed result and
	// must still reject current revocation and later unrelated project changes.
	id := sha256Hex([]byte(strings.ToLower(f.candidate.DOI)))
	payload, err := json.Marshal(map[string]string{"doi": f.candidate.DOI})
	require.NoError(t, err)
	require.NoError(t, core.NewTransactionAdapter(f.db).(core.ObjectStore).InsertObject(core.StoredObject{ProjectID: "project-1", Kind: "academic_retraction", ID: id, Version: 1, Payload: payload}))
	_, err = adoption.AcceptCandidate(ctx, input)
	require.Error(t, err)
	require.NoError(t, f.db.Exec("DELETE FROM lingdoc_g2_objects WHERE project_id = ? AND kind = ? AND id = ?", "project-1", "academic_retraction", id).Error)
	require.NoError(t, f.db.Exec("UPDATE tenant_members SET status = 'inactive' WHERE tenant_id = ? AND user_id = ?", f.actor.TenantID, f.actor.UserID).Error)
	_, err = adoption.AcceptCandidate(ctx, input)
	require.Error(t, err)
	require.NoError(t, f.db.Exec("UPDATE tenant_members SET status = 'active' WHERE tenant_id = ? AND user_id = ?", f.actor.TenantID, f.actor.UserID).Error)
	require.NoError(t, f.db.Exec("UPDATE chunks SET is_enabled = ? WHERE id = ?", false, ordinarySourceID).Error)
	_, err = adoption.AcceptCandidate(ctx, input)
	require.Error(t, err, "revoked ordinary source must invalidate mixed replay")
	require.NoError(t, f.db.Exec("UPDATE chunks SET is_enabled = ? WHERE id = ?", true, ordinarySourceID).Error)
	originalOrdinary, err := json.Marshal(ordinary)
	require.NoError(t, err)
	changedOrdinary := ordinary
	changedOrdinary.Version++
	changedPayload, err := json.Marshal(changedOrdinary)
	require.NoError(t, err)
	require.NoError(t, f.db.Exec("UPDATE lingdoc_g2_objects SET version = version + 1, payload_json = ? WHERE project_id = ? AND kind = ? AND id = ?", string(changedPayload), "project-1", core.EvidenceCardKind, ordinary.ID).Error)
	_, err = adoption.AcceptCandidate(ctx, input)
	require.Error(t, err, "ordinary-card changes must invalidate mixed replay")
	require.NoError(t, f.db.Exec("UPDATE lingdoc_g2_objects SET version = version - 1, payload_json = ? WHERE project_id = ? AND kind = ? AND id = ?", string(originalOrdinary), "project-1", core.EvidenceCardKind, ordinary.ID).Error)
	require.NoError(t, f.db.Exec("UPDATE lingdoc_candidates SET body_markdown = ? WHERE id = ?", "changed after acceptance", candidate.ID).Error)
	_, err = adoption.AcceptCandidate(ctx, input)
	require.Error(t, err)
	require.NoError(t, f.db.Exec("UPDATE lingdoc_candidates SET body_markdown = ? WHERE id = ?", candidate.BodyMarkdown, candidate.ID).Error)
	changedRequest := input
	changedRequest.ExpectedBodyHash = strings.Repeat("a", 64)
	_, err = adoption.AcceptCandidate(ctx, changedRequest)
	require.ErrorIs(t, err, candidateadoption.ErrIdempotencyConflict)
	require.NoError(t, f.db.Exec("UPDATE lingdoc_projects SET project_version = project_version + 1 WHERE id = ?", "project-1").Error)
	_, err = adoption.AcceptCandidate(ctx, input)
	require.Error(t, err)
	require.NoError(t, ctx.Err(), "a rejection must not be caused by exhausting the request deadline")
}
