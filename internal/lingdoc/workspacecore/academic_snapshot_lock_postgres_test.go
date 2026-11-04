package workspacecore

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/evidence"
)

// This exercises the same native-state lock used by confirmation, using the
// production PostgreSQL migration chain and independent transactions.
func TestPostgresAcademicSnapshotLocksBlockNativeStateChanges(t *testing.T) {
	fixture := newPostgresTestDatabase(t)
	db, closePool := openSilentPostgres(t, fixture.dsn)
	t.Cleanup(closePool)
	applyVersionedMigrations(t, db)
	if err := db.Exec(tenantMembersFixtureDDLPostgres).Error; err != nil {
		t.Fatal(err)
	}
	svc := NewServiceWithSources(NewGORMRepository(db), ContractDemoTemplate{}, &fakeSources{})
	actor := Actor{TenantID: 8131, UserID: "academic-source-owner"}
	p := seedActiveImpactProject(t, svc, actor)
	const sourceID, knowledgeID, kbID, assetID, quote = "snapshot-source", "snapshot-knowledge", "snapshot-kb", "snapshot-asset", "bounded synthetic evidence"
	fingerprint := evidence.FingerprintOf(evidence.KnowledgeSignal{KnowledgeID: knowledgeID, ParseStatus: "completed", FileHash: "synthetic-hash", FileSize: 26})
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{"INSERT INTO knowledge_bases(id,tenant_id,name,embedding_model_id,summary_model_id) VALUES(?,?,?,?,?)", []any{kbID, actor.TenantID, "synthetic", "embed", "summary"}},
		{"INSERT INTO knowledges(id,tenant_id,knowledge_base_id,type,title,source,parse_status,file_hash,file_size) VALUES(?,?,?,?,?,?,?,?,?)", []any{knowledgeID, actor.TenantID, kbID, "file", "synthetic", "upload", "completed", "synthetic-hash", 26}},
		{"INSERT INTO chunks(id,tenant_id,knowledge_base_id,knowledge_id,content,chunk_index,start_at,end_at,is_enabled) VALUES(?,?,?,?,?,?,?,?,?)", []any{sourceID, actor.TenantID, kbID, knowledgeID, quote, 0, 0, len(quote), true}},
		{"INSERT INTO lingdoc_project_assets(id,tenant_id,project_id,knowledge_id,knowledge_base_id,asset_revision,processing_state,created_by) VALUES(?,?,?,?,?,?,?,?)", []any{assetID, actor.TenantID, p.ID, knowledgeID, kbID, 1, "ready", actor.UserID}},
		{"INSERT INTO lingdoc_asset_revisions(id,asset_id,revision_no,weknora_knowledge_id,content_hash,status) VALUES(?,?,?,?,?,?)", []any{"snapshot-revision", assetID, 1, knowledgeID, fingerprint.Digest, "active"}},
	} {
		if err := db.Exec(q.sql, q.args...).Error; err != nil {
			t.Fatal(err)
		}
	}
	policy := ProjectAssetPolicy{ID: assetID, ProjectID: p.ID, AssetID: assetID, Version: 1, AssetRevision: 1, PolicyVersion: 1, Scope: "project", ModelUseAllowed: true, CreatedBy: actor.UserID}
	payload, err := json.Marshal(policy)
	if err != nil {
		t.Fatal(err)
	}
	if err = svc.repository.Transaction(context.Background(), TransactionOptions{}, func(tx Transaction) error {
		return tx.(ObjectStore).InsertObject(StoredObject{ProjectID: p.ID, Kind: AssetPolicyKind, ID: assetID, Version: 1, Payload: payload})
	}); err != nil {
		t.Fatal(err)
	}
	source := ResearchSource{SourceID: sourceID, KnowledgeID: knowledgeID, AssetID: assetID, AssetRevision: 1, QuotedTextHash: sha256Hex(quote)}
	tests := []struct {
		name, sql string
		args      []any
	}{
		{"asset", "UPDATE lingdoc_project_assets SET title=title WHERE id=?", []any{assetID}},
		{"chunk", "UPDATE chunks SET content=content WHERE id=?", []any{sourceID}},
		{"knowledge", "UPDATE knowledges SET title=title WHERE id=?", []any{knowledgeID}},
		{"knowledge-base", "UPDATE knowledge_bases SET name=name WHERE id=?", []any{kbID}},
		{"policy", "UPDATE lingdoc_g2_objects SET payload_json=payload_json WHERE project_id=? AND kind=? AND id=?", []any{p.ID, AssetPolicyKind, assetID}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ready, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
			go func() {
				done <- svc.repository.Transaction(context.Background(), TransactionOptions{}, func(tx Transaction) error {
					if e := validateAssociationSourceInTransaction(context.Background(), tx, actor, p, source); e != nil {
						return e
					}
					close(ready)
					<-release
					return nil
				})
			}()
			select {
			case <-ready:
			case e := <-done:
				t.Fatalf("locking validation: %v", e)
			case <-time.After(10 * time.Second):
				t.Fatal("locking validation timed out")
			}
			updated := make(chan error, 1)
			go func() { updated <- db.Exec(tc.sql, tc.args...).Error }()
			select {
			case e := <-updated:
				close(release)
				t.Fatalf("update passed while validation transaction held locks: %v", e)
			case <-time.After(200 * time.Millisecond):
			}
			close(release)
			if e := <-done; e != nil {
				t.Fatal(e)
			}
			select {
			case e := <-updated:
				if e != nil {
					t.Fatal(e)
				}
			case <-time.After(10 * time.Second):
				t.Fatal("update did not resume")
			}
		})
	}
}
