package workspace

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/application/service"
	"github.com/Tencent/WeKnora/internal/evidence"
	"github.com/Tencent/WeKnora/internal/lingdoc/delivery"
	"github.com/Tencent/WeKnora/internal/types"
	"gorm.io/gorm"
)

// This integration test keeps the real KB-share service and the production
// NewHandler SourcePolicy composition in place. Only the surrounding SQL
// schema and project-membership fixture are reduced to the columns exercised
// here; the source permission itself is granted and revoked through the app
// service API.
func TestCheckPassedSnapshotCannotDownloadAfterRealKBShareRevocation(t *testing.T) {
	_, db := newSourceCurrentnessHandler(t)
	seedRealSharePermissionTables(t, db)
	if err := db.Exec(
		"INSERT INTO knowledge_bases (id, tenant_id) VALUES (?, ?)", "shared-kb", 8,
	).Error; err != nil {
		t.Fatalf("seed shared knowledge base: %v", err)
	}
	if err := db.Exec(
		"INSERT INTO organizations (id, name, owner_id, owner_tenant_id) VALUES (?, ?, ?, ?)", "synthetic-org", "synthetic org", "kb-owner", 8,
	).Error; err != nil {
		t.Fatalf("seed organization: %v", err)
	}
	if err := db.Exec(
		"INSERT INTO organization_tenant_members (id, organization_id, tenant_id, role) VALUES (?, ?, ?, ?), (?, ?, ?, ?)",
		"org-owner", "synthetic-org", 8, string(types.OrgRoleEditor),
		"org-reader", "synthetic-org", 7, string(types.OrgRoleViewer),
	).Error; err != nil {
		t.Fatalf("seed organization tenant members: %v", err)
	}

	kbShares := service.NewKBShareService(
		repository.NewKBShareRepository(db),
		repository.NewOrganizationRepository(db),
		repository.NewKnowledgeBaseRepository(db),
		nil, nil, nil,
	)
	ownerCtx := types.WithCaller(context.Background(), types.Caller{
		TenantID: 8,
		UserID:   "kb-owner",
		Role:     types.TenantRoleAdmin,
	})
	share, err := kbShares.ShareKnowledgeBase(ownerCtx, "shared-kb", "synthetic-org", "kb-owner", 8, types.OrgRoleViewer)
	if err != nil {
		t.Fatalf("grant cross-tenant KB read through application service: %v", err)
	}

	processedAt := time.Date(2026, time.September, 26, 10, 0, 0, 0, time.UTC)
	if err := db.Exec(
		"INSERT INTO knowledges (id, tenant_id, knowledge_base_id, type, title, file_type, file_size, file_hash, file_path, parse_status, processed_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
		"shared-knowledge", 8, "shared-kb", "file", "synthetic shared PDF", "pdf",
		len([]rune(sourcePolicyQuote)), "shared-hash", "", types.ParseStatusCompleted, processedAt,
	).Error; err != nil {
		t.Fatalf("seed shared knowledge row: %v", err)
	}
	if err := db.Exec(
		"INSERT INTO chunks (id, tenant_id, knowledge_id, knowledge_base_id, content, content_revision, chunk_index, start_at, end_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)",
		deliverySourceID, 8, "shared-knowledge", "shared-kb", sourcePolicyQuote, 0, 0, 0, len([]rune(sourcePolicyQuote)),
	).Error; err != nil {
		t.Fatalf("seed shared source chunk: %v", err)
	}

	// NewHandler builds the actual FixedAuthorizer -> kbReadChecker -> KB-share
	// permission chain. Do not replace this gateway with a switchable test authorizer.
	handler := NewHandler(db, kbShares, nil)
	asset, err := handler.bindings.Bind(context.Background(), evidence.BindInput{
		TenantID:        8,
		ProjectID:       "project-1",
		KnowledgeID:     "shared-knowledge",
		KnowledgeBaseID: "shared-kb",
		Title:           "synthetic shared PDF",
		CreatedBy:       "kb-owner",
		Signal: evidence.KnowledgeSignal{
			KnowledgeID: "shared-knowledge",
			ParseStatus: types.ParseStatusCompleted,
			FileHash:    "shared-hash",
			FileSize:    int64(len([]rune(sourcePolicyQuote))),
			ProcessedAt: processedAt,
		},
	})
	if err != nil {
		t.Fatalf("bind cross-tenant shared source: %v", err)
	}
	if err := handler.WorkspaceSourcePolicy().Validate(policyContext(), "project-1", deliveryActorID, []string{deliverySourceID}); err != nil {
		t.Fatalf("production-composed source policy rejected an active share: %v", err)
	}

	chapters := []deliveryChapter{
		deliveryQuestionChapter(asset.ID, asset.AssetRevision, deliveryBody, []string{deliverySourceID}),
		deliveryMethodChapter(),
	}
	seedDeliveryWorkspace(t, db, chapters...)
	router := assembleExportRoutes(t, handler, db, deliveryTestAuthorizer{})
	snapshot := freezeRelease(t, router, deliveryFreezeKey)
	if snapshot.Check.Status != delivery.CheckPassed {
		t.Fatalf("fixture snapshot status = %s, want CheckPassed", snapshot.Check.Status)
	}
	artifact := startExportOf(t, router, snapshot.ID)
	if artifact.Status != string(delivery.ExportVerified) || artifact.DownloadPath == nil {
		t.Fatalf("pre-revocation export = %+v, want a verified downloadable file", artifact)
	}

	if err := kbShares.RemoveShare(ownerCtx, share.ID, "kb-owner", 8); err != nil {
		t.Fatalf("revoke KB share through application service: %v", err)
	}
	if err := handler.WorkspaceSourcePolicy().Validate(policyContext(), "project-1", deliveryActorID, []string{deliverySourceID}); err == nil {
		t.Fatal("production-composed SourcePolicy still permits the source after share revocation")
	}

	// A second export proves CheckPassed does not short-circuit current source
	// authorization. The first export proves a frozen file cannot be downloaded
	// later through its old URL after the same permission is revoked.
	_, envelope, status := startExport(t, router, snapshot.ID, "export-after-share-revoked-2")
	if status != http.StatusForbidden {
		t.Fatalf("startExport on a CheckPassed snapshot after revocation = %d, want 403: %s", status, envelope.Data)
	}
	if envelope.Error == nil || envelope.Error.Code != "source_access_denied" {
		t.Fatalf("startExport after revocation error = %+v, want source_access_denied", envelope.Error)
	}

	download := deliveryServe(router, deliveryRequest(http.MethodGet, *artifact.DownloadPath, "", ""))
	if download.Code != http.StatusForbidden {
		t.Fatalf("download old verified export after share revocation = %d, want 403: %s", download.Code, download.Body.String())
	}
	if code := decodeDeliveryEnvelope(t, download).Error; code == nil || code.Code != "source_access_denied" {
		t.Fatalf("download after revocation error = %+v, want source_access_denied", code)
	}
}

