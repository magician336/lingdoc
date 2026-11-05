package workspace

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/lingdoc/delivery"
	"github.com/Tencent/WeKnora/internal/lingdoc/rulesetstore"
	tpl "github.com/Tencent/WeKnora/internal/lingdoc/template"
	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func docxFixtureWithAllBodies(t *testing.T, suffix string) []byte {
	t.Helper()
	return replaceDOCXDocument(t, readDOCXFixture(t), func(document string) string {
		tails := map[string]string{
			"topic-summary": "字以内）。", "topic-basis": "应用价值。", "research-content": "目录）",
			"innovation": "特色和创新。", "expected-results": "（略写）", "references": "（略写）",
		}
		occurrences := map[string]int{"expected-results": 2, "references": 3}
		for _, spec := range docxSectionSpecs {
			tail := tails[spec.ID]
			occurrence := occurrences[spec.ID]
			if occurrence == 0 {
				occurrence = 1
			}
			tailAt, searchFrom := -1, 0
			for range occurrence {
				next := strings.Index(document[searchFrom:], tail)
				require.GreaterOrEqual(t, next, 0, spec.ID)
				tailAt = searchFrom + next
				searchFrom = tailAt + len(tail)
			}
			paragraphEnd := strings.Index(document[tailAt:], "</w:p>")
			require.GreaterOrEqual(t, paragraphEnd, 0, spec.ID)
			insertAt := tailAt + paragraphEnd
			body := fmt.Sprintf("<w:r><w:t>冻结合成正文-%s-%s [[source:source-1]]</w:t></w:r>", spec.ID, suffix)
			document = document[:insertAt] + body + document[insertAt:]
		}
		return document
	})
}

func confirmAllDOCXSections(t *testing.T, f candidateOneConnFixture, version DOCXSavedVersion, keyPrefix string) {
	t.Helper()
	for _, section := range version.Projection.Sections {
		request := docxHTTPConfirmationRequest(version, section.SectionID, f.input.ExpectedSpecRevision)
		request.ProjectID = version.ProjectID
		request.IdempotencyKey = keyPrefix + "-" + section.SectionID
		_, replayed, err := f.runtime.ConfirmSavedDOCXChapter(f.ctx, f.actor, request)
		require.NoError(t, err, section.SectionID)
		require.False(t, replayed)
	}
}

func newDOCXDeliveryFixture(t *testing.T) (candidateOneConnFixture, *DOCXDeliveryService) {
	t.Helper()
	f, _ := newDOCXHTTPFixture(t)
	for _, name := range []string{"000024_lingdoc_delivery_stores.up.sql", "000036_lingdoc_rule_sets.up.sql", "000039_lingdoc_ruleset_export_reference.up.sql", "000040_lingdoc_ruleset_use_audits.up.sql"} {
		migration, err := os.ReadFile(filepath.Join("..", "..", "..", "migrations", "sqlite", name))
		require.NoError(t, err)
		require.NoError(t, f.db.Exec(string(migration)).Error)
	}
	var raw string
	require.NoError(t, f.db.Table("lingdoc_project_template_copies").Select("definition_json").Where("id='actual-copy'").Scan(&raw).Error)
	var definition tpl.Template
	require.NoError(t, json.Unmarshal([]byte(raw), &definition))
	require.NotNil(t, definition.RuleSetSnapshot)
	draft := *definition.RuleSetSnapshot
	draft.State, draft.RulesetHash = tpl.RuleSetDraft, ""
	require.NoError(t, f.db.Exec(`CREATE TABLE audit_logs (
		id INTEGER PRIMARY KEY AUTOINCREMENT, tenant_id INTEGER NOT NULL,
		actor_user_id VARCHAR(36) NOT NULL DEFAULT '', actor_role VARCHAR(32) NOT NULL DEFAULT '',
		action VARCHAR(64) NOT NULL, target_type VARCHAR(32) NOT NULL DEFAULT '', target_id VARCHAR(64) NOT NULL DEFAULT '',
		target_user_id VARCHAR(36) NOT NULL DEFAULT '', request_path VARCHAR(512) NOT NULL DEFAULT '', request_method VARCHAR(16) NOT NULL DEFAULT '',
		outcome VARCHAR(16) NOT NULL DEFAULT 'success', details TEXT NOT NULL DEFAULT '{}',
		scope_type VARCHAR(32) NOT NULL DEFAULT '', scope_id VARCHAR(64) NOT NULL DEFAULT '', created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	)`).Error)
	catalog := rulesetstore.NewStore(f.db, rulesetstore.TenantMembershipAuthorizer{})
	catalogActor := rulesetstore.Actor{TenantID: f.actor.TenantID, UserID: f.actor.UserID, Role: string(f.actor.Role)}
	require.NoError(t, catalog.CreateDraft(f.ctx, catalogActor, draft))
	published, err := catalog.Publish(f.ctx, catalogActor, draft.ID, draft.Version)
	require.NoError(t, err)
	require.Equal(t, definition.RuleSetSnapshot.RulesetHash, published.RulesetHash)
	service := NewDOCXDeliveryService(f.runtime, delivery.NewSQLiteSnapshotStore(f.db), delivery.NewSQLiteExportStore(f.db))
	require.NotNil(t, service)
	return f, service
}

