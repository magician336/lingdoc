package workspace

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
	"github.com/stretchr/testify/require"
)

func docxWithCitationBody(t *testing.T, section, body string) []byte {
	t.Helper()
	return replaceDOCXDocument(t, readDOCXFixture(t), func(document string) string {
		tail := map[string]string{"topic-summary": "字以内）。", "topic-basis": "应用价值。"}[section]
		require.NotEmpty(t, tail)
		index := strings.Index(document, tail)
		require.Greater(t, index, 0)
		insertAt := index + len(tail)
		return document[:insertAt] + body + document[insertAt:]
	})
}

func docxCitationFor(f candidateOneConnFixture) DOCXCitationRef {
	return DOCXCitationRef{SectionID: "topic-summary", SourceID: "source-1", EvidenceCardID: f.card.ID, EvidenceCardVersion: f.card.Version}
}

func clearDOCXFixtureSourceLedger(t *testing.T, f candidateOneConnFixture) {
	t.Helper()
	require.NoError(t, f.db.Exec("UPDATE lingdoc_chapter_versions SET source_ids_json='[]',citation_usages_json='[]' WHERE project_id='project-1'").Error)
}

func savedCitationProjection(t *testing.T, f candidateOneConnFixture, versionID, sectionID string) docxSectionProjectionRow {
	t.Helper()
	var row docxSectionProjectionRow
	require.NoError(t, f.db.Where("saved_version_id=? AND section_id=?", versionID, sectionID).Take(&row).Error)
	return row
}

func TestDOCXCitationSaveTracksSavedMarkersAndOldClients(t *testing.T) {
	f, _ := newDOCXHTTPFixture(t)
	clearDOCXFixtureSourceLedger(t, f)
	ref := docxCitationFor(f)
	data := docxWithCitationBody(t, ref.SectionID, "引用正文 [[source:source-1]]")
	saved, replayed, err := f.runtime.SaveDOCXVersion(f.ctx, f.actor, "project-1", "citation-first", 0, data, []DOCXCitationRef{ref})
	require.NoError(t, err)
	require.False(t, replayed)
	require.Equal(t, []DOCXCitationRef{ref}, saved.Citations)
	first := savedCitationProjection(t, f, saved.ID, ref.SectionID)
	require.Equal(t, `["source-1"]`, first.SourceIDsJSON)
	require.Equal(t, docxCitationJSON(saved.Citations), first.CitationsJSON)
	var ledger string
	require.NoError(t, f.db.Table("lingdoc_chapter_versions").Select("source_ids_json").Where("id=?", first.ChapterVersionID).Scan(&ledger).Error)
	require.Equal(t, first.SourceIDsJSON, ledger)
	// A pre-extension client can keep saving the same actual citation. No second
	// body or out-of-band metadata write is needed to preserve its provenance.
	next, _, err := f.runtime.SaveDOCXVersion(f.ctx, f.actor, "project-1", "citation-old-client", 1, data)
	require.NoError(t, err)
	require.Equal(t, saved.Citations, next.Citations)
	require.Equal(t, first.ChapterVersionID, savedCitationProjection(t, f, next.ID, ref.SectionID).ChapterVersionID)
	confirmation := docxHTTPConfirmationRequest(next, ref.SectionID, f.input.ExpectedSpecRevision)
	confirmation.ProjectID, confirmation.IdempotencyKey = "project-1", "citation-confirm"
	_, _, err = f.runtime.ConfirmSavedDOCXChapter(f.ctx, f.actor, confirmation)
	require.NoError(t, err)
	// A stale UI selection must not survive deletion/undo of the actual marker.
	removed, _, err := f.runtime.SaveDOCXVersion(f.ctx, f.actor, "project-1", "citation-removed", 2, docxWithCitationBody(t, ref.SectionID, "保留正文，删除引用"), []DOCXCitationRef{ref})
	require.NoError(t, err)
	require.Empty(t, removed.Citations)
	removedProjection := savedCitationProjection(t, f, removed.ID, ref.SectionID)
	require.Equal(t, "[]", removedProjection.SourceIDsJSON)
	require.Equal(t, "[]", removedProjection.CitationsJSON)
	require.NotEqual(t, first.ChapterVersionID, removedProjection.ChapterVersionID)
	var valid int64
	require.NoError(t, f.db.Table("lingdoc_chapter_confirmations").Where("chapter_version_id=? AND valid=1", first.ChapterVersionID).Count(&valid).Error)
	require.Zero(t, valid)
	// Immutable old versions retain the association that was true for their bytes.
	loaded, err := NewDOCXVersionStore(f.db).Get(f.ctx, "project-1", saved.ID)
	require.NoError(t, err)
	require.Equal(t, saved.Citations, loaded.Citations)
	require.Equal(t, data, loaded.Bytes)
	// Selection alone creates neither a source association nor a card claim.
	unused := DOCXCitationRef{SectionID: ref.SectionID, SourceID: "unread-source", EvidenceCardID: "unread-card", EvidenceCardVersion: 1}
	noMarker, _, err := f.runtime.SaveDOCXVersion(f.ctx, f.actor, "project-1", "citation-unused-selection", 3, readDOCXFixture(t), []DOCXCitationRef{unused})
	require.NoError(t, err)
	require.Empty(t, noMarker.Citations)
	var workingBody string
	require.NoError(t, f.db.Table("lingdoc_working_copies").Select("body_markdown").Where("project_id='project-1' AND chapter_id='chapter-2'").Scan(&workingBody).Error)
	require.NotContains(t, workingBody, "保留正文", "DOCX saves must not create a second Markdown body authority")
}

