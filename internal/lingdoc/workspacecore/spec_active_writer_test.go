package workspacecore

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"gorm.io/gorm"
)

// Keep an actual WAL writer uncommitted until the competing request has seen
// FIVE lock failures. The old initial-attempt-plus-three-retries policy returns
// early and fails this test. No driver error is injected, and no arbitrary
// sleep decides when the writer is allowed to finish.
func TestSpecRetryWaitsForActiveWriter(t *testing.T) {
	for _, tc := range []struct {
		name       string
		key        string
		value      string
		wantReplay bool
		wantError  error
	}{
		{name: "different keys", key: "save-active-other", value: "other", wantError: ErrVersionConflict},
		{name: "same key same body", key: "save-active-writer", value: "winner", wantReplay: true},
		{name: "same key different body", key: "save-active-writer", value: "other", wantError: ErrIdempotencyConflict},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "active-writer.db")
			setup := testStore(t, path)
			actor := Actor{TenantID: 19, UserID: "writer"}
			seedTenantMember(t, setup, actor)
			raw, _, _, err := setup.CreateProject(context.Background(), actor, "create-active", CreateProjectInput{Name: "active", TemplateID: "template-demo"})
			if err != nil {
				t.Fatal(err)
			}
			projectID := asProject(t, raw).ID
			winner, loser := testStore(t, path), testStore(t, path)
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			var wg sync.WaitGroup
			t.Cleanup(func() { cancel(); wg.Wait() })

			locked, release, retried := make(chan struct{}), make(chan struct{}), make(chan struct{})
			var paused atomic.Bool
			if err := winner.db.Callback().Create().After("gorm:create").Register("test:hold-spec-commit", func(tx *gorm.DB) {
				if tx.Error != nil || tx.Statement.Table != "lingdoc_operations" || !paused.CompareAndSwap(false, true) {
					return
				}
				close(locked)
				select {
				case <-release:
				case <-ctx.Done():
					_ = tx.AddError(ctx.Err())
				}
			}); err != nil {
				t.Fatal(err)
			}
			var lockFailures atomic.Int32
			if err := loser.db.Callback().Update().After("gorm:update").Register("test:count-active-writer-locks", func(tx *gorm.DB) {
				if isSQLiteLockError(tx.Error) && lockFailures.Add(1) == 5 {
					close(retried)
				}
			}); err != nil {
				t.Fatal(err)
			}
			type result struct {
				raw    json.RawMessage
				status int
				replay bool
				err    error
			}
			start := func(svc *Service, key, value string) <-chan result {
				out := make(chan result, 1)
				wg.Add(1)
				go func() {
					defer wg.Done()
					raw, status, replay, err := svc.SaveSpec(ctx, actor, projectID, key,
						SaveSpecInput{ExpectedSpecRevision: 0, Fields: map[string]string{"research_goal": value}})
					out <- result{raw, status, replay, err}
				}()
				return out
			}
			firstResult := start(winner, "save-active-writer", "winner")
			select {
			case <-locked:
			case first := <-firstResult:
				t.Fatalf("winner did not reach the commit barrier: %v", first.err)
			case <-ctx.Done():
				t.Fatal("timed out waiting for the active writer")
			}
			secondResult := start(loser, tc.key, tc.value)
			select {
			case <-retried:
			case second := <-secondResult:
				t.Fatalf("writer returned before five real lock failures: %v (locks=%d)", second.err, lockFailures.Load())
			case <-ctx.Done():
				t.Fatal("competing request did not retry the active writer")
			}
			close(release)
			receive := func(out <-chan result) result {
				t.Helper()
				select {
				case value := <-out:
					return value
				case <-ctx.Done():
					t.Fatal("writer did not complete after releasing commit")
					return result{}
				}
			}
			first, second := receive(firstResult), receive(secondResult)
			if first.err != nil || first.status != 200 || first.replay {
				t.Fatalf("winner = %+v", first)
			}
			if !errors.Is(second.err, tc.wantError) || second.replay != tc.wantReplay {
				t.Fatalf("loser = %+v; want %v / replay=%v", second, tc.wantError, tc.wantReplay)
			}
			if tc.wantReplay && (second.status != 200 || string(second.raw) != string(first.raw)) {
				t.Fatal("retry did not return the winner's exact committed acknowledgement")
			}
			project, err := setup.GetProject(context.Background(), actor, projectID)
			if err != nil || project.SpecRevision != 1 || project.ProjectVersion != 2 || project.Spec["research_goal"] != "winner" {
				t.Fatalf("unexpected final project = %+v, %v", project, err)
			}
			var count int64
			if err := setup.db.Model(&operationRow{}).Where("operation = ? AND target = ?", "saveSpec", projectID).Count(&count).Error; err != nil || count != 1 {
				t.Fatalf("operation records = %d, err=%v; want exactly one", count, err)
			}
		})
	}
}