func seedRealSharePermissionTables(t *testing.T, db *gorm.DB) {
	t.Helper()
	statements := []string{
		`CREATE TABLE knowledge_bases (
			id TEXT PRIMARY KEY,
			tenant_id INTEGER NOT NULL,
			deleted_at DATETIME
		)`,
		`CREATE TABLE organizations (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			owner_id TEXT NOT NULL,
			owner_tenant_id INTEGER NOT NULL,
			deleted_at DATETIME
		)`,
		`CREATE TABLE organization_tenant_members (
			id TEXT PRIMARY KEY,
			organization_id TEXT NOT NULL,
			tenant_id INTEGER NOT NULL,
			role TEXT NOT NULL,
			joined_at DATETIME,
			created_at DATETIME,
			updated_at DATETIME
		)`,
		`CREATE TABLE kb_shares (
			id TEXT PRIMARY KEY,
			knowledge_base_id TEXT NOT NULL,
			organization_id TEXT NOT NULL,
			shared_by_user_id TEXT NOT NULL,
			source_tenant_id INTEGER NOT NULL,
			permission TEXT NOT NULL,
			created_at DATETIME,
			updated_at DATETIME,
			deleted_at DATETIME
		)`,
	}
	for _, statement := range statements {
		if err := db.Exec(statement).Error; err != nil {
			t.Fatalf("create real share-service fixture schema: %v", err)
		}
	}
}
