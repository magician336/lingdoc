package delivery

import (
	"fmt"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestPostgresDownloadRejectsPersistedCorruption(t *testing.T) {
	name := os.Getenv("LINGDOC_FAILURE_TEST_DB")
	if name == "" {
		t.Skip("requires explicitly isolated PostgreSQL failure-test database")
	}
	require.Contains(t, name, "lingdoc_fault_")
	dsn := fmt.Sprintf("host=127.0.0.1 port=5432 user=lingdoc password=%s dbname=%s sslmode=disable", os.Getenv("DB_PASSWORD"), name)
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	defer sqlDB.Close()
	// Fixture data in a separate database, retaining the real migration's FKs.
	require.NoError(t, db.Exec("INSERT INTO lingdoc_projects (id,tenant_id,name,status,project_version,spec_revision,template_id,template_version) VALUES ('project-1',1,'Isolated integrity fixture','active',1,1,'template-demo','1')").Error)
	snapshots := NewSQLiteSnapshotStore(db)
	exports := NewSQLiteExportStore(db)
	snapshot := preparedSnapshotOn(t, snapshots)
	service := NewExportService(snapshots, exports, frozenRenderer(func(DeliveryInput) ([]byte, error) { return []byte("original verified bytes"), nil }), FrozenValidatorFunc(fileValidationNotUnderTest), CurrentnessFunc(currentExportInput), ExportAccessFunc(allowExport))
	artifact, _, err := service.Start("owner", snapshot.ProjectID, snapshot.ID, exportActionKey)
	require.NoError(t, err)
	stored, err := exports.GetExport(snapshot.ProjectID, artifact.ID)
	require.NoError(t, err)
	original := append([]byte(nil), stored.file...)
	stored.file[0] ^= 1
	require.NoError(t, exports.SaveExport(stored))
	_, file, err := service.Download("owner", snapshot.ProjectID, artifact.ID)
	require.ErrorIs(t, err, ErrExportUnavailable)
	require.Empty(t, file)
	stored.file = original
	require.NoError(t, exports.SaveExport(stored))
	_, file, err = service.Download("owner", snapshot.ProjectID, artifact.ID)
	require.NoError(t, err)
	require.Equal(t, original, file)
	// Close the connection and read the restored bytes with a new connection.
	require.NoError(t, sqlDB.Close())
	reopened, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	next, err := reopened.DB()
	require.NoError(t, err)
	defer next.Close()
	service.exports = NewSQLiteExportStore(reopened)
	_, file, err = service.Download("owner", snapshot.ProjectID, artifact.ID)
	require.NoError(t, err)
	require.Equal(t, original, file)
}
