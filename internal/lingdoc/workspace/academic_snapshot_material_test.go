package workspace

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/evidence"
	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"gorm.io/gorm"
)

// Generate a complete one-page native-text PDF. Tests seed ingestion proofs
// explicitly; they exercise snapshot verification, not PDF parsing itself.
func academicSnapshotTestPDF(text string) []byte {
	stream := "BT /F1 12 Tf 72 720 Td (" + text + ") Tj ET"
	objects := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Count 1 /Kids [3 0 R] >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Resources << /Font << /F1 4 0 R >> >> /Contents 5 0 R >>",
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>",
		fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(stream), stream),
	}
	pdf := "%PDF-1.4\n"
	offsets := []int{0}
	for i, object := range objects {
		offsets = append(offsets, len(pdf))
		pdf += fmt.Sprintf("%d 0 obj\n%s\nendobj\n", i+1, object)
	}
	xref := len(pdf)
	pdf += fmt.Sprintf("xref\n0 %d\n0000000000 65535 f \n", len(offsets))
	for _, offset := range offsets[1:] {
		pdf += fmt.Sprintf("%010d 00000 n \n", offset)
	}
	pdf += fmt.Sprintf("trailer << /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(offsets), xref)
	return []byte(pdf)
}

type academicSnapshotTestFixture struct {
	db       *gorm.DB
	adapter  *ResearchAdapter
	projects *adapterProjectsStub
	storage  *adapterStorageStub
	source   *adapterStorageSourcesStub
	actor    core.Actor
	raw      []byte
}

func newAcademicSnapshotTestFixture(t *testing.T, pdf bool) academicSnapshotTestFixture {
	t.Helper()
	_, db, assetID := seedBoundSource(t)
	seedStorageBackedKnowledge(t, db)
	configureDirectTextStorageKnowledge(t, db)
	raw := []byte(sourcePolicyQuote + "\r\nother parser-aligned text")
	if pdf {
		raw = academicSnapshotTestPDF(sourcePolicyQuote)
		if err := db.Exec("UPDATE knowledges SET file_type='pdf' WHERE id=?", "knowledge-1").Error; err != nil {
			t.Fatal(err)
		}
	}
	storage := &adapterStorageStub{data: raw}
	setFixtureUploadHash(t, db, raw)
	seedFixtureOriginProof(t, db, "source-1", "knowledge-1", 7, raw)
	asset := refreshFixtureStorageAsset(t, db, assetID, storage)
	if pdf {
		applySQLiteMigrations(t, db, "000062_chunk_pdf_page.up.sql")
		if err := db.Create(&types.ChunkPDFPage{ChunkID: "source-1", TenantID: 7, KnowledgeID: "knowledge-1", Page: 1, TextSHA256: sha256Hex([]byte(sourcePolicyQuote))}).Error; err != nil {
			t.Fatal(err)
		}
	}
	sources := &adapterStorageSourcesStub{assets: []evidence.Asset{asset}, source: evidence.Source{ID: "source-1", AssetID: assetID, AssetRevision: asset.AssetRevision, Status: evidence.SourceAvailable, QuotedText: sourcePolicyQuote, QuotedTextHash: sha256Hex([]byte(sourcePolicyQuote))}}
	projects := &adapterProjectsStub{}
	return academicSnapshotTestFixture{db: db, adapter: NewResearchAdapter(db, projects, sources, nil, storage, nil), projects: projects, storage: storage, source: sources, actor: core.Actor{TenantID: 7, UserID: "reader", Role: types.TenantRoleOwner}, raw: raw}
}

func TestAcademicSnapshotCapturesProvenUploadedTXTAndPDF(t *testing.T) {
	for _, pdf := range []bool{false, true} {
		name := "txt"
		if pdf {
			name = "pdf"
		}
		t.Run(name, func(t *testing.T) {
			f := newAcademicSnapshotTestFixture(t, pdf)
			material, err := f.adapter.ReadAcademicSnapshotMaterial(policyContext(), f.actor, "project-1", "chapter-1", "source-1")
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(material.Raw, f.raw) || material.SourceText != sourcePolicyQuote || material.RawSHA256 != sha256Hex(f.raw) || material.Source.SourceID != "source-1" || !material.Source.StableOriginal {
				t.Fatalf("incomplete captured source identity: %+v", material.Source)
			}
			if material.Source.ModelUseAllowed {
				t.Fatal("capturing a snapshot must not grant model use")
			}
			if pdf && material.Source.VerifiedPDFPage != 1 || !pdf && material.Source.VerifiedPDFPage != 0 {
				t.Fatalf("incorrect page proof: %d", material.Source.VerifiedPDFPage)
			}
			encoded, err := json.Marshal(material)
			if err != nil || strings.Contains(string(encoded), sourcePolicyQuote) || strings.Contains(string(encoded), material.RawSHA256) {
				t.Fatalf("snapshot material serialized: %s, %v", encoded, err)
			}
		})
	}
}

