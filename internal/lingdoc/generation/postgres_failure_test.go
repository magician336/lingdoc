package generation

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/lingdoc/candidateadoption"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type faultModelFunc func(context.Context, Input) (Draft, error)

func (f faultModelFunc) Generate(ctx context.Context, input Input) (Draft, error) {
	return f(ctx, input)
}

type faultCurrentness struct{ current atomic.Bool }

func (c *faultCurrentness) GenerationInputIsCurrent(context.Context, Actor, string, string, candidateadoption.Basis) (bool, error) {
	return c.current.Load(), nil
}

// Uses the production GORM repository against an explicitly isolated PostgreSQL
// database. Models and workspace adapters are controlled doubles, not providers.
func TestPostgresGenerationFailureBoundaries(t *testing.T) {
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
	repo := NewSQLiteRepository(db)
	// Tables must have been created by the real versioned PostgreSQL migrations.
	candidates := candidateadoption.NewSQLiteCandidateAdoptionStore(db)

	for _, scenario := range []string{"changed-during-model", "deadline", "cancelled"} {
		t.Run(scenario, func(t *testing.T) {
			svc, fixture := generationFixture()
			svc.Repository = repo
			input := fixture.input
			input.Request.ChapterID = "11111111-1111-4111-8111-111111111112"
			input.Workspace.ChapterID = input.Request.ChapterID
			input.Workspace.ProjectID = "11111111-1111-4111-8111-111111111111"
			input.Sources[0].ProjectID = input.Workspace.ProjectID
			hash := fmt.Sprintf("%x", sha256.Sum256([]byte(scenario)))
			run, err := repo.CreateOrReplay(context.Background(), input.Actor, input.Workspace.ProjectID, scenario, hash, input)
			require.NoError(t, err)
			current := &faultCurrentness{}
			current.current.Store(true)
			svc.Currentness = current
			entered := make(chan struct{})
			release := make(chan struct{})
			svc.Model = faultModelFunc(func(ctx context.Context, _ Input) (Draft, error) {
				close(entered)
				if scenario == "changed-during-model" {
					select {
					case <-release:
						return Draft{BodyMarkdown: "Text [[source:source-1]]", Sources: []Source{{ID: "source-1"}}}, nil
					case <-ctx.Done():
						return Draft{}, ctx.Err()
					}
				}
				<-ctx.Done()
				return Draft{}, ctx.Err()
			})
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			if scenario == "deadline" {
				var timeoutCancel context.CancelFunc
				ctx, timeoutCancel = context.WithTimeout(ctx, 100*time.Millisecond)
				defer timeoutCancel()
			}
			type outcome struct {
				run Run
				err error
			}
			done := make(chan outcome, 1)
			go func() { r, e := svc.Execute(ctx, run.ID); done <- outcome{r, e} }()
			select {
			case <-entered:
			case <-ctx.Done():
				t.Fatal("worker did not enter controlled model")
			}
			switch scenario {
			case "changed-during-model":
				current.current.Store(false)
				close(release)
			case "cancelled":
				cancel()
			}
			var result outcome
			select {
			case result = <-done:
			case <-time.After(3 * time.Second):
				t.Fatal("worker did not terminate")
			}
			require.NoError(t, result.err)
			require.Equal(t, StatusInterrupted, result.run.Status)
			var stored generationRow
			require.NoError(t, db.Where("id = ?", run.ID).First(&stored).Error)
			if scenario == "changed-during-model" {
				require.Equal(t, "stale_input", stored.ErrorCode)
			} else {
				require.Equal(t, "generation_interrupted", stored.ErrorCode)
			}
			require.Nil(t, stored.CandidateID)
			listed, err := candidates.ListCandidates(context.Background(), input.Workspace.ProjectID, input.Request.ChapterID)
			require.NoError(t, err)
			require.Empty(t, listed)
			// A new repository instance reads the terminal state from PostgreSQL.
			replay, found, err := NewSQLiteRepository(db).FindReplay(context.Background(), input.Actor, input.Workspace.ProjectID, scenario, hash)
			require.NoError(t, err)
			require.True(t, found)
			require.Equal(t, StatusInterrupted, replay.Status)
		})
	}
}

// This kills a real worker process. Lease time is advanced only in the isolated
// fixture database; it is not a provider failure or a five-minute wall-clock run.
func TestPostgresWorkerCrashLeasePersistence(t *testing.T) {
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
	repo := NewSQLiteRepository(db)
	svc, fixture := generationFixture()
	svc.Repository = repo
	if runID := os.Getenv("LINGDOC_FAILURE_CRASH_RUN"); runID != "" {
		svc.Model = faultModelFunc(func(ctx context.Context, _ Input) (Draft, error) {
			if err := os.WriteFile(os.Getenv("LINGDOC_FAILURE_CRASH_READY"), []byte("entered"), 0600); err != nil {
				return Draft{}, err
			}
			<-ctx.Done()
			return Draft{}, ctx.Err()
		})
		_, err := svc.Execute(context.Background(), runID)
		require.NoError(t, err)
		t.Fatal("controlled worker must be killed before it returns")
	}
	input := fixture.input
	input.Request.ChapterID = "11111111-1111-4111-8111-111111111114"
	input.Workspace.ChapterID = input.Request.ChapterID
	input.Workspace.ProjectID = "11111111-1111-4111-8111-111111111113"
	input.Sources[0].ProjectID = input.Workspace.ProjectID
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte("worker-crash")))
	run, err := repo.CreateOrReplay(context.Background(), input.Actor, input.Workspace.ProjectID, "worker-crash", hash, input)
	require.NoError(t, err)
	ready := filepath.Join(t.TempDir(), "worker-entered")
	child := exec.Command(os.Args[0], "-test.run=^TestPostgresWorkerCrashLeasePersistence$")
	child.Env = append(os.Environ(), "LINGDOC_FAILURE_CRASH_RUN="+run.ID, "LINGDOC_FAILURE_CRASH_READY="+ready)
	require.NoError(t, child.Start())
	t.Cleanup(func() { _ = child.Process.Kill() })
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(ready); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("child worker did not enter controlled model")
		}
		time.Sleep(10 * time.Millisecond)
	}
	var before generationRow
	require.NoError(t, db.Where("id = ?", run.ID).First(&before).Error)
	require.Equal(t, StatusRunning, before.Status)
	require.NoError(t, child.Process.Kill())
	require.Error(t, child.Wait())
	_, _, claimed, err := repo.Claim(context.Background(), run.ID)
	require.NoError(t, err)
	require.False(t, claimed, "restart must not duplicate work under a still-live lease")
	require.NoError(t, db.Model(&generationRow{}).Where("id = ?", run.ID).Update("updated_at", time.Now().UTC().Add(-claimLeaseDuration-time.Second)).Error)
	svc.Model = testModel{err: context.Canceled}
	recovered, err := svc.Execute(context.Background(), run.ID)
	require.NoError(t, err)
	require.Equal(t, StatusInterrupted, recovered.Status)
	require.ErrorIs(t, repo.Touch(context.Background(), run.ID, before.ClaimToken), ErrRunUnavailable)
	var after generationRow
	require.NoError(t, db.Where("id = ?", run.ID).First(&after).Error)
	require.Nil(t, after.CandidateID)
	require.Equal(t, "generation_interrupted", after.ErrorCode)
}
