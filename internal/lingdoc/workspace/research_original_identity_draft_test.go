package workspace

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"github.com/Tencent/WeKnora/internal/evidence"
	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
	"github.com/Tencent/WeKnora/internal/types"
)

// A stable quote is not a digest of the document. An in-place rewrite outside
// that quote can leave both the source and DB version unchanged. This test
// exercises the real local-file origin path; it does not claim immutable bytes.
func TestOriginalIdentityLocalRewriteOutsideQuoteIsUndetectableByCurrentSource(t *testing.T) {
	_, db, assetID := seedBoundSource(t)
	seedStorageBackedKnowledge(t, db)
	path := filepath.Join(t.TempDir(), "paper.txt")
	before := []byte(sourcePolicyQuote + "\nfirst suffix")
	after := []byte(sourcePolicyQuote + "\nother suffix")
	if len(before) != len(after) {
		t.Fatal("fixture must preserve length")
	}
	if err := os.WriteFile(path, before, 0600); err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("UPDATE knowledges SET file_path=? WHERE id=?", path, "knowledge-1").Error; err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(sourcePolicyQuote))
	sources := &adapterStorageSourcesStub{assets: []evidence.Asset{{ID: assetID, KnowledgeID: "knowledge-1", AssetRevision: 1, ProcessingState: evidence.AssetStateReady}}, source: evidence.Source{ID: "source-1", AssetID: assetID, AssetRevision: 1, Status: evidence.SourceAvailable, QuotedText: sourcePolicyQuote, QuotedTextHash: hex.EncodeToString(sum[:]), Locator: "synthetic-location"}}
	adapter := NewResearchAdapter(db, &adapterProjectsStub{}, sources, nil, nil, nil)
	actor := core.Actor{TenantID: 7, UserID: "reader", Role: types.TenantRoleOwner}
	first, err := adapter.ReadResearchSource(policyContext(), actor, "project-1", "source-1")
	if err != nil || !first.StableOriginal {
		t.Fatalf("before: stable=%v err=%v", first.StableOriginal, err)
	}
	var metadataBefore struct {
		FileHash string
		FileSize int64
	}
	if err = db.Table("knowledges").Select("file_hash,file_size").Where("id=?", "knowledge-1").Take(&metadataBefore).Error; err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, after, 0600); err != nil {
		t.Fatal(err)
	}
	second, err := adapter.ReadResearchSource(policyContext(), actor, "project-1", "source-1")
	if err != nil || !second.StableOriginal {
		t.Fatalf("after: stable=%v err=%v", second.StableOriginal, err)
	}
	var metadataAfter struct {
		FileHash string
		FileSize int64
	}
	if err = db.Table("knowledges").Select("file_hash,file_size").Where("id=?", "knowledge-1").Take(&metadataAfter).Error; err != nil {
		t.Fatal(err)
	}
	if sha256.Sum256(before) == sha256.Sum256(after) {
		t.Fatal("fixture did not change full original bytes")
	}
	if first.QuotedTextHash != second.QuotedTextHash || first.AssetRevision != second.AssetRevision || metadataBefore != metadataAfter {
		t.Fatal("fixture did not preserve the existing quote and DB signals")
	}
}