func TestAcademicSnapshotRejectsMissingProofOrPermission(t *testing.T) {
	t.Run("missing ingestion proof", func(t *testing.T) {
		f := newAcademicSnapshotTestFixture(t, false)
		if err := f.db.Exec("DELETE FROM chunk_origin_sha256 WHERE chunk_id=?", "source-1").Error; err != nil {
			t.Fatal(err)
		}
		if _, err := f.adapter.ReadAcademicSnapshotMaterial(policyContext(), f.actor, "project-1", "chapter-1", "source-1"); !errors.Is(err, core.ErrEvidenceIncomplete) {
			t.Fatalf("missing proof accepted: %v", err)
		}
	})
	t.Run("changed ingestion proof", func(t *testing.T) {
		f := newAcademicSnapshotTestFixture(t, false)
		if err := f.db.Exec("UPDATE chunk_origin_sha256 SET sha256=? WHERE chunk_id=?", sha256Hex([]byte("other")), "source-1").Error; err != nil {
			t.Fatal(err)
		}
		if _, err := f.adapter.ReadAcademicSnapshotMaterial(policyContext(), f.actor, "project-1", "chapter-1", "source-1"); !errors.Is(err, core.ErrEvidenceIncomplete) {
			t.Fatalf("wrong ingestion proof accepted: %v", err)
		}
	})
	t.Run("denied chapter", func(t *testing.T) {
		f := newAcademicSnapshotTestFixture(t, false)
		f.projects.denied = true
		if result, err := f.adapter.ReadAcademicSnapshotMaterial(policyContext(), f.actor, "project-1", "chapter-1", "source-1"); err == nil || len(result.Raw) != 0 || result.SourceText != "" {
			t.Fatalf("denied access returned material: %v", err)
		}
	})
	t.Run("PDF without native page proof", func(t *testing.T) {
		f := newAcademicSnapshotTestFixture(t, true)
		if err := f.db.Exec("DELETE FROM chunk_pdf_page WHERE chunk_id=?", "source-1").Error; err != nil {
			t.Fatal(err)
		}
		if _, err := f.adapter.ReadAcademicSnapshotMaterial(policyContext(), f.actor, "project-1", "chapter-1", "source-1"); !errors.Is(err, core.ErrEvidenceIncomplete) {
			t.Fatalf("PDF without native page proof accepted: %v", err)
		}
	})
	t.Run("PDF page proof mismatch", func(t *testing.T) {
		f := newAcademicSnapshotTestFixture(t, true)
		if err := f.db.Exec("UPDATE chunk_pdf_page SET text_sha256=? WHERE chunk_id=?", sha256Hex([]byte("other")), "source-1").Error; err != nil {
			t.Fatal(err)
		}
		if _, err := f.adapter.ReadAcademicSnapshotMaterial(policyContext(), f.actor, "project-1", "chapter-1", "source-1"); !errors.Is(err, core.ErrEvidenceIncomplete) {
			t.Fatalf("invalid PDF page proof accepted: %v", err)
		}
	})
	t.Run("oversized file", func(t *testing.T) {
		f := newAcademicSnapshotTestFixture(t, false)
		f.storage.data = bytes.Repeat([]byte("x"), evidence.MaxOriginBytes+1)
		if _, err := f.adapter.ReadAcademicSnapshotMaterial(policyContext(), f.actor, "project-1", "chapter-1", "source-1"); !errors.Is(err, core.ErrEvidenceIncomplete) {
			t.Fatalf("oversized file accepted: %v", err)
		}
	})
}

type revokingAcademicProjects struct {
	adapterProjectsStub
	calls int
}

func (p *revokingAcademicProjects) AuthorizeResourceBatch(_ context.Context, _ Actor, _ string, _ AuthorizationAction, resources []ResourceRef) ([]ResourceAuthorizationDecision, error) {
	p.calls++
	decisions := make([]ResourceAuthorizationDecision, len(resources))
	for i, ref := range resources {
		decisions[i] = ResourceAuthorizationDecision{Resource: ref, Allowed: p.calls < 3 && ref.ChapterID == "chapter-1"}
	}
	return decisions, nil
}

func TestAcademicSnapshotRechecksChapterPermissionAfterRead(t *testing.T) {
	f := newAcademicSnapshotTestFixture(t, false)
	p := &revokingAcademicProjects{}
	f.adapter.projects = p
	material, err := f.adapter.ReadAcademicSnapshotMaterial(policyContext(), f.actor, "project-1", "chapter-1", "source-1")
	if !errors.Is(err, core.ErrSourceUnavailable) || p.calls != 3 || len(material.Raw) != 0 || material.SourceText != "" {
		t.Fatalf("revoked permission returned material: err=%v calls=%d", err, p.calls)
	}
}

type changingAcademicStorage struct {
	interfaces.KnowledgeService
	first  []byte
	second []byte
	calls  int
}

func (s *changingAcademicStorage) GetKnowledgeFile(context.Context, string) (io.ReadCloser, string, error) {
	s.calls++
	if s.calls == 1 {
		return io.NopCloser(bytes.NewReader(s.first)), "source.txt", nil
	}
	return io.NopCloser(bytes.NewReader(s.second)), "source.txt", nil
}

func TestAcademicSnapshotRejectsStorageChangeBetweenProofReads(t *testing.T) {
	f := newAcademicSnapshotTestFixture(t, false)
	f.adapter.knowledge = &changingAcademicStorage{first: f.raw, second: []byte(sourcePolicyQuote + "\r\nchanged")}
	if material, err := f.adapter.ReadAcademicSnapshotMaterial(policyContext(), f.actor, "project-1", "chapter-1", "source-1"); !errors.Is(err, core.ErrEvidenceIncomplete) || len(material.Raw) != 0 {
		t.Fatalf("changed bytes escaped verification: %v", err)
	}
}
