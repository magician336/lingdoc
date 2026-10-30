package workspace

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/Tencent/WeKnora/internal/lingdoc/delivery"
	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type academicSnapshotHTTPFixture struct {
	db       *gorm.DB
	engine   *gin.Engine
	projects *core.Service
	adapter  *ResearchAdapter
	runtime  *SourceRuntime
	actor    core.Actor
	storage  *adapterStorageStub
	assetID  string
	sourceID string
	raw      []byte
}

func newAcademicSnapshotHTTPFixture(t *testing.T, pdf bool) academicSnapshotHTTPFixture {
	t.Helper()
	_, db, assetID := seedBoundSource(t)
	seedDeliveryWorkspace(t, db, deliveryMethodChapter())
	ensureImpactRouteTables(t, db)
	applyCreateTablesFromMigration(t, db, "000000_init.up.sql", "knowledge_bases")
	applySQLiteMigrations(t, db, "000033_lingdoc_working_copies.up.sql")
	applyCreateTablesFromMigration(t, db, "000037_lingdoc_full_workflow.up.sql", "lingdoc_g2_objects", "lingdoc_g2_object_versions")
	applySQLiteMigrations(t, db, "000063_lingdoc_academic_original_snapshots.up.sql")
	for _, statement := range []string{
		"ALTER TABLE chunks ADD COLUMN source_content TEXT NOT NULL DEFAULT ''",
		"INSERT INTO knowledge_bases(id,tenant_id,name,embedding_model_id,summary_model_id) VALUES('kb-1',7,'synthetic-kb','embed-1','summary-1')",
		"UPDATE knowledges SET file_type='txt', file_path='resource://synthetic-storage-object' WHERE id='knowledge-1'",
		"UPDATE chunks SET source_content=content WHERE id='source-1'",
	} {
		if err := db.Exec(statement).Error; err != nil {
			t.Fatal(err)
		}
	}
	seedDeliveryBoundTemplateCopy(t, db, "snapshot-http-copy")
	raw := []byte(sourcePolicyQuote + "\r\nSynthetic suffix")
	sourceID := "source-1"
	var captured struct {
		Raw       []byte `json:"raw"`
		ChunkID   string `json:"chunk_id"`
		Text      string `json:"text"`
		OriginSHA string `json:"origin_sha"`
		Page      int    `json:"page"`
		TextSHA   string `json:"text_sha"`
	}
	if pdf {
		raw = academicSnapshotTestPDF(sourcePolicyQuote)
		if path := os.Getenv("LINGDOC_TEST_PDF_CAPTURE"); path != "" {
			data, e := os.ReadFile(path)
			if e != nil {
				t.Fatal(e)
			}
			if e = json.Unmarshal(data, &captured); e != nil {
				t.Fatal(e)
			}
			if captured.ChunkID == "" || captured.Text != sourcePolicyQuote || captured.OriginSHA != sha256Hex(captured.Raw) || captured.TextSHA != sha256Hex([]byte(captured.Text)) || captured.Page != 1 {
				t.Fatal("captured real parser proof is inconsistent")
			}
			raw, sourceID = captured.Raw, captured.ChunkID
			if e = db.Exec("UPDATE chunks SET id=? WHERE id='source-1'", sourceID).Error; e != nil {
				t.Fatal(e)
			}
		}
		if err := db.Exec("UPDATE knowledges SET file_type='pdf' WHERE id='knowledge-1'").Error; err != nil {
			t.Fatal(err)
		}
	}
	storage := &adapterStorageStub{data: raw}
	setFixtureUploadHash(t, db, raw)
	if captured.ChunkID == "" {
		seedFixtureOriginProof(t, db, sourceID, "knowledge-1", 7, raw)
	} else {
		applySQLiteMigrations(t, db, "000061_chunk_origin_sha256.up.sql")
		if err := db.Create(&types.ChunkOriginSHA256{ChunkID: sourceID, TenantID: 7, KnowledgeID: "knowledge-1", SHA256: captured.OriginSHA}).Error; err != nil {
			t.Fatal(err)
		}
	}
	if pdf {
		applySQLiteMigrations(t, db, "000062_chunk_pdf_page.up.sql")
		if err := db.Create(&types.ChunkPDFPage{ChunkID: sourceID, TenantID: 7, KnowledgeID: "knowledge-1", Page: 1, TextSHA256: sha256Hex([]byte(sourcePolicyQuote))}).Error; err != nil {
			t.Fatal(err)
		}
	}
	shares := candidateOneConnShares{}
	runtime := NewGORMSourceRuntime(db, ProjectPorts{}, shares, nil, delivery.NewFixedTemplateReader())
	projects := core.NewServiceWithSources(core.NewGORMRepository(db), core.ContractDemoTemplate{}, runtime.WorkspaceSourcePolicy())
	runtime.ConnectProjects(ProjectPorts{Authorizer: projects, Resources: projects, Projects: projects, Context: projects})
	connectAggregateCapabilities(runtime, projects)
	runtime.EnableFullWorkflowPolicy()
	runtime.ConnectOriginFiles(KnowledgeFileOriginReader{Service: storage})
	if err := runtime.bindings.RefreshAsset(context.Background(), "project-1", assetID); err != nil {
		t.Fatal(err)
	}
	current, err := runtime.bindings.CurrentAsset(context.Background(), "project-1", assetID)
	if err != nil {
		t.Fatal(err)
	}
	policy := core.ProjectAssetPolicy{ID: assetID, ProjectID: "project-1", AssetID: assetID, Version: 1, AssetRevision: int64(current.AssetRevision), PolicyVersion: 1, Scope: "project", Purpose: "synthetic test", ModelUseAllowed: true, AllowedUserIDs: []string{}, CreatedBy: "reader", GrantedBy: "reader", Reason: "synthetic test"}
	policyJSON, err := json.Marshal(policy)
	if err != nil {
		t.Fatal(err)
	}
	if err = core.NewTransactionAdapter(db).(core.ObjectStore).InsertObject(core.StoredObject{ProjectID: "project-1", Kind: core.AssetPolicyKind, ID: assetID, Version: 1, Payload: policyJSON}); err != nil {
		t.Fatal(err)
	}
	adapter := NewResearchAdapter(db, projects, runtime, shares, storage, nil)
	if err = adapter.ConfigureAcademicResearchPolicy(syntheticGlobalAcademicCatalog(), &syntheticResearchBudgetLedger{}); err != nil {
		t.Fatal(err)
	}
	projects.ConfigureAgentRuntime(core.AgentRuntime{Sources: adapter, Research: adapter})
	engine := gin.New()
	RegisterAgentRoutes(engine.Group("/api"), AgentRoutePorts{Research: projects, Imports: projects, EvidenceCards: projects, Claims: projects, Anchors: projects, AgentSessions: projects, Proposals: projects, Context: projects, ChangeSets: projects})
	return academicSnapshotHTTPFixture{db: db, engine: engine, projects: projects, adapter: adapter, runtime: runtime, actor: core.Actor{TenantID: 7, UserID: "reader", Role: types.TenantRoleOwner}, storage: storage, assetID: assetID, sourceID: sourceID, raw: raw}
}

