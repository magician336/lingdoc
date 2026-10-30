package workspacecore

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/evidence"
)

// The PDF bytes here are deliberately synthetic. This test exercises the
// persisted proof and permission boundary, not PDF parsing.
func academicSnapshotReadFixture(t *testing.T, pdf bool) (agentFixture, EvidenceCard, AcademicEvidenceSnapshot) {
	t.Helper()
	f := newAgentFixture(t, false)
	seedNativeEvidence(t, f)
	seedCandidateProofFoundation(t, f)
	source := f.source.sources["source-1"]
	if pdf {
		source.VerifiedPDFPage = 3
		f.source.sources[source.SourceID] = source
	}
	db := f.svc.testDB()
	for _, statement := range []string{
		"ALTER TABLE knowledges ADD COLUMN type TEXT NOT NULL DEFAULT 'file'",
		"ALTER TABLE knowledges ADD COLUMN file_type TEXT NOT NULL DEFAULT 'txt'",
		"ALTER TABLE chunks ADD COLUMN chunk_type TEXT NOT NULL DEFAULT 'text'",
		"ALTER TABLE chunks ADD COLUMN content_revision INTEGER NOT NULL DEFAULT 0",
		"ALTER TABLE chunks ADD COLUMN start_at INTEGER NOT NULL DEFAULT 0",
		"ALTER TABLE chunks ADD COLUMN end_at INTEGER NOT NULL DEFAULT 0",
	} {
		if err := db.Exec(statement).Error; err != nil {
			t.Fatal(err)
		}
	}
	raw := []byte(source.QuotedText)
	fileType := "txt"
	if pdf {
		raw = []byte("%PDF-1.7\nsynthetic-only")
		fileType = "pdf"
	}
	rawSHA := sha256Hex(string(raw))
	fileMD5 := md5.Sum(raw)
	fileHash := hex.EncodeToString(fileMD5[:])
	if err := db.Exec("UPDATE knowledges SET type='file', file_type=?, file_hash=?, file_size=? WHERE id=?", fileType, fileHash, len(raw), source.KnowledgeID).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("UPDATE chunks SET chunk_type='text', start_at=0, end_at=? WHERE id=?", len([]rune(source.QuotedText)), source.SourceID).Error; err != nil {
		t.Fatal(err)
	}
	metadata, _ := json.Marshal(map[string]string{"original_content_sha256": rawSHA})
	fingerprint := evidence.FingerprintOf(evidence.KnowledgeSignal{KnowledgeID: source.KnowledgeID, ParseStatus: "completed", FileHash: fileHash, FileSize: int64(len(raw)), OriginalContentSHA256: rawSHA})
	if err := db.Exec("UPDATE lingdoc_asset_revisions SET metadata_json=?, content_hash=? WHERE asset_id=?", string(metadata), fingerprint.Digest, source.AssetID).Error; err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"000061_chunk_origin_sha256.up.sql", "000062_chunk_pdf_page.up.sql"} {
		body, err := os.ReadFile(filepath.Join("..", "..", "..", "migrations", "sqlite", name))
		if err != nil {
			t.Fatal(err)
		}
		if err = db.Exec(string(body)).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Exec("INSERT INTO chunk_origin_sha256(chunk_id,tenant_id,knowledge_id,sha256) VALUES(?,?,?,?)", source.SourceID, f.owner.TenantID, source.KnowledgeID, rawSHA).Error; err != nil {
		t.Fatal(err)
	}
	if pdf {
		if err := db.Exec("INSERT INTO chunk_pdf_page(chunk_id,tenant_id,knowledge_id,page,text_sha256) VALUES(?,?,?,?,?)", source.SourceID, f.owner.TenantID, source.KnowledgeID, 3, source.QuotedTextHash).Error; err != nil {
			t.Fatal(err)
		}
	}
	anchor := f.anchor(t, source.SourceID, "snapshot-read-anchor")
	claim := f.claim(t)
	assoc := seedDraftAssociation(t, f, "snapshot-read-association", source)
	input := SaveEvidenceCardInput{VersionExpectation: f.expectation(0), ClaimRef: &VersionRef{ID: claim.ID, Version: claim.Version}, AnchorRefs: []VersionRef{{ID: anchor.ID, Version: anchor.Version}}, AcademicAssociationRefs: []VersionRef{{ID: assoc.ID, Version: assoc.Version}}, SupportType: "supports", Scope: "synthetic", ScopeChapterIDs: []string{f.chapter.ID}, Confirmed: true}
	cardRaw, _, _, err := f.svc.SaveEvidenceCard(context.Background(), f.owner, f.project.ID, "snapshot-read-card", input)
	card := decodeAgentJSON[EvidenceCard](t, cardRaw, err)
	var rawRef, textRef AcademicOriginalSnapshotRef
	err = f.svc.repository.Transaction(context.Background(), TransactionOptions{}, func(tx Transaction) error {
		var e error
		rawRef, e = PutAcademicOriginalSnapshot(tx, f.owner.TenantID, f.project.ID, raw)
		if e != nil {
			return e
		}
		textRef, e = PutAcademicOriginalSnapshot(tx, f.owner.TenantID, f.project.ID, []byte(source.QuotedText))
		return e
	})
	if err != nil {
		t.Fatal(err)
	}
	snapshot := AcademicEvidenceSnapshot{SourceID: source.SourceID, KnowledgeID: source.KnowledgeID, AssetID: source.AssetID, AssetRevision: source.AssetRevision, Locator: source.Locator, RawRef: rawRef, TextRef: textRef, AnchorRefs: card.AnchorRefs, Associations: []AcademicSnapshotAssociation{{AssociationRef: VersionRef{ID: assoc.ID, Version: assoc.Version}, CandidateRef: assoc.CandidateRef, DOI: assoc.DOI, CatalogVersion: assoc.CatalogVersion, PolicySnapshotVersion: assoc.PolicySnapshotVersion}}}
	if pdf {
		snapshot.Page = 3
	}
	return f, card, snapshot
}