// Only the opt-in isolated server uses this persistent composition. Ordinary
// tests retain their temporary fixtures; reopening never runs their seed chain.
func openIsolatedDOCXDelivery(t *testing.T, databasePath string) (candidateOneConnFixture, *DOCXDeliveryService) {
	t.Helper()
	require.True(t, filepath.IsAbs(databasePath), "LINGDOC_SQLITE_PATH must be absolute")
	databasePath = filepath.Clean(databasePath)
	_, err := os.Stat(databasePath)
	if errors.Is(err, os.ErrNotExist) {
		initializeIsolatedDOCXDatabase(t, databasePath)
	} else {
		require.NoError(t, err)
	}
	db, err := gorm.Open(sqlite.Open(databasePath), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	sqlDB.SetMaxOpenConns(1)
	sqlDB.SetMaxIdleConns(1)
	require.NoError(t, db.Exec("PRAGMA busy_timeout = 5000").Error)
	// Validate the existing runtime instead of silently replacing an empty,
	// damaged, or unrelated database with a fresh demo.
	var originPath string
	require.NoError(t, db.Table("knowledges").Select("file_path").Where("id = ?", "knowledge-1").Scan(&originPath).Error)
	require.Equal(t, filepath.Join(filepath.Dir(databasePath), "actual-origin.txt"), originPath)
	_, err = os.ReadFile(originPath)
	require.NoError(t, err, "the persisted source file must survive process restart")
	runtime := NewGORMSourceRuntime(db, nil, candidateOneConnShares{}, nil, delivery.NewFixedTemplateReader())
	projects := core.NewServiceWithSources(core.NewGORMRepository(db), core.ContractDemoTemplate{}, runtime.WorkspaceSourcePolicy())
	runtime.ConnectProjects(projects)
	runtime.EnableFullWorkflowPolicy()
	ctx := types.WithExecutionTenant(policyContext(), 7)
	actor := core.Actor{TenantID: 7, UserID: "reader", Role: types.TenantRoleOwner}
	_, err = projects.GetProject(ctx, actor, "project-1")
	require.NoError(t, err)
	f := candidateOneConnFixture{db: db, runtime: runtime, projects: projects, ctx: ctx, actor: actor}
	return f, NewDOCXDeliveryService(runtime, delivery.NewSQLiteSnapshotStore(db), delivery.NewSQLiteExportStore(db))
}

func initializeIsolatedDOCXDatabase(t *testing.T, databasePath string) {
	t.Helper()
	directory := filepath.Dir(databasePath)
	require.NoError(t, os.MkdirAll(directory, 0700))
	lockPath := databasePath + ".init.lock"
	lock, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	require.NoError(t, err, "another initialization is active; do not overwrite its database")
	defer func() { _ = lock.Close(); _ = os.Remove(lockPath) }()
	if _, err = os.Stat(databasePath); err == nil {
		return
	}
	require.ErrorIs(t, err, os.ErrNotExist)
	// Reuse the known fixture and its complete schema once. VACUUM INTO takes
	// a consistent SQLite copy, including every store, without copying a live
	// database file or duplicating the schema/seed implementation here.
	seed, _ := newDOCXDeliveryFixture(t)
	originPath := filepath.Join(directory, "actual-origin.txt")
	origin, err := os.ReadFile(originPath)
	if errors.Is(err, os.ErrNotExist) {
		require.NoError(t, os.WriteFile(originPath, []byte(sourcePolicyQuote), 0600))
	} else {
		require.NoError(t, err)
		require.Equal(t, sourcePolicyQuote, string(origin), "do not overwrite an existing source")
	}
	require.NoError(t, seed.db.Exec("UPDATE knowledges SET file_path=? WHERE id=?", originPath, "knowledge-1").Error)
	stage, err := os.CreateTemp(directory, ".lingdoc-init-*.db")
	require.NoError(t, err)
	stagePath := stage.Name()
	require.NoError(t, stage.Close())
	defer func() { _ = os.Remove(stagePath) }()
	require.NoError(t, seed.db.Exec("VACUUM INTO ?", stagePath).Error)
	// Publish only a fully initialized database. A failed initialization leaves
	// no partial database that a later process could mistake for a usable one.
	require.NoError(t, os.Rename(stagePath, databasePath))
	sqlDB, err := seed.db.DB()
	require.NoError(t, err)
	require.NoError(t, sqlDB.Close())
}

func TestDOCXFreezeAndExportUseSpecifiedImmutableVersion(t *testing.T) {
	f, service := newDOCXDeliveryFixture(t)
	data := docxFixtureWithAllBodies(t, "v1")
	saved, replayed, err := f.runtime.SaveDOCXVersion(f.ctx, f.actor, "project-1", "delivery-save-v1", 0, data)
	require.NoError(t, err)
	require.False(t, replayed)
	confirmAllDOCXSections(t, f, saved, "delivery-confirm-v1")

	frozen, err := service.FreezeSavedDOCXVersion(f.ctx, f.actor, "project-1", saved.ID, "delivery-freeze-v1")
	require.NoError(t, err)
	require.True(t, frozen.DeliveryEligible)
	require.False(t, frozen.Replayed)
	require.Equal(t, delivery.CheckPassed, frozen.Snapshot.Check.Status)
	require.Len(t, frozen.Snapshot.FrozenInput.Chapters, 6)
	require.Equal(t, saved.ID, frozen.Snapshot.FrozenInput.DOCXSavedVersionID)
	require.Equal(t, saved.DocSHA256, frozen.Snapshot.FrozenInput.DOCXSHA256)
	originalDigest := frozen.Snapshot.SnapshotDigest
	originalConfirmations := make(map[string]string, 6)
	for _, chapter := range frozen.Snapshot.FrozenInput.Chapters {
		require.NotNil(t, chapter.Confirmation)
		originalConfirmations[chapter.ChapterID] = chapter.Confirmation.ID
	}

	nextData := docxFixtureWithAllBodies(t, "v2")
	next, replayed, err := f.runtime.SaveDOCXVersion(f.ctx, f.actor, "project-1", "delivery-save-v2", 1, nextData)
	require.NoError(t, err)
	require.False(t, replayed)
	require.NotEqual(t, saved.ID, next.ID)

	replay, err := service.FreezeSavedDOCXVersion(f.ctx, f.actor, "project-1", saved.ID, "delivery-freeze-v1")
	require.NoError(t, err)
	require.True(t, replay.Replayed)
	require.Equal(t, frozen.Snapshot.ID, replay.Snapshot.ID)

	historical, err := service.FreezeSavedDOCXVersion(f.ctx, f.actor, "project-1", saved.ID, "delivery-freeze-historical")
	require.NoError(t, err, "a non-latest immutable DOCX version must not be rejected as stale")
	require.NotEqual(t, frozen.Snapshot.ID, historical.Snapshot.ID)
	require.False(t, historical.DeliveryEligible, "the old invalidated confirmation must remain a blocked historical result")

	artifact, replayed, err := service.StartExport(f.ctx, f.actor, "project-1", frozen.Snapshot.ID, "delivery-export-v1")
	require.NoError(t, err)
	require.False(t, replayed)
	require.Equal(t, delivery.ExportVerified, artifact.Status)
	require.Equal(t, saved.DocSHA256, artifact.FileSHA256)
	_, downloaded, err := service.Download(f.ctx, f.actor, "project-1", artifact.ID)
	require.NoError(t, err)
	require.Equal(t, data, downloaded)
	require.NotEqual(t, nextData, downloaded)

	confirmAllDOCXSections(t, f, next, "delivery-confirm-v2")
	stored, err := delivery.NewSQLiteSnapshotStore(f.db).Get("project-1", frozen.Snapshot.ID)
	require.NoError(t, err)
	require.Equal(t, originalDigest, stored.SnapshotDigest)
	for _, chapter := range stored.FrozenInput.Chapters {
		require.NotNil(t, chapter.Confirmation)
		require.Equal(t, originalConfirmations[chapter.ChapterID], chapter.Confirmation.ID, "later confirmations must not rewrite old snapshots")
	}

	require.NoError(t, f.db.Model(&docxSavedVersionRow{}).Where("id=?", saved.ID).Update("doc_bytes", []byte("tampered")).Error)
	_, _, err = service.StartExport(f.ctx, f.actor, "project-1", frozen.Snapshot.ID, "delivery-export-tampered")
	require.ErrorContains(t, err, "INTEGRITY")
}

func TestDOCXFreezePersistsBlockedSnapshotAndExportRefusesIt(t *testing.T) {
	f, service := newDOCXDeliveryFixture(t)
	saved, _, err := f.runtime.SaveDOCXVersion(f.ctx, f.actor, "project-1", "blocked-save-v1", 0, readDOCXFixture(t))
	require.NoError(t, err)
	frozen, err := service.FreezeSavedDOCXVersion(f.ctx, f.actor, "project-1", saved.ID, "blocked-freeze-v1")
	require.NoError(t, err)
	require.False(t, frozen.DeliveryEligible)
	require.Equal(t, delivery.CheckBlocked, frozen.Snapshot.Check.Status)
	require.NotEmpty(t, frozen.Snapshot.Check.Issues)
	_, _, err = service.StartExport(f.ctx, f.actor, "project-1", frozen.Snapshot.ID, "blocked-export-v1")
	require.True(t, errors.Is(err, delivery.ErrExportPreflightBlocked), err)

	var snapshots int64
	require.NoError(t, f.db.Table("lingdoc_release_snapshots").Where("project_id=?", "project-1").Count(&snapshots).Error)
	require.EqualValues(t, 1, snapshots, "the blocked diagnostic is a real persisted record")
}

func TestDOCXFreezeFailuresDoNotCreateSnapshots(t *testing.T) {
	f, service := newDOCXDeliveryFixture(t)
	_, err := service.FreezeSavedDOCXVersion(f.ctx, f.actor, "project-1", "missing-version", "invalid-freeze-v1")
	require.Error(t, err)
	var count int64
	require.NoError(t, f.db.Table("lingdoc_release_snapshots").Where("project_id=?", "project-1").Count(&count).Error)
	require.Zero(t, count)

	saved, _, err := f.runtime.SaveDOCXVersion(f.ctx, f.actor, "project-1", "denied-save-v1", 0, readDOCXFixture(t))
	require.NoError(t, err)
	require.NoError(t, f.db.Exec("UPDATE tenant_members SET status='removed' WHERE user_id=?", f.actor.UserID).Error)
	_, err = service.FreezeSavedDOCXVersion(f.ctx, f.actor, "project-1", saved.ID, "denied-freeze-v1")
	require.Error(t, err)
	require.NoError(t, f.db.Table("lingdoc_release_snapshots").Where("project_id=?", "project-1").Count(&count).Error)
	require.Zero(t, count)
}

func TestDOCXDownloadRechecksSourceAccess(t *testing.T) {
	f, service := newDOCXDeliveryFixture(t)
	saved, _, err := f.runtime.SaveDOCXVersion(f.ctx, f.actor, "project-1", "download-source-save", 0, docxFixtureWithAllBodies(t, "source"))
	require.NoError(t, err)
	confirmAllDOCXSections(t, f, saved, "download-source-confirm")
	frozen, err := service.FreezeSavedDOCXVersion(f.ctx, f.actor, "project-1", saved.ID, "download-source-freeze")
	require.NoError(t, err)
	require.True(t, frozen.DeliveryEligible)
	artifact, _, err := service.StartExport(f.ctx, f.actor, "project-1", frozen.Snapshot.ID, "download-source-export")
	require.NoError(t, err)

	require.NoError(t, f.db.Exec("UPDATE chunks SET content_revision=1 WHERE id='source-1'").Error)
	_, downloaded, err := service.Download(f.ctx, f.actor, "project-1", artifact.ID)
	require.ErrorIs(t, err, delivery.ErrExportStaleInput)
	require.Nil(t, downloaded)
}

func TestDOCXFreezeSameKeyRejectsAnotherSavedVersion(t *testing.T) {
	f, service := newDOCXDeliveryFixture(t)
	first, _, err := f.runtime.SaveDOCXVersion(f.ctx, f.actor, "project-1", "conflict-save-v1", 0, docxFixtureWithAllBodies(t, "first"))
	require.NoError(t, err)
	confirmAllDOCXSections(t, f, first, "conflict-confirm-v1")
	frozen, err := service.FreezeSavedDOCXVersion(f.ctx, f.actor, "project-1", first.ID, "conflict-freeze-key")
	require.NoError(t, err)
	require.True(t, frozen.DeliveryEligible)

	second, _, err := f.runtime.SaveDOCXVersion(f.ctx, f.actor, "project-1", "conflict-save-v2", 1, docxFixtureWithAllBodies(t, "second"))
	require.NoError(t, err)
	confirmAllDOCXSections(t, f, second, "conflict-confirm-v2")
	_, err = service.FreezeSavedDOCXVersion(f.ctx, f.actor, "project-1", second.ID, "conflict-freeze-key")
	require.ErrorIs(t, err, delivery.ErrIdempotencyConflict)
	var count int64
	require.NoError(t, f.db.Table("lingdoc_release_snapshots").Where("project_id=?", "project-1").Count(&count).Error)
	require.EqualValues(t, 1, count)
}

func TestDOCXConcurrentSaveAndFreezeHaveOneWinner(t *testing.T) {
	f, service := newDOCXDeliveryFixture(t)
	data := docxFixtureWithAllBodies(t, "concurrent")
	type saveResult struct {
		version DOCXSavedVersion
		err     error
	}
	saveStart := make(chan struct{})
	saveResults := make(chan saveResult, 2)
	for i := 0; i < 2; i++ {
		go func(i int) {
			<-saveStart
			version, _, err := f.runtime.SaveDOCXVersion(f.ctx, f.actor, "project-1", fmt.Sprintf("concurrent-save-%d", i), 0, data)
			saveResults <- saveResult{version: version, err: err}
		}(i)
	}
	close(saveStart)
	var saved DOCXSavedVersion
	var successes, conflicts int
	for i := 0; i < 2; i++ {
		result := <-saveResults
		if result.err == nil {
			successes++
			saved = result.version
		} else {
			conflicts++
			require.ErrorContains(t, result.err, "VERSION_CHANGED")
		}
	}
	require.Equal(t, 1, successes)
	require.Equal(t, 1, conflicts)
	confirmAllDOCXSections(t, f, saved, "concurrent-confirm")

	type freezeResult struct {
		result DOCXFreezeResult
		err    error
	}
	freezeStart := make(chan struct{})
	freezeResults := make(chan freezeResult, 2)
	for i := 0; i < 2; i++ {
		go func() {
			<-freezeStart
			result, err := service.FreezeSavedDOCXVersion(f.ctx, f.actor, "project-1", saved.ID, "concurrent-freeze-key")
			freezeResults <- freezeResult{result: result, err: err}
		}()
	}
	close(freezeStart)
	var snapshotID string
	var replayed int
	for i := 0; i < 2; i++ {
		result := <-freezeResults
		require.NoError(t, result.err)
		require.True(t, result.result.DeliveryEligible)
		if snapshotID == "" {
			snapshotID = result.result.Snapshot.ID
		}
		require.Equal(t, snapshotID, result.result.Snapshot.ID)
		if result.result.Replayed {
			replayed++
		}
	}
	require.Equal(t, 1, replayed)
	var count int64
	require.NoError(t, f.db.Table("lingdoc_release_snapshots").Where("project_id=?", "project-1").Count(&count).Error)
	require.EqualValues(t, 1, count)
}
