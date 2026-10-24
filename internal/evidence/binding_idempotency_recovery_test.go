package evidence

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"sync/atomic"
	"testing"

	"gorm.io/gorm"
)

func operationHash(t *testing.T, knowledgeID string) string {
	t.Helper()
	body, err := json.Marshal(struct {
		KnowledgeID string `json:"knowledge_id"`
	}{knowledgeID})
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

// WAL permits the winner to commit on a second connection after the loser
// has read an empty operation snapshot. The loser then fails its write and
// must leave that transaction before reading the winner's committed response.
// Actual PostgreSQL unique-key contention is separately exercised over HTTP.
func TestBindIdempotentRereadsCommittedWinnerAfterRollback(t *testing.T) {
	for _, conflicting := range []bool{false, true} {
		t.Run(map[bool]string{false: "matching_request", true: "different_request"}[conflicting], func(t *testing.T) {
			store := newTestStore(t)
			if err := store.db.Exec("PRAGMA journal_mode=WAL").Error; err != nil {
				t.Fatal(err)
			}
			if err := store.db.AutoMigrate(&operationRow{}); err != nil {
				t.Fatal(err)
			}
			ctx := context.Background()
			in := bindInput("p-race", "k-race", nil)
			asset, err := store.Bind(ctx, in)
			if err != nil {
				t.Fatal(err)
			}
			response, err := json.Marshal(asset)
			if err != nil {
				t.Fatal(err)
			}
			hash := operationHash(t, in.KnowledgeID)
			if conflicting {
				hash = operationHash(t, "different-knowledge")
			}
			var inserted atomic.Bool
			if err := store.db.Callback().Query().After("gorm:query").Register("r1:commit-winning-operation", func(tx *gorm.DB) {
				if tx.Statement.Table != "lingdoc_operations" || !errors.Is(tx.Error, gorm.ErrRecordNotFound) || !inserted.CompareAndSwap(false, true) {
					return
				}
				if err := store.db.Create(&operationRow{TenantID: 1, UserID: "u-race", Operation: "bindAsset", Target: in.ProjectID,
					Key: "race-key-606", BodyHash: hash, ResponseJSON: string(response), ResponseCode: 201}).Error; err != nil {
					t.Fatalf("winner must commit independently: %v", err)
				}
			}); err != nil {
				t.Fatal(err)
			}
			got, replay, err := store.BindIdempotent(ctx, 1, "u-race", in.ProjectID, "race-key-606", in)
			if !inserted.Load() {
				t.Fatal("race fixture did not execute")
			}
			if conflicting {
				if !errors.Is(err, ErrIdempotencyConflict) || replay || got != (Asset{}) {
					t.Fatalf("different request replayed: got=%+v replay=%v err=%v", got, replay, err)
				}
			} else if err != nil || !replay || !reflect.DeepEqual(got, asset) {
				t.Fatalf("committed winner not replayed: got=%+v replay=%v err=%v", got, replay, err)
			}
			for model, expected := range map[string]int64{"lingdoc_project_assets": 1, "lingdoc_asset_revisions": 1, "lingdoc_operations": 1} {
				var count int64
				if err := store.db.Table(model).Count(&count).Error; err != nil || count != expected {
					t.Fatalf("%s count=%d err=%v", model, count, err)
				}
			}
		})
	}
}

func TestBindIdempotentWriteFailureNeverReturnsPartialAsset(t *testing.T) {
	store := newTestStore(t)
	if err := store.db.AutoMigrate(&operationRow{}); err != nil {
		t.Fatal(err)
	}
	wantErr := errors.New("synthetic operation record write failed")
	if err := store.db.Callback().Create().Before("gorm:create").Register("r1:fail-operation-write", func(tx *gorm.DB) {
		if tx.Statement.Table == "lingdoc_operations" {
			tx.AddError(wantErr)
		}
	}); err != nil {
		t.Fatal(err)
	}
	in := bindInput("p-rollback", "k-rollback", nil)
	got, replay, err := store.BindIdempotent(context.Background(), 1, "u-race", in.ProjectID, "rollback-key-606", in)
	if !errors.Is(err, wantErr) || replay || got != (Asset{}) {
		t.Fatalf("returned a partial success: got=%+v replay=%v err=%v", got, replay, err)
	}
	for _, table := range []string{"lingdoc_project_assets", "lingdoc_asset_revisions", "lingdoc_operations"} {
		var count int64
		if err := store.db.Table(table).Count(&count).Error; err != nil || count != 0 {
			t.Fatalf("partial write in %s: count=%d err=%v", table, count, err)
		}
	}
}
