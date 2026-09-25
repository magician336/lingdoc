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

// Both real SQLite transactions observe revision zero and a missing operation.
// The second transaction is released only after the first commits, forcing an
// actual WAL snapshot upgrade failure rather than relying on scheduler timing.
func TestSpecRetryPreservesIdempotencyOrder(t *testing.T) {
	for _, tc := range []struct {
		name       string
		sameKey    bool
		sameBody   bool
		revoke     bool
		wantReplay bool
		wantError  error
	}{
		{name: "different keys", wantError: ErrVersionConflict},
		{name: "same key same body", sameKey: true, sameBody: true, wantReplay: true},
		{name: "same key different body", sameKey: true, wantError: ErrIdempotencyConflict},
		{name: "revoked before retry", sameKey: true, sameBody: true, revoke: true, wantError: ErrNotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "retry.db")
			setup := testStore(t, path)
			actor := Actor{TenantID: 17, UserID: "writer"}
			seedTenantMember(t, setup, actor)
			raw, _, _, err := setup.CreateProject(context.Background(), actor, "create-retry", CreateProjectInput{Name: "retry", TemplateID: "template-demo"})
			if err != nil {
				t.Fatal(err)
			}
			id := asProject(t, raw).ID
			writers := []*Service{testStore(t, path), testStore(t, path)}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			var wg sync.WaitGroup
			t.Cleanup(func() { cancel(); wg.Wait() })
			ready := make(chan int, 2)
			release := []chan struct{}{make(chan struct{}), make(chan struct{})}
			var lockErrors atomic.Int32
			for i, writer := range writers {
				var waited atomic.Bool
				err := writer.db.Callback().Query().After("gorm:query").Register("test:spec-retry-barrier", func(tx *gorm.DB) {
					if tx.Statement.Table != "lingdoc_operations" || !errors.Is(tx.Error, gorm.ErrRecordNotFound) || !waited.CompareAndSwap(false, true) {
						return
					}
					ready <- i
					select {
					case <-release[i]:
					case <-ctx.Done():
						_ = tx.AddError(ctx.Err())
					}
				})
				if err != nil {
					t.Fatal(err)
				}
				if err := writer.db.Callback().Update().After("gorm:update").Register("test:spec-retry-lock", func(tx *gorm.DB) {
					if isSQLiteLockError(tx.Error) {
						lockErrors.Add(1)
					}
				}); err != nil {
					t.Fatal(err)
				}
			}
			type result struct {
				writer int
				raw    json.RawMessage
				status int
				replay bool
				err    error
			}
			results := make(chan result, 2)
			for i, writer := range writers {
				key, value := "save-retry-first", "first"
				if i == 1 {
					if !tc.sameKey {
						key = "save-retry-second"
					}
					if !tc.sameBody {
						value = "second"
					}
				}
				wg.Add(1)
				go func(index int, service *Service, key, value string) {
					defer wg.Done()
					raw, status, replay, err := service.SaveSpec(ctx, actor, id, key, SaveSpecInput{ExpectedSpecRevision: 0, Fields: map[string]string{"research_goal": value}})
					results <- result{index, raw, status, replay, err}
				}(i, writer, key, value)
			}
			for n := 0; n < 2; n++ {
				select {
				case <-ready:
				case <-ctx.Done():
					t.Fatal("both transactions did not reach the missing-operation barrier")
				}
			}
			receive := func() result {
				t.Helper()
				select {
				case value := <-results:
					return value
				case <-ctx.Done():
					t.Fatal("timed out awaiting writer result")
					return result{}
				}
			}
			close(release[0])
			first := receive()
			if first.writer != 0 || first.err != nil || first.replay || first.status != 200 {
				t.Fatalf("first commit = %+v", first)
			}
			if tc.revoke {
				if err := setup.db.Exec("UPDATE tenant_members SET status = 'inactive' WHERE tenant_id = ? AND user_id = ?", actor.TenantID, actor.UserID).Error; err != nil {
					t.Fatal(err)
				}
			}
			close(release[1])
			second := receive()
			if second.writer != 1 || !errors.Is(second.err, tc.wantError) || second.replay != tc.wantReplay {
				t.Fatalf("second result = %+v, want error %v / replay %v", second, tc.wantError, tc.wantReplay)
			}
			if tc.wantReplay && (second.status != first.status || string(second.raw) != string(first.raw)) {
				t.Fatal("replay did not preserve the committed HTTP status and response")
			}
			if lockErrors.Load() == 0 {
				t.Fatal("test never exercised a real SQLite lock/snapshot failure")
			}
			var project projectRow
			if err := setup.db.Where("id = ?", id).First(&project).Error; err != nil {
				t.Fatal(err)
			}
			if project.SpecRevision != 1 || project.ProjectVersion != 2 || project.SpecJSON != `{"research_goal":"first"}` {
				t.Fatalf("losing or replayed request changed the committed project: %+v", project)
			}
			var count int64
			if err := setup.db.Model(&operationRow{}).Where("operation = ? AND target = ?", "saveSpec", id).Count(&count).Error; err != nil || count != 1 {
				t.Fatalf("save operation count = %d, err = %v; want exactly one", count, err)
			}
		})
	}
}

// Fault injection verifies bounded retry and cancellation independently of the
// actual-WAL contention cases above; it never claims a simulated lock is real.
func TestSpecRetryBoundAndCancellation(t *testing.T) {
	for _, cancelOnLock := range []bool{false, true} {
		name := "exhaustion"
		if cancelOnLock {
			name = "cancellation"
		}
		t.Run(name, func(t *testing.T) {
			svc := testStore(t, filepath.Join(t.TempDir(), "bound.db"))
			actor := Actor{TenantID: 18, UserID: "writer"}
			seedTenantMember(t, svc, actor)
			raw, _, _, err := svc.CreateProject(context.Background(), actor, "create-bound", CreateProjectInput{Name: "bound", TemplateID: "template-demo"})
			if err != nil {
				t.Fatal(err)
			}
			id := asProject(t, raw).ID
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			calls := 0
			if err := svc.db.Callback().Update().Before("gorm:update").Register("test:force-spec-lock", func(tx *gorm.DB) {
				calls++
				_ = tx.AddError(errors.New("database is locked"))
				if cancelOnLock {
					cancel()
				}
			}); err != nil {
				t.Fatal(err)
			}
			started := time.Now()
			_, _, _, err = svc.SaveSpec(ctx, actor, id, "save-bound", SaveSpecInput{ExpectedSpecRevision: 0, Fields: map[string]string{"research_goal": "blocked"}})
			wantErr := ErrRequestInProgress
			if cancelOnLock {
				wantErr = context.Canceled
			}
			if !errors.Is(err, wantErr) || calls < 1 || calls > specLockMaxAttempts || (cancelOnLock && calls != 1) {
				t.Fatalf("error/calls = %v/%d, want %v within bounded attempts", err, calls, wantErr)
			}
			if !cancelOnLock && time.Since(started) < specLockRetryBudget {
				t.Fatal("persistent contention exhausted retries before the recovery budget")
			}
			var project projectRow
			if err := svc.db.Where("id = ?", id).First(&project).Error; err != nil || project.SpecRevision != 0 || project.ProjectVersion != 1 {
				t.Fatalf("failed attempts modified project: %+v, %v", project, err)
			}
			var count int64
			if err := svc.db.Model(&operationRow{}).Where("operation = ? AND target = ?", "saveSpec", id).Count(&count).Error; err != nil || count != 0 {
				t.Fatalf("failed attempts persisted %d operations: %v", count, err)
			}
		})
	}
}
