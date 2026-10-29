package workspace

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
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
	if _, err := core.ReadProjectAssetPolicyFromDB(context.Background(), db, "project-1", assetID); err != nil {
		t.Fatalf("legacy revision must remain readable: %v", err)
	}
	before := []byte(sourcePolicyQuote + "\nfirst suffix")
	after := []byte(sourcePolicyQuote + "\nother suffix")
	if len(before) != len(after) {
		t.Fatal("fixture must preserve length")
	}
	sum := sha256.Sum256([]byte(sourcePolicyQuote))
	storage := &adapterStorageStub{data: before}
	setFixtureUploadHash(t, db, before)
	currentAsset := refreshFixtureStorageAsset(t, db, assetID, storage)
	sources := &adapterStorageSourcesStub{assets: []evidence.Asset{currentAsset}, source: evidence.Source{ID: "source-1", AssetID: assetID, AssetRevision: currentAsset.AssetRevision, Status: evidence.SourceAvailable, QuotedText: sourcePolicyQuote, QuotedTextHash: hex.EncodeToString(sum[:]), Locator: "synthetic-location"}}
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

// The gateway refreshes the revision from current storage on each access.
// A new revision alone must never make overwritten bytes an uploaded original.
func TestOriginalIdentityAfterGatewayRefreshRejectsOverwrite(t *testing.T) {
	_, db, assetID := seedBoundSource(t)
	seedStorageBackedKnowledge(t, db)
	configureDirectTextStorageKnowledge(t, db)
	before := []byte(sourcePolicyQuote + "\nfirst suffix")
	storage := &adapterStorageStub{data: before}
	setFixtureUploadHash(t, db, before)
	runtime := NewGORMSourceRuntime(db, ProjectPorts{}, nil, nil, nil)
	runtime.ConnectOriginFiles(KnowledgeFileOriginReader{Service: storage})
	gateway := evidence.NewAssetGateway(runtime.bindings, allowTestAssetAuthorizer{})
	ctx := context.Background()
	resolve := func() evidence.Asset {
		t.Helper()
		result, err := gateway.ResolveAllowed(ctx, "project-1", evidence.Actor{}, []string{assetID})
		if err != nil || len(result.Allowed) != 1 {
			t.Fatalf("resolve: %+v, %v", result, err)
		}
		return result.Allowed[0]
	}
	first := resolve()
	if _, err := core.ReadProjectAssetPolicyFromDB(ctx, db, "project-1", assetID); err != nil {
		t.Fatalf("SHA revision must remain readable: %v", err)
	}
	reader := evidence.NewOriginReader(dbKnowledgeReader{db: db, files: KnowledgeFileOriginReader{Service: storage}}).(evidence.OriginSnapshotReader)
	if _, _, ok, err := reader.OriginSnapshot(ctx, "knowledge-1"); err != nil || !ok {
		t.Fatalf("original snapshot: ok=%v err=%v", ok, err)
	}
	// A failed storage read must degrade the snapshot without revising the
	// material simply because the storage service is temporarily unavailable.
	storage.err = errors.New("offline")
	if current := resolve(); current.AssetRevision != first.AssetRevision {
		t.Fatalf("unavailable storage changed revision: %d -> %d", first.AssetRevision, current.AssetRevision)
	}
	if _, err := core.ReadProjectAssetPolicyFromDB(ctx, db, "project-1", assetID); err != nil {
		t.Fatalf("stored revision must remain readable while storage is offline: %v", err)
	}
	if _, _, ok, err := reader.OriginSnapshot(ctx, "knowledge-1"); err != nil || ok {
		t.Fatalf("unavailable snapshot: ok=%v err=%v", ok, err)
	}
	storage.err = nil
	storage.data = []byte(sourcePolicyQuote + "\nother suffix")
	modified := resolve()
	if modified.AssetRevision <= first.AssetRevision {
		t.Fatal("changed stored bytes were not reflected in revision")
	}
	if _, _, ok, err := reader.OriginSnapshot(ctx, "knowledge-1"); err != nil || ok {
		t.Fatalf("overwritten snapshot accepted after refresh: ok=%v err=%v", ok, err)
	}
	if err := db.Exec("UPDATE knowledges SET file_hash='different' WHERE id='knowledge-1'").Error; err != nil {
		t.Fatal(err)
	}
	if _, err := core.ReadProjectAssetPolicyFromDB(ctx, db, "project-1", assetID); !errors.Is(err, core.ErrVersionConflict) {
		t.Fatalf("unrefreshed knowledge change must conflict, got %v", err)
	}
}