func TestDOCXCitationFailuresLeaveNoPartialVersion(t *testing.T) {
	for _, scenario := range []string{"missing-reference", "missing-card", "stale-card", "wrong-source", "wrong-chapter", "source-revoked", "confirmation-revoked", "malformed-marker"} {
		t.Run(scenario, func(t *testing.T) {
			f, _ := newDOCXHTTPFixture(t)
			clearDOCXFixtureSourceLedger(t, f)
			ref := docxCitationFor(f)
			refs := []DOCXCitationRef{ref}
			section, body := ref.SectionID, "引用正文 [[source:source-1]]"
			switch scenario {
			case "missing-reference":
				refs = nil
			case "missing-card":
				refs[0].EvidenceCardID = "absent-card"
			case "stale-card":
				refs[0].EvidenceCardVersion++
			case "wrong-source":
				refs[0].SourceID = "not-card-source"
				body = "引用正文 [[source:not-card-source]]"
			case "wrong-chapter":
				section, refs[0].SectionID = "topic-basis", "topic-basis"
			case "source-revoked":
				require.NoError(t, f.db.Exec("UPDATE chunks SET content_revision=1 WHERE id='source-1'").Error)
			case "confirmation-revoked":
				card := f.card
				card.Status, card.Confirmation = "NEEDS_REVIEW", nil
				raw, err := json.Marshal(card)
				require.NoError(t, err)
				require.NoError(t, f.db.Table("lingdoc_g2_objects").Where("project_id=? AND kind=? AND id=?", "project-1", core.G2CardKind, card.ID).Update("payload_json", string(raw)).Error)
			case "malformed-marker":
				body = "引用正文 [[source:source-1]"
			}
			var before []string
			require.NoError(t, f.db.Table("lingdoc_chapters").Order("id").Pluck("current_version_id", &before).Error)
			_, _, err := f.runtime.SaveDOCXVersion(f.ctx, f.actor, "project-1", "citation-invalid-"+scenario, 0, docxWithCitationBody(t, section, body), refs)
			require.Error(t, err)
			var versions, projections int64
			require.NoError(t, f.db.Model(&docxSavedVersionRow{}).Count(&versions).Error)
			require.NoError(t, f.db.Model(&docxSectionProjectionRow{}).Count(&projections).Error)
			require.Zero(t, versions)
			require.Zero(t, projections)
			var after []string
			require.NoError(t, f.db.Table("lingdoc_chapters").Order("id").Pluck("current_version_id", &after).Error)
			require.Equal(t, before, after, "failed validation must roll back earlier projected chapters")
		})
	}
}