// Keep the real project authorizer while denying the selected chapter asset
// resource. This tests scope propagation without inventing a storage grant.
type academicSnapshotChapterDeny struct {
	core.AssetUseAuthorizer
	chapter string
}

type academicSnapshotPostCommitReader struct {
	*ResearchAdapter
	db          *gorm.DB
	cardID      string
	onCommitted func()
	once        sync.Once
}

func (r *academicSnapshotPostCommitReader) ReadResearchSourceForChapter(ctx context.Context, actor core.Actor, projectID, chapterID, sourceID string) (core.ResearchSource, error) {
	if chapterID == "chapter-2" {
		var row struct {
			PayloadJSON string `gorm:"column:payload_json"`
		}
		if err := r.db.Table("lingdoc_g2_objects").Where("project_id=? AND kind=? AND id=?", projectID, core.EvidenceCardKind, r.cardID).Take(&row).Error; err == nil {
			var card core.EvidenceCard
			if json.Unmarshal([]byte(row.PayloadJSON), &card) == nil && card.Status == "CONFIRMED" {
				r.once.Do(r.onCommitted)
			}
		}
	}
	return r.ResearchAdapter.ReadResearchSourceForChapter(ctx, actor, projectID, chapterID, sourceID)
}

func (d academicSnapshotChapterDeny) AuthorizeResourceBatch(ctx context.Context, actor core.Actor, projectID string, action core.AuthorizationAction, refs []core.ResourceRef) ([]core.ResourceAuthorizationDecision, error) {
	decisions, err := d.AssetUseAuthorizer.AuthorizeResourceBatch(ctx, actor, projectID, action, refs)
	if err != nil {
		return nil, err
	}
	for i, ref := range refs {
		if ref.ChapterID == d.chapter {
			decisions[i].Allowed = false
		}
	}
	return decisions, nil
}

