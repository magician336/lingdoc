package workspacecore

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

// The same authority boundary used after confirmation must permit PostgreSQL
// row locks for the cited-chapter fallback, and fail closed if citation goes.
func TestPostgresAcademicConfirmationChapterCitationFallback(t *testing.T) {
	fixture := newPostgresTestDatabase(t)
	db, closePool := openSilentPostgres(t, fixture.dsn)
	t.Cleanup(closePool)
	applyVersionedMigrations(t, db)
	if err := db.Exec(tenantMembersFixtureDDLPostgres).Error; err != nil {
		t.Fatal(err)
	}
	svc := NewServiceWithSources(NewGORMRepository(db), ContractDemoTemplate{}, &fakeSources{})
	ctx := context.Background()
	owner := Actor{TenantID: 8345, UserID: "source-owner"}
	project := seedActiveImpactProject(t, svc, owner)
	chapters, err := svc.ListChapters(ctx, owner, project.ID)
	if err != nil || len(chapters) == 0 {
		t.Fatalf("chapter fixture: %v", err)
	}
	chapter := chapters[0]
	reviewer := Actor{TenantID: owner.TenantID, UserID: "chapter-reviewer", Role: types.TenantRoleAdmin}
	seedTenantMember(t, svc, reviewer)
	if err = svc.repository.Transaction(ctx, TransactionOptions{}, func(tx Transaction) error {
		p, e := tx.Project(owner.TenantID, project.ID)
		if e != nil {
			return e
		}
		p.Members = append(p.Members, Member{UserID: reviewer.UserID, Role: "collaborator", GovernanceRole: "member", FunctionRoles: []string{"reviewer", "researcher"}, FunctionScopes: map[string][]string{"reviewer": {chapter.ID}, "researcher": {chapter.ID}}, Status: "active"})
		return tx.ReplaceMembers(project.ID, p.Members)
	}); err != nil {
		t.Fatal(err)
	}
	const knowledgeID, kbID, sourceID, assetID, versionID = "pg-proof-knowledge", "pg-proof-kb", "pg-proof-chunk", "pg-proof-asset", "pg-proof-version"
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{"INSERT INTO knowledge_bases(id,tenant_id,name,embedding_model_id,summary_model_id) VALUES(?,?,?,?,?)", []any{kbID, owner.TenantID, "synthetic", "embed", "summary"}},
		{"INSERT INTO knowledges(id,tenant_id,knowledge_base_id,type,title,source,parse_status,file_hash,file_size) VALUES(?,?,?,?,?,?,?,?,?)", []any{knowledgeID, owner.TenantID, kbID, "file", "synthetic", "upload", "completed", "hash", 4}},
		{"INSERT INTO chunks(id,tenant_id,knowledge_base_id,knowledge_id,content,chunk_index,start_at,end_at,is_enabled) VALUES(?,?,?,?,?,?,?,?,?)", []any{sourceID, owner.TenantID, kbID, knowledgeID, "text", 0, 0, 4, true}},
		{"INSERT INTO lingdoc_project_assets(id,tenant_id,project_id,knowledge_id,knowledge_base_id,asset_revision,processing_state,created_by) VALUES(?,?,?,?,?,?,?,?)", []any{assetID, owner.TenantID, project.ID, knowledgeID, kbID, 1, "ready", owner.UserID}},
	} {
		if err = db.Exec(q.sql, q.args...).Error; err != nil {
			t.Fatal(err)
		}
	}
	version := chapterVersionRow{ID: versionID, ProjectID: project.ID, ChapterID: chapter.ID, BodyMarkdown: "synthetic", SourceIDsJSON: `["pg-proof-chunk"]`, CitationUsagesJSON: `[]`, ReviewItemsJSON: `[]`, SpecRevision: project.SpecRevision}
	if err = db.Create(&version).Error; err != nil {
		t.Fatal(err)
	}
	if err = db.Model(&chapterRow{}).Where("id=? AND project_id=?", chapter.ID, project.ID).Update("current_version_id", versionID).Error; err != nil {
		t.Fatal(err)
	}
	verify := func(tx Transaction, _ Project) error {
		ref, e := NewAssetResourceRefForChapter(assetID, chapter.ID)
		if e != nil {
			return e
		}
		if _, e = svc.authorizeProjectWithoutShadow(ctx, tx, authorizationRequest(reviewer, project.ID, AuthorizationResearch, ref.AuthorizationResource())); !errors.Is(e, ErrNotFound) {
			t.Fatalf("fixture did not exercise chapter fallback: %v", e)
		}
		if _, e = svc.authorizeProject(ctx, tx, authorizationRequest(reviewer, project.ID, AuthorizationEvidenceConfirm, chapter.ID)); e != nil {
			return e
		}
		return svc.authorizeAcademicEvidenceSourceInTransaction(ctx, tx, reviewer, project.ID, chapter.ID, assetID)
	}
	if err = svc.withAcademicConfirmationAuthority(ctx, reviewer, project.ID, []string{chapter.ID}, verify); err != nil {
		t.Fatalf("authorized fallback: %v", err)
	}
	// This is the precise PostgreSQL error from the formerly used
	// readWithCapability transaction; SQLite does not enforce it.
	err = svc.repository.Transaction(ctx, TransactionOptions{ReadOnly: true}, func(tx Transaction) error { return verify(tx, project) })
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "read-only") {
		t.Fatalf("expected read-only row-lock rejection, got %v", err)
	}
	if err = db.Model(&chapterVersionRow{}).Where("id=?", versionID).Update("source_ids_json", `[]`).Error; err != nil {
		t.Fatal(err)
	}
	if err = svc.withAcademicConfirmationAuthority(ctx, reviewer, project.ID, []string{chapter.ID}, verify); !errors.Is(err, ErrSourceUnavailable) {
		t.Fatalf("uncited asset was authorized: %v", err)
	}
}