func TestDOCXCitationHTTPRoundTripAndIdempotency(t *testing.T) {
	f, router := newDOCXHTTPFixture(t)
	clearDOCXFixtureSourceLedger(t, f)
	ref := docxCitationFor(f)
	payload := map[string]any{"expectedDiskVersion": 0, "data": base64.StdEncoding.EncodeToString(docxWithCitationBody(t, ref.SectionID, "引用正文 [[source:source-1]]")), "citations": []DOCXCitationRef{ref}}
	path := "/api/docx/projects/project-1/versions"
	first := performDOCXHTTPRequest(t, router, f.ctx, http.MethodPost, path, "citation-http-key", payload)
	require.Equal(t, http.StatusOK, first.Code, first.Body.String())
	saved := decodeDOCXHTTPResponse[docxHTTPSaveResponse](t, first)
	require.Equal(t, []DOCXCitationRef{ref}, saved.Version.Citations)
	latest := performDOCXHTTPRequest(t, router, f.ctx, http.MethodGet, path+"/latest", "", nil)
	require.Equal(t, http.StatusOK, latest.Code, latest.Body.String())
	require.Equal(t, saved.Version.Citations, decodeDOCXHTTPResponse[DOCXSavedVersion](t, latest).Citations)
	replay := performDOCXHTTPRequest(t, router, f.ctx, http.MethodPost, path, "citation-http-key", payload)
	require.Equal(t, http.StatusOK, replay.Code, replay.Body.String())
	replayed := decodeDOCXHTTPResponse[docxHTTPSaveResponse](t, replay)
	require.True(t, replayed.Replayed)
	require.Equal(t, saved.Version.ID, replayed.Version.ID)
	ref.EvidenceCardVersion++
	payload["citations"] = []DOCXCitationRef{ref}
	conflict := performDOCXHTTPRequest(t, router, f.ctx, http.MethodPost, path, "citation-http-key", payload)
	require.Equal(t, http.StatusConflict, conflict.Code, conflict.Body.String())
	require.Contains(t, conflict.Body.String(), "IDEMPOTENCY_CONFLICT")
	payload["expectedDiskVersion"] = 1
	rejected := performDOCXHTTPRequest(t, router, f.ctx, http.MethodPost, path, "citation-http-stale", payload)
	require.Equal(t, http.StatusUnprocessableEntity, rejected.Code, rejected.Body.String())
	require.Contains(t, rejected.Body.String(), "CITATION_INVALID")
}

func TestDOCXManualCitationDoesNotRequireModelConsent(t *testing.T) {
	f, _ := newDOCXHTTPFixture(t)
	clearDOCXFixtureSourceLedger(t, f)
	var row struct{ ID, PayloadJSON string }
	require.NoError(t, f.db.Table("lingdoc_g2_objects").Select("id,payload_json").Where("project_id=? AND kind=?", "project-1", core.G2AssetPolicyKind).Take(&row).Error)
	var policy core.ProjectAssetPolicy
	require.NoError(t, json.Unmarshal([]byte(row.PayloadJSON), &policy))
	policy.ModelUseAllowed = false
	raw, err := json.Marshal(policy)
	require.NoError(t, err)
	require.NoError(t, f.db.Table("lingdoc_g2_objects").Where("project_id=? AND kind=? AND id=?", "project-1", core.G2AssetPolicyKind, row.ID).Update("payload_json", string(raw)).Error)
	ref := docxCitationFor(f)
	_, _, err = f.runtime.SaveDOCXVersion(f.ctx, f.actor, "project-1", "manual-without-model", 0, docxWithCitationBody(t, ref.SectionID, "人工引用 [[source:source-1]]"), []DOCXCitationRef{ref})
	require.NoError(t, err)
}
