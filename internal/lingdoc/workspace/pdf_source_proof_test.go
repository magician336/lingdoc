package workspace

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/evidence"
	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
	"github.com/Tencent/WeKnora/internal/types"
)

func TestPDFSourceRequiresUnchangedFileAndServerOwnedPageProof(t *testing.T) {
	_, db, assetID := seedBoundSource(t)
	seedStorageBackedKnowledge(t, db)
	if err := db.Exec("UPDATE knowledges SET file_type='pdf' WHERE id=?", "knowledge-1").Error; err != nil {
		t.Fatal(err)
	}
	// Synthetic bytes suffice here: this tests the storage/proof boundary, not PDF parsing.
	original := []byte("%PDF-1.7\nsynthetic-original")
	storage := &adapterStorageStub{data: original}
	setFixtureUploadHash(t, db, original)
	asset := refreshFixtureStorageAsset(t, db, assetID, storage)
	sources := &adapterStorageSourcesStub{assets: []evidence.Asset{asset}, source: evidence.Source{
		ID: "source-1", AssetID: assetID, AssetRevision: asset.AssetRevision, Status: evidence.SourceAvailable, QuotedText: sourcePolicyQuote,
	}}
	adapter := NewResearchAdapter(db, &adapterProjectsStub{}, sources, nil, storage, nil)
	actor := core.Actor{TenantID: 7, UserID: "reader", Role: types.TenantRoleOwner}
	read := func() core.ResearchSource {
		t.Helper()
		got, err := adapter.ReadResearchSource(policyContext(), actor, "project-1", "source-1")
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	if got := read(); got.StableOriginal || got.VerifiedPDFPage != 0 {
		t.Fatal("legacy PDF must be weak")
	}
	seedFixtureOriginProof(t, db, "source-1", "knowledge-1", 7, original)
	applySQLiteMigrations(t, db, "000062_chunk_pdf_page.up.sql")
	page := types.ChunkPDFPage{ChunkID: "source-1", TenantID: 7, KnowledgeID: "knowledge-1", Page: 2, TextSHA256: sha256Hex([]byte(sourcePolicyQuote))}
	if err := db.Create(&page).Error; err != nil {
		t.Fatal(err)
	}
	got := read()
	if !got.StableOriginal || got.VerifiedPDFPage != 2 {
		t.Fatalf("expected verified page 2: %+v", got)
	}
	if got.Locator == "" {
		t.Fatal("expected page locator")
	}
	storage.data = []byte("%PDF-1.7\nchanged-original")
	if got = read(); got.StableOriginal || got.VerifiedPDFPage != 0 {
		t.Fatal("changed PDF retained page proof")
	}
	storage.data = original
	if err := db.Model(&page).Update("text_sha256", sha256Hex([]byte("other"))).Error; err != nil {
		t.Fatal(err)
	}
	if got = read(); got.StableOriginal || got.VerifiedPDFPage != 0 {
		t.Fatal("changed text retained page proof")
	}
	if err := db.Model(&page).Updates(map[string]any{"text_sha256": sha256Hex([]byte(sourcePolicyQuote)), "tenant_id": 8}).Error; err != nil {
		t.Fatal(err)
	}
	if got = read(); got.StableOriginal || got.VerifiedPDFPage != 0 {
		t.Fatal("other tenant's page proof accepted")
	}
}