// This seeds the already-confirmed tuple so the consumption tests exercise
// the real read paths independently of confirmation's snapshot-material port.
func confirmedAcademicSnapshotFixture(t *testing.T) (agentFixture, EvidenceCard, AcademicEvidenceSnapshot) {
	t.Helper()
	f, card, snapshot := academicSnapshotReadFixture(t, false)
	err := f.svc.repository.Transaction(context.Background(), TransactionOptions{}, func(tx Transaction) error {
		current, old, err := loadStoredObject[EvidenceCard](tx, f.project.ID, EvidenceCardKind, card.ID)
		if err != nil {
			return err
		}
		current.Version++
		current.Status = "CONFIRMED"
		current.Confirmation = &EvidenceConfirmation{ConfirmedBy: f.owner.UserID, ConfirmedAt: time.Now().UTC(), CardVersion: current.Version, ClaimRef: *current.ClaimRef, AnchorRefs: current.AnchorRefs, ScopeChapterIDs: current.ScopeChapterIDs, ContextRevision: current.Basis.ContextRevision, Reason: "reviewed test snapshot", AcademicSnapshots: []AcademicEvidenceSnapshot{snapshot}}
		card = current
		return storeStoredObject(tx, old, f.project.ID, EvidenceCardKind, card.ID, card.Version, card)
	})
	if err != nil {
		t.Fatal(err)
	}
	return f, card, snapshot
}

func TestAcademicSnapshotConsumersUseStoredTextAndFailClosed(t *testing.T) {
	f, card, snapshot := confirmedAcademicSnapshotFixture(t)
	refs := []VersionRef{{ID: card.ID, Version: card.Version}}
	input := AgentContextInput{VersionExpectation: f.expectation(0), ChapterID: f.chapter.ID, CardRefs: refs, SensitiveContextConfirmed: true}
	// A mutable source reader may change its live body without changing the
	// authorized historical snapshot. Neither consumer may return that body.
	live := f.source.sources[snapshot.SourceID]
	live.QuotedText = "changed live text which must not be returned"
	live.QuotedTextHash = sha256Hex(live.QuotedText)
	f.source.sources[snapshot.SourceID] = live
	selection, err := f.svc.SelectGenerationEvidence(context.Background(), f.owner, f.project.ID, f.chapter.ID, refs, true)
	if err != nil || len(selection.Sources) != 1 || selection.Sources[0].QuotedText != stringMustReadSnapshot(t, f, snapshot.TextRef) {
		t.Fatalf("selection did not use stored text: %v", err)
	}
	pack, err := f.svc.BuildAgentContext(context.Background(), f.owner, f.project.ID, input)
	if err != nil || len(pack.SourceRefs) != 1 || pack.SourceRefs[0].QuotedText != selection.Sources[0].QuotedText {
		t.Fatalf("context did not use same stored text: %v", err)
	}
	withoutChapter := input
	withoutChapter.ChapterID = ""
	pack, err = f.svc.BuildAgentContext(context.Background(), f.owner, f.project.ID, withoutChapter)
	if err == nil || len(pack.SourceRefs) != 0 {
		t.Fatal("context without the confirmed chapter returned evidence")
	}
	live.ModelUseAllowed = false
	f.source.sources[snapshot.SourceID] = live
	selection, err = f.svc.SelectGenerationEvidence(context.Background(), f.owner, f.project.ID, f.chapter.ID, refs, true)
	if err == nil || len(selection.Sources) != 0 || len(selection.Cards) != 0 {
		t.Fatal("revoked model access returned evidence")
	}
	pack, err = f.svc.BuildAgentContext(context.Background(), f.owner, f.project.ID, input)
	if err == nil || len(pack.SourceRefs) != 0 {
		t.Fatal("revoked model access returned context")
	}
}

