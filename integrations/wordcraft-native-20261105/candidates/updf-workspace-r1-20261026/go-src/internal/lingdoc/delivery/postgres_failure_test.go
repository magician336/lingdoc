package delivery

import (
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
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
	dsn := os.Getenv("LINGDOC_FAILURE_TEST_DSN")
	if dsn == "" {
		dsn = fmt.Sprintf("host=127.0.0.1 port=5432 user=lingdoc password=%s dbname=%s sslmode=disable", os.Getenv("DB_PASSWORD"), name)
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	defer sqlDB.Close()

	// 项目 id 必须每次运行唯一，否则第二次运行撞两处：
	// ① `lingdoc_projects` 的固定主键直接冲突；
	// ② 动作索引 `(project_id, action_actor_id, action_key)` 会命中上一轮记下的
	// 冻结动作，于是 ReplayFreeze 把上一轮的快照当成本轮结果返回——比主键冲突更
	// 隐蔽，因为它报的是「成功」。可重复性必须来自代码，不能来自清库。
	//
	// 就用裸 UUID：迁移里 project_id 是 varchar(36)，加前缀会撑爆列宽。
	projectID := uuid.NewString()
	require.NoError(t, db.Exec(
		"INSERT INTO lingdoc_projects (id,tenant_id,name,status,project_version,spec_revision,template_id,template_version) VALUES (?,1,'Isolated integrity fixture','active',1,1,'template-demo','1')",
		projectID).Error)

	snapshots := NewSQLiteSnapshotStore(db)
	exports := NewSQLiteExportStore(db)
	snapshot := preparedSnapshotForProjectOn(t, snapshots, projectID, time.Date(2026, 9, 24, 0, 0, 0, 0, time.UTC))
	service := NewExportService(snapshots, exports, frozenRenderer(func(DeliveryInput) ([]byte, error) { return []byte("original verified bytes"), nil }), FrozenValidatorFunc(fileValidationNotUnderTest), CurrentnessFunc(currentExportInput), ExportAccessFunc(allowAnyProjectForOwner))
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

// deliveryInputForProject 把共用夹具 `validDeliveryInput` 里的项目 id 换成调用方给的那个。
// 复用同一份输入而不是复制一份，是为了让这条用例仍然被夹具变更波及——复制一份就等于
// 把「夹具与生产输入不同步」这类问题永久藏起来了。
func deliveryInputForProject(projectID string) DeliveryInput {
	input := validDeliveryInput()
	input.ProjectID = projectID
	for i := range input.Sources {
		input.Sources[i].ProjectID = projectID
	}
	return input
}

// preparedSnapshotForProjectOn 是 preparedSnapshotAt 在调用方自带项目 id 时的形状。
func preparedSnapshotForProjectOn(t *testing.T, store SnapshotStore, projectID string, at time.Time) ReleaseSnapshot {
	t.Helper()
	service := NewReleaseService(store)
	service.now = func() time.Time { return at }
	snapshot, err := service.Prepare(deliveryInputForProject(projectID))
	require.NoError(t, err)
	return snapshot
}

// allowAnyProjectForOwner 只校验操作者，把项目作用域交给夹具给的那个唯一 id。
// 用它而不是 allowExport（后者硬编码 project-1）是为了不给「固定主键」留后门：
// 一旦有人把 projectID 换回常量，这里不会拦住，测试又会退回不可重复。
func allowAnyProjectForOwner(actorUserID, _ string) error {
	if actorUserID != "owner" {
		return errors.New("project access denied")
	}
	return nil
}
