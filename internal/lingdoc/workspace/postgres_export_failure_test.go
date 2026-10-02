package workspace

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/lingdoc/delivery"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// The renderer fault is controlled; the DOCX validator, export orchestration and
// PostgreSQL stores are production implementations. No provider call is made.
type missingWarningRenderer struct{}

func (missingWarningRenderer) RenderFrozen(input delivery.DeliveryInput) ([]byte, error) {
	file, err := (DeliveryDocument{}).RenderFrozen(input)
	if err != nil {
		return nil, err
	}
	reader, err := zip.NewReader(bytes.NewReader(file), int64(len(file)))
	if err != nil {
		return nil, err
	}
	var output bytes.Buffer
	writer := zip.NewWriter(&output)
	for _, member := range reader.File {
		stream, err := member.Open()
		if err != nil {
			return nil, err
		}
		content, readErr := io.ReadAll(stream)
		closeErr := stream.Close()
		if readErr != nil {
			return nil, readErr
		}
		if closeErr != nil {
			return nil, closeErr
		}
		if member.Name == "word/document.xml" {
			content = bytes.ReplaceAll(content, []byte("不能证明真实研究结论"), []byte("错误地丢失待核提示"))
		}
		part, err := writer.Create(member.Name)
		if err != nil {
			return nil, err
		}
		if _, err := part.Write(content); err != nil {
			return nil, err
		}
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}

func TestPostgresRendererFailureUsesRealDOCXValidator(t *testing.T) {
	name := os.Getenv("LINGDOC_FAILURE_TEST_DB")
	if name == "" {
		t.Skip("requires explicitly isolated PostgreSQL failure-test database")
	}
	require.True(t, strings.HasPrefix(name, "lingdoc_fault_"))
	dsn := fmt.Sprintf("host=127.0.0.1 port=5432 user=lingdoc password=%s dbname=%s sslmode=disable", os.Getenv("DB_PASSWORD"), name)
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	defer sqlDB.Close()
	input := renderableInput()
	input.ProjectID = "project-renderer-fault"
	require.NoError(t, db.Exec("INSERT INTO lingdoc_projects (id,tenant_id,name,status,project_version,spec_revision,template_id,template_version) VALUES (?,1,'Isolated renderer fixture','active',1,1,'template-demo','1')", input.ProjectID).Error)
	snapshots := delivery.NewSQLiteSnapshotStore(db)
	exports := delivery.NewSQLiteExportStore(db)
	snapshot := delivery.ReleaseSnapshot{ID: "snapshot-renderer-fault", ProjectID: input.ProjectID, FrozenInput: input,
		Check: delivery.CheckResult{Status: delivery.CheckPassed}, IsCurrent: true, CreatedAt: time.Now().UTC()}
	require.NoError(t, snapshots.Save(snapshot))
	currentness := delivery.CurrentnessFunc(func(delivery.DeliveryInput) (bool, error) { return true, nil })
	access := delivery.ExportAccessFunc(func(actor, project string) error {
		if actor != "fault-owner" || project != input.ProjectID {
			return fmt.Errorf("fixture access denied")
		}
		return nil
	})
	service := delivery.NewExportService(snapshots, exports, missingWarningRenderer{}, DeliveryDocument{}, currentness, access)
	artifact, replayed, err := service.Start("fault-owner", input.ProjectID, snapshot.ID, "renderer-failure-action")
	require.NoError(t, err)
	require.False(t, replayed)
	require.Equal(t, delivery.ExportFailed, artifact.Status)
	require.Equal(t, delivery.FailureValidationFailed, artifact.FailureCode)
	require.Empty(t, artifact.FileSHA256)
	_, file, err := service.Download("fault-owner", input.ProjectID, artifact.ID)
	require.ErrorIs(t, err, delivery.ErrExportUnavailable)
	require.Empty(t, file)
	var stored struct{ FileBlob []byte }
	require.NoError(t, db.Table("lingdoc_export_artifacts").Select("file_blob").Where("id = ?", artifact.ID).First(&stored).Error)
	require.Empty(t, stored.FileBlob)
	// A new store/service instance must read and replay the same failed result.
	reopened := delivery.NewExportService(delivery.NewSQLiteSnapshotStore(db), delivery.NewSQLiteExportStore(db), DeliveryDocument{}, DeliveryDocument{}, currentness, access)
	same, replayed, err := reopened.Start("fault-owner", input.ProjectID, snapshot.ID, "renderer-failure-action")
	require.NoError(t, err)
	require.True(t, replayed)
	require.Equal(t, artifact.ID, same.ID)
	require.Equal(t, delivery.ExportFailed, same.Status)
	// Recovery is a new action with the unmodified renderer and real validator.
	recovered, replayed, err := reopened.Start("fault-owner", input.ProjectID, snapshot.ID, "renderer-recovery-action")
	require.NoError(t, err)
	require.False(t, replayed)
	require.Equal(t, delivery.ExportVerified, recovered.Status)
	_, file, err = reopened.Download("fault-owner", input.ProjectID, recovered.ID)
	require.NoError(t, err)
	require.NoError(t, (DeliveryDocument{}).ValidateFrozen(input, file))
}