func TestAcademicSnapshotConsumerRejectsSupersededClaim(t *testing.T) {
	f, card, _ := confirmedAcademicSnapshotFixture(t)
	err := f.svc.repository.Transaction(context.Background(), TransactionOptions{}, func(tx Transaction) error {
		claim, old, err := loadStoredObject[ResearchClaim](tx, f.project.ID, ResearchClaimKind, card.ClaimRef.ID)
		if err != nil {
			return err
		}
		claim.Version++
		claim.Status = "superseded"
		return storeStoredObject(tx, old, f.project.ID, ResearchClaimKind, claim.ID, claim.Version, claim)
	})
	if err != nil {
		t.Fatal(err)
	}
	refs := []VersionRef{{ID: card.ID, Version: card.Version}}
	selection, err := f.svc.SelectGenerationEvidence(context.Background(), f.owner, f.project.ID, f.chapter.ID, refs, true)
	if err == nil || len(selection.Sources) != 0 || len(selection.Cards) != 0 {
		t.Fatal("superseded claim returned evidence")
	}
	pack, err := f.svc.BuildAgentContext(context.Background(), f.owner, f.project.ID, AgentContextInput{VersionExpectation: f.expectation(0), ChapterID: f.chapter.ID, CardRefs: refs})
	if err == nil || len(pack.SourceRefs) != 0 {
		t.Fatal("superseded claim returned context")
	}
}

func TestAcademicSnapshotConsumerRejectsRevokedAssociation(t *testing.T) {
	f, card, snapshot := confirmedAcademicSnapshotFixture(t)
	err := f.svc.repository.Transaction(context.Background(), TransactionOptions{}, func(tx Transaction) error {
		association, old, err := loadStoredObject[AcademicSourceAssociation](tx, f.project.ID, AcademicSourceAssociationKind, snapshot.Associations[0].AssociationRef.ID)
		if err != nil {
			return err
		}
		association.Version++
		association.Status = "revoked"
		return storeStoredObject(tx, old, f.project.ID, AcademicSourceAssociationKind, association.ID, association.Version, association)
	})
	if err != nil {
		t.Fatal(err)
	}
	refs := []VersionRef{{ID: card.ID, Version: card.Version}}
	selection, err := f.svc.SelectGenerationEvidence(context.Background(), f.owner, f.project.ID, f.chapter.ID, refs, true)
	if err == nil || len(selection.Sources) != 0 || len(selection.Cards) != 0 {
		t.Fatal("revoked association returned evidence")
	}
	pack, err := f.svc.BuildAgentContext(context.Background(), f.owner, f.project.ID, AgentContextInput{VersionExpectation: f.expectation(0), ChapterID: f.chapter.ID, CardRefs: refs})
	if err == nil || len(pack.SourceRefs) != 0 {
		t.Fatal("revoked association returned context")
	}
}

func stringMustReadSnapshot(t *testing.T, f agentFixture, ref AcademicOriginalSnapshotRef) string {
	t.Helper()
	var raw []byte
	err := f.svc.repository.Transaction(context.Background(), TransactionOptions{ReadOnly: true}, func(tx Transaction) error {
		var err error
		raw, err = ReadAcademicOriginalSnapshot(tx, f.owner.TenantID, f.project.ID, ref)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestAcademicSnapshotConsumptionChecksProofAndReturnsStoredText(t *testing.T) {
	for _, pdf := range []bool{false, true} {
		t.Run(map[bool]string{false: "text", true: "pdf"}[pdf], func(t *testing.T) {
			f, card, snapshot := academicSnapshotReadFixture(t, pdf)
			read := func(snapshot AcademicEvidenceSnapshot) (map[string]AgentSourceRef, error) {
				var out map[string]AgentSourceRef
				err := f.svc.repository.Transaction(context.Background(), TransactionOptions{ReadOnly: true}, func(tx Transaction) error {
					var e error
					out, e = f.svc.validateAcademicEvidenceSnapshots(context.Background(), tx, f.owner, f.project, card, []AcademicEvidenceSnapshot{snapshot}, true)
					return e
				})
				return out, err
			}
			got, err := read(snapshot)
			if err != nil || got[snapshot.SourceID].QuotedText != f.source.sources[snapshot.SourceID].QuotedText {
				t.Fatalf("snapshot rejected: %v", err)
			}
			bad := snapshot
			bad.TextRef.Size++
			if got, err = read(bad); err == nil || len(got) != 0 {
				t.Fatal("corrupt text ref accepted or leaked")
			}
			bad = snapshot
			bad.AssetRevision++
			if got, err = read(bad); err == nil || len(got) != 0 {
				t.Fatal("different asset revision accepted or leaked")
			}
			if pdf {
				bad = snapshot
				bad.Page++
				if got, err = read(bad); err == nil || len(got) != 0 {
					t.Fatal("unproven PDF page accepted")
				}
			}
		})
	}
}
