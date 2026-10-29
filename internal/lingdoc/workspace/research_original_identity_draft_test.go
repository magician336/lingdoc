package workspace

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"github.com/Tencent/WeKnora/internal/evidence"
	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
	"github.com/Tencent/WeKnora/internal/types"
)

// A stable quote is not a digest of the document. Rewriting bytes outside the
// quote must invalidate the full-original proof even when the quote is intact.
func TestOriginalIdentityRewriteOutsideQuoteInvalidatesStableOriginal(t *testing.T) {
	_, db, assetID := seedBoundSource(t)
	seedStorageBackedKnowledge(t, db)
	configureDirectTextStorageKnowledge(t, db)
	before := []byte(sourcePolicyQuote + "\nfirst suffix")
	after := []byte(sourcePolicyQuote + "\nother suffix")
	if len(before) != len(after) {
		t.Fatal("fixture must preserve length")
	}
	sum := sha256.Sum256([]byte(sourcePolicyQuote))
	fullSum := sha256.Sum256(before)
	storage := &adapterStorageStub{data: before}
	sources := &adapterStorageSourcesStub{assets: []evidence.Asset{{ID: assetID, KnowledgeID: "knowledge-1", AssetRevision: 1, ProcessingState: evidence.AssetStateReady, OriginalContentSHA256: hex.EncodeToString(fullSum[:])}}, source: evidence.Source{ID: "source-1", AssetID: assetID, AssetRevision: 1, Status: evidence.SourceAvailable, QuotedText: sourcePolicyQuote, QuotedTextHash: hex.EncodeToString(sum[:]), Locator: "synthetic-location"}}
	adapter := NewResearchAdapter(db, &adapterProjectsStub{}, sources, nil, storage, nil)
	actor := core.Actor{TenantID: 7, UserID: "reader", Role: types.TenantRoleOwner}
	first, err := adapter.ReadResearchSource(policyContext(), actor, "project-1", "source-1")
	if err != nil || !first.StableOriginal {
		t.Fatalf("before: stable=%v err=%v", first.StableOriginal, err)
	}
	storage.data = after
	second, err := adapter.ReadResearchSource(policyContext(), actor, "project-1", "source-1")
	if err != nil || second.StableOriginal {
		t.Fatalf("after: stable=%v err=%v", second.StableOriginal, err)
	}
	if sha256.Sum256(before) == sha256.Sum256(after) {
		t.Fatal("fixture did not change full original bytes")
	}
	if first.QuotedTextHash != second.QuotedTextHash || first.AssetRevision != second.AssetRevision {
		t.Fatal("fixture did not preserve the existing quote and source revision")
	}
}
