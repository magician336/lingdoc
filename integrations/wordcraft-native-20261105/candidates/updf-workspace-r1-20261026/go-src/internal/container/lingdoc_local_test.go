package container

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/application/service"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestLocalResearchBoundaryAcceptsLoopbackAliasPair(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, configuredOrigin := range []string{"http://127.0.0.1:7104", "http://localhost:7104"} {
		for _, incomingOrigin := range []string{"http://127.0.0.1:7104", "http://localhost:7104"} {
			t.Run(configuredOrigin+" accepts "+incomingOrigin, func(t *testing.T) {
				router := gin.New()
				router.Use(localResearchBoundary("127.0.0.1:7199", configuredOrigin))
				router.POST("/write", func(c *gin.Context) { c.Status(http.StatusNoContent) })
				req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:7199/write", nil)
				req.RemoteAddr = "127.0.0.1:1234"
				req.Header.Set("Origin", incomingOrigin)
				response := httptest.NewRecorder()
				router.ServeHTTP(response, req)
				require.Equal(t, http.StatusNoContent, response.Code)
				require.Equal(t, incomingOrigin, response.Header().Get("Access-Control-Allow-Origin"))
			})
		}
	}
}

func TestLocalResearchBoundaryRejectsForeignOriginsAndHosts(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(localResearchBoundary("127.0.0.1:7199", "http://127.0.0.1:7104"))
	router.POST("/write", func(c *gin.Context) { c.Status(http.StatusNoContent) })
	for _, test := range []struct {
		host, origin, remote string
		status               int
	}{
		{"127.0.0.1:7199", "http://127.0.0.1:7104", "127.0.0.1:1234", http.StatusNoContent},
		{"127.0.0.1:7199", "https://foreign.example", "127.0.0.1:1234", http.StatusForbidden},
		{"127.0.0.1:7199", "http://localhost:7105", "127.0.0.1:1234", http.StatusForbidden},
		{"127.0.0.1:7199", "http://127.0.0.1:7105", "127.0.0.1:1234", http.StatusForbidden},
		{"127.0.0.1:7199", "http://localhost.evil:7104", "127.0.0.1:1234", http.StatusForbidden},
		{"127.0.0.1:7199", "http://127.0.0.1.evil:7104", "127.0.0.1:1234", http.StatusForbidden},
		{"rebind.example:7199", "", "127.0.0.1:1234", http.StatusForbidden},
		{"127.0.0.1:7199", "", "10.0.0.2:1234", http.StatusForbidden},
	} {
		req := httptest.NewRequest(http.MethodPost, "http://"+test.host+"/write", nil)
		req.RemoteAddr = test.remote
		req.Header.Set("Origin", test.origin)
		response := httptest.NewRecorder()
		router.ServeHTTP(response, req)
		require.Equal(t, test.status, response.Code)
	}
}

func TestLocalResearchContextSupportsBothHTTPIdentityReaders(t *testing.T) {
	ctx := localResearchContext(context.Background(), &types.User{ID: "local-owner"}, &types.Tenant{ID: 7})
	caller := types.CallerFromContext(ctx)
	require.Equal(t, types.TenantRoleOwner, caller.Role)
	require.Equal(t, caller.Role, types.TenantRoleFromContext(ctx))
	userID, ok := types.UserIDFromContext(ctx)
	require.True(t, ok)
	require.Equal(t, caller.UserID, userID)
	tenantID, ok := types.TenantIDFromContext(ctx)
	require.True(t, ok)
	require.Equal(t, caller.TenantID, tenantID)
}

func TestDatabaseOriginFilesReadResourceInsideSingleConnectionTransaction(t *testing.T) {
	t.Setenv("STORAGE_TYPE", "local")
	t.Setenv("LOCAL_STORAGE_BASE_DIR", t.TempDir())
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { sqlDB.Close() })
	require.NoError(t, db.AutoMigrate(&types.Tenant{}, &types.StorageBackend{}, &types.StoredResource{}, &types.ResourceBinding{}, &types.KnowledgeBase{}, &types.Knowledge{}))
	ctx := context.Background()
	backendRepo := repository.NewStorageBackendRepository(db)
	tenant, err := service.NewTenantService(repository.NewTenantRepository(db), backendRepo).CreateTenant(ctx, &types.Tenant{Name: "local origin test"})
	require.NoError(t, err)
	catalog := service.NewResourceCatalog(repository.NewResourceRepository(db))
	resolver := service.NewStorageBackendService(backendRepo, db, catalog)
	files, _, err := resolver.ResolveFileService(ctx, tenant, "", "", os.Getenv("LOCAL_STORAGE_BASE_DIR"))
	require.NoError(t, err)
	const original = "可逐字核对的原始材料。"
	ref, err := files.SaveBytes(ctx, []byte(original), tenant.ID, "source.txt", false)
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(ref, "resource://"))
	kb := types.KnowledgeBase{ID: "kb", TenantID: tenant.ID, Name: "sources", StorageBackendID: tenant.DefaultStorageBackendID}
	require.NoError(t, db.Create(&kb).Error)
	require.NoError(t, db.Create(&types.Knowledge{ID: "source", TenantID: tenant.ID, KnowledgeBaseID: "kb", FilePath: ref, FileName: "source.txt", FileType: "txt"}).Error)
	bounded, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	require.NoError(t, db.WithContext(bounded).Transaction(func(tx *gorm.DB) error {
		bytes, available, err := databaseOriginFiles(tx).OriginBytes(bounded, "source")
		require.NoError(t, err)
		require.True(t, available)
		require.Equal(t, original, string(bytes))
		return nil
	}))
}