func (f academicSnapshotHTTPFixture) request(user, method, route, key, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, route, strings.NewReader(body)).WithContext(actorContext(user))
	req.Header.Set("Content-Type", "application/json")
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	out := httptest.NewRecorder()
	f.engine.ServeHTTP(out, req)
	return out
}

func academicSnapshotHTTPData(t *testing.T, response *httptest.ResponseRecorder, status int, dst any) {
	t.Helper()
	if response.Code != status {
		t.Fatalf("HTTP %d want %d: %s", response.Code, status, response.Body.String())
	}
	if dst != nil {
		var envelope struct {
			Data json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(envelope.Data, dst); err != nil {
			t.Fatal(err)
		}
	}
}

func TestAcademicSnapshotConfirmationHTTPWithRealStorageBytes(t *testing.T) {
	for _, tc := range []struct {
		name     string
		pdf      bool
		scenario string
	}{{"uploaded-txt-original", false, "original"}, {"uploaded-txt-catalog", false, "catalog"}, {"uploaded-txt-postcommit", false, "postcommit"}, {"native-text-pdf-original", true, "original"}, {"native-text-pdf-catalog", true, "catalog"}} {
		t.Run(tc.name, func(t *testing.T) {
			pdf := tc.pdf
			f := newAcademicSnapshotHTTPFixture(t, pdf)
			ctx := policyContext()
			project, err := f.projects.GetProject(ctx, f.actor, "project-1")
			if err != nil {
				t.Fatal(err)
			}
			expectation := func(version int64) string {
				return fmt.Sprintf(`"expected_project_version":%d,"expected_context_revision":%d,"expected_version":%d`, project.ProjectVersion, project.CurrentContextRevision, version)
			}
			const route = "/api/projects/project-1"
			var catalog core.AcademicSourceCatalog
			academicSnapshotHTTPData(t, f.request("reader", http.MethodPut, route+"/academic-source-catalog", "snapshot-catalog", `{`+expectation(0)+`,"rules":[{"id":"journal","source_type":"journal","venue_name":"Synthetic","venue_ids":["1234-5678"]}]}`), http.StatusOK, &catalog)
			policyVersion := academicCatalogSnapshot(syntheticGlobalAcademicCatalog(), catalog).Version
			candidate := core.ImportCandidate{ID: "snapshot-candidate", ProjectID: "project-1", Version: 1, DOI: "10.5555/snapshot", Status: "imported", SelectionType: "academic_metadata", SaveMode: "metadata_only", CatalogVersion: catalog.SourceCatalogVersion, PolicySnapshotVersion: policyVersion, PolicyStatus: "approved", RetractionStatus: "unknown", DuplicateStatus: "unique", AcquisitionStatus: "metadata_only", Basis: core.VersionBasis{ProjectVersion: project.ProjectVersion, SpecRevision: project.SpecRevision, ContextRevision: project.CurrentContextRevision}}
			payload, err := json.Marshal(candidate)
			if err != nil {
				t.Fatal(err)
			}
			if err = core.NewTransactionAdapter(f.db).(core.ObjectStore).InsertObject(core.StoredObject{ProjectID: "project-1", Kind: core.ImportCandidateKind, ID: candidate.ID, Version: candidate.Version, Payload: payload}); err != nil {
				t.Fatal(err)
			}
			var anchor core.SourceAnchor
			academicSnapshotHTTPData(t, f.request("reader", http.MethodPost, route+"/source-anchors", "snapshot-anchor", `{`+expectation(0)+`,"source_id":`+strconv.Quote(f.sourceID)+`,"selection_start":0,"selection_end":`+strconv.Itoa(len([]rune(sourcePolicyQuote)))+`,"extraction_type":"native_text","confirmed":true}`), http.StatusCreated, &anchor)
			var claim core.ResearchClaim
			academicSnapshotHTTPData(t, f.request("reader", http.MethodPost, route+"/research-claims", "snapshot-claim", `{`+expectation(0)+`,"text":"Synthetic conclusion","scope":"synthetic","reason":"test","confirmed":true}`), http.StatusOK, &claim)
			associationBody := `{` + expectation(candidate.Version) + `,"candidate_id":"` + candidate.ID + `","source_id":` + strconv.Quote(f.sourceID) + `,"expected_asset_revision":` + strconv.Itoa(anchor.AssetRevision) + `,"expected_quoted_text_hash":"` + anchor.QuotedTextHash + `","expected_locator":` + strconv.Quote(anchor.Locator) + `,"confirmed":true,"reason":"synthetic human review"}`
			var association core.AcademicSourceAssociation
			academicSnapshotHTTPData(t, f.request("reader", http.MethodPost, route+"/academic-source-associations", "snapshot-association", associationBody), http.StatusCreated, &association)
			var card core.EvidenceCard
			cardBody := `{` + expectation(0) + `,"claim_ref":{"id":"` + claim.ID + `","version":` + strconv.FormatInt(claim.Version, 10) + `},"anchor_refs":[{"id":"` + anchor.ID + `","version":` + strconv.FormatInt(anchor.Version, 10) + `}],"academic_association_refs":[{"id":"` + association.ID + `","version":` + strconv.FormatInt(association.Version, 10) + `}],"support_type":"supports","scope":"synthetic","scope_chapter_ids":["chapter-2"],"limitations":"synthetic only","confirmed":true}`
			academicSnapshotHTTPData(t, f.request("reader", http.MethodPost, route+"/evidence-cards", "snapshot-card", cardBody), http.StatusCreated, &card)
			confirmBody := `{` + expectation(card.Version) + `,"confirmed":true,"reason":"synthetic human review"}`
			confirmRoute := route + "/evidence-cards/" + card.ID + "/confirm"
			denied := f.request("outsider", http.MethodPost, confirmRoute, "snapshot-denied", confirmBody)
			if denied.Code < 400 || denied.Code >= 500 || strings.Contains(denied.Body.String(), sourcePolicyQuote) {
				t.Fatalf("unauthorized confirmation returned %d", denied.Code)
			}
			f.adapter.projects = academicSnapshotChapterDeny{AssetUseAuthorizer: f.projects, chapter: "chapter-2"}
			chapterDenied := f.request("reader", http.MethodPost, confirmRoute, "snapshot-chapter-denied", confirmBody)
			f.adapter.projects = f.projects
			if chapterDenied.Code < 400 || chapterDenied.Code >= 500 || strings.Contains(chapterDenied.Body.String(), sourcePolicyQuote) {
				t.Fatalf("chapter-restricted confirmation was accepted: %d", chapterDenied.Code)
			}
			rawHash, textHash := sha256Hex(f.raw), sha256Hex([]byte(sourcePolicyQuote))
			if rawHash == textHash {
				t.Fatal("fixture must use different raw and text snapshots")
			}
			// The injected failure fires only after the raw insert is visible in
			// the same transaction, proving this is the second snapshot write.
			if err = f.db.Exec("CREATE TRIGGER synthetic_snapshot_failure BEFORE INSERT ON lingdoc_academic_original_snapshots WHEN NEW.sha256 = '" + textHash + "' AND EXISTS (SELECT 1 FROM lingdoc_academic_original_snapshots WHERE sha256 = '" + rawHash + "') BEGIN SELECT RAISE(ABORT, 'snapshot failure'); END").Error; err != nil {
				t.Fatal(err)
			}
			failed := f.request("reader", http.MethodPost, confirmRoute, "snapshot-rollback", confirmBody)
			if failed.Code != http.StatusInternalServerError {
				t.Fatalf("snapshot failure did not reach rollback seam: status=%d body=%s", failed.Code, failed.Body.String())
			}
			var count int64
			if err = f.db.Table("lingdoc_academic_original_snapshots").Count(&count).Error; err != nil || count != 0 {
				t.Fatalf("partial snapshots after rollback: %d %v", count, err)
			}
			var currentRow struct {
				PayloadJSON string `gorm:"column:payload_json"`
			}
			if err = f.db.Table("lingdoc_g2_objects").Where("project_id=? AND kind=? AND id=?", "project-1", core.EvidenceCardKind, card.ID).Take(&currentRow).Error; err != nil {
				t.Fatal(err)
			}
			var afterFailure core.EvidenceCard
			if err = json.Unmarshal([]byte(currentRow.PayloadJSON), &afterFailure); err != nil {
				t.Fatal(err)
			}
			if afterFailure.Version != card.Version || afterFailure.Status != card.Status || afterFailure.Confirmation != nil {
				t.Fatalf("card changed despite snapshot rollback: %+v", afterFailure)
			}
			if err = f.db.Table("lingdoc_g2_object_versions").Where("project_id=? AND kind=? AND id=?", "project-1", core.EvidenceCardKind, card.ID).Count(&count).Error; err != nil || count != 1 {
				t.Fatalf("card history changed despite snapshot rollback: %d %v", count, err)
			}
			if err = f.db.Exec("DROP TRIGGER synthetic_snapshot_failure").Error; err != nil {
				t.Fatal(err)
			}
			if tc.scenario == "postcommit" {
				started, revoked := make(chan struct{}), make(chan struct{})
				errors := make(chan error, 1)
				reader := &academicSnapshotPostCommitReader{ResearchAdapter: f.adapter, db: f.db, cardID: card.ID, onCommitted: func() { close(started); <-revoked }}
				f.projects.ConfigureAgentRuntime(core.AgentRuntime{Sources: reader, Research: f.adapter})
				go func() {
					<-started
					errors <- f.db.Exec("UPDATE tenant_members SET status='suspended' WHERE tenant_id=7 AND user_id='reader'").Error
					close(revoked)
				}()
				response := f.request("reader", http.MethodPost, confirmRoute, "snapshot-confirm", confirmBody)
				if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "confirmation_committed_unreadable") || strings.Contains(response.Body.String(), sourcePolicyQuote) {
					t.Fatalf("post-commit receipt status=%d body=%s", response.Code, response.Body.String())
				}
				if err = <-errors; err != nil {
					t.Fatal(err)
				}
				if err = f.db.Exec("UPDATE tenant_members SET status='active' WHERE tenant_id=7 AND user_id='reader'").Error; err != nil {
					t.Fatal(err)
				}
			}
			var confirmed core.EvidenceCard
			academicSnapshotHTTPData(t, f.request("reader", http.MethodPost, confirmRoute, "snapshot-confirm", confirmBody), http.StatusOK, &confirmed)
			if confirmed.Status != "CONFIRMED" || confirmed.Confirmation == nil || len(confirmed.Confirmation.AcademicSnapshots) != 1 {
				t.Fatalf("snapshot not confirmed: %+v", confirmed)
			}
			refs := []core.VersionRef{{ID: confirmed.ID, Version: confirmed.Version}}
			contextBody := `{` + expectation(0) + `,"chapter_id":"chapter-2","card_refs":[{"id":"` + confirmed.ID + `","version":` + strconv.FormatInt(confirmed.Version, 10) + `}],"sensitive_context_confirmed":true}`
			checkConsumers := func(wantAllowed bool, label string) {
				t.Helper()
				selection, selectionErr := f.projects.SelectGenerationEvidence(ctx, f.actor, "project-1", "chapter-2", refs, true)
				response := f.request("reader", http.MethodPost, route+"/agent-context", "", contextBody)
				if wantAllowed {
					if selectionErr != nil || len(selection.Sources) != 1 || selection.Sources[0].QuotedText != sourcePolicyQuote {
						t.Fatalf("%s: selection did not use snapshot: %v", label, selectionErr)
					}
					var pack core.AgentContextPackage
					academicSnapshotHTTPData(t, response, http.StatusOK, &pack)
					if len(pack.SourceRefs) != 1 || pack.SourceRefs[0].QuotedText != sourcePolicyQuote {
						t.Fatalf("%s: context did not use snapshot", label)
					}
				} else {
					if selectionErr == nil || len(selection.Sources) != 0 || len(selection.Cards) != 0 {
						t.Fatalf("%s: selection disclosed evidence: %v", label, selectionErr)
					}
					if response.Code < 400 || strings.Contains(response.Body.String(), sourcePolicyQuote) {
						t.Fatalf("%s: context disclosed evidence: HTTP %d", label, response.Code)
					}
				}
			}
			checkConsumers(true, "confirmed")
			f.adapter.projects = academicSnapshotChapterDeny{AssetUseAuthorizer: f.projects, chapter: "chapter-2"}
			checkConsumers(false, "chapter access denied")
			f.adapter.projects = f.projects
			var stored struct {
				Content []byte `gorm:"column:content"`
			}
			ref := confirmed.Confirmation.AcademicSnapshots[0].RawRef
			if err = f.db.Table("lingdoc_academic_original_snapshots").Where("project_id=? AND sha256=?", "project-1", ref.SHA256).Take(&stored).Error; err != nil || !bytes.Equal(stored.Content, f.raw) {
				t.Fatalf("stored raw differs: %v", err)
			}
			replay := f.request("reader", http.MethodPost, confirmRoute, "snapshot-confirm", confirmBody)
			if replay.Code != http.StatusOK || strings.Contains(replay.Body.String(), sourcePolicyQuote) {
				t.Fatalf("confirmation replay failed or leaked contents: %d", replay.Code)
			}
			// Inject a server-owned retraction observation without changing the
			// old candidate, to verify the replay rechecks the durable gate.
			observationID := sha256Hex([]byte(strings.ToLower(candidate.DOI)))
			observationJSON, err := json.Marshal(map[string]string{"doi": candidate.DOI, "observed_by": "synthetic-test"})
			if err != nil {
				t.Fatal(err)
			}
			if err = core.NewTransactionAdapter(f.db).(core.ObjectStore).InsertObject(core.StoredObject{ProjectID: "project-1", Kind: "academic_retraction", ID: observationID, Version: 1, Payload: observationJSON}); err != nil {
				t.Fatal(err)
			}
			retracted := f.request("reader", http.MethodPost, confirmRoute, "snapshot-confirm", confirmBody)
			if retracted.Code < 400 || retracted.Code >= 500 || strings.Contains(retracted.Body.String(), sourcePolicyQuote) {
				t.Fatalf("retracted evidence replay accepted: %d", retracted.Code)
			}
			checkConsumers(false, "retracted")
			// Reset only this test-only injected observation, so that the next
			// assertion independently exercises original-file replacement.
			for _, table := range []string{"lingdoc_g2_object_versions", "lingdoc_g2_objects"} {
				if err = f.db.Exec("DELETE FROM "+table+" WHERE project_id=? AND kind=? AND id=?", "project-1", "academic_retraction", observationID).Error; err != nil {
					t.Fatal(err)
				}
			}
			if restored := f.request("reader", http.MethodPost, confirmRoute, "snapshot-confirm", confirmBody); restored.Code != http.StatusOK {
				t.Fatalf("test observation reset did not restore control: %d", restored.Code)
			}
			if err = f.db.Exec("UPDATE tenant_members SET status='disabled' WHERE tenant_id=7 AND user_id='reader'").Error; err != nil {
				t.Fatal(err)
			}
			deniedReplay := f.request("reader", http.MethodPost, confirmRoute, "snapshot-confirm", confirmBody)
			if deniedReplay.Code < 400 || strings.Contains(deniedReplay.Body.String(), sourcePolicyQuote) {
				t.Fatalf("revoked replay disclosed data: %d", deniedReplay.Code)
			}
			checkConsumers(false, "tenant access revoked")
			if err = f.db.Exec("UPDATE tenant_members SET status='active' WHERE tenant_id=7 AND user_id='reader'").Error; err != nil {
				t.Fatal(err)
			}
			checkConsumers(true, "access restored")
			if tc.scenario == "original" {
				f.storage.data = []byte("changed original")
				changed := f.request("reader", http.MethodPost, confirmRoute, "snapshot-confirm", confirmBody)
				if changed.Code < 400 || changed.Code >= 500 || strings.Contains(changed.Body.String(), sourcePolicyQuote) {
					t.Fatalf("changed original replay accepted: %d", changed.Code)
				}
				checkConsumers(false, "original replaced")
			} else {
				academicSnapshotHTTPData(t, f.request("reader", http.MethodPut, route+"/academic-source-catalog", "snapshot-catalog-change", `{`+expectation(catalog.Version)+`,"rules":[{"id":"journal","source_type":"journal","venue_name":"Changed","venue_ids":["1234-5678"]}]}`), http.StatusOK, nil)
				checkConsumers(false, "catalog changed")
			}
		})
	}
}
