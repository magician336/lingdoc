package delivery

import (
	"errors"
	"testing"
)

func freezeAttempt() FreezeAttempt {
	return FreezeAttempt{ActorID: "owner", ProjectID: "project-1", Key: "freeze-action-1"}
}

// 冻结与「哪一次动作冻了它」必须一起落。分开写就有个窗口：快照已经在库里、
// 动作还没记上，此时的重试会当作新动作再冻一份——一次用户动作于是变成
// 交付历史里的两条。Freeze 不落存，正是为了让这件事只能一起发生。
func TestFreezeDoesNotPersistUntilTheActionIsRecorded(t *testing.T) {
	forEachSnapshotStore(t, testFreezeDoesNotPersistUntilTheActionIsRecorded)
}

func testFreezeDoesNotPersistUntilTheActionIsRecorded(t *testing.T, store snapshotStoreUnderTest) {
	service := NewReleaseService(store)

	snapshot, err := service.Freeze(validDeliveryInput())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get("project-1", snapshot.ID); !errors.Is(err, ErrSnapshotNotFound) {
		t.Fatalf("Freeze left a snapshot in the store: %v", err)
	}

	recorded, replayed, err := store.RecordFreeze(snapshot, freezeAttempt(), "hash-1")
	if err != nil {
		t.Fatal(err)
	}
	if replayed {
		t.Fatal("the first record of an action reports a replay")
	}
	if recorded.ID != snapshot.ID {
		t.Fatalf("recorded %s, want the frozen %s", recorded.ID, snapshot.ID)
	}
	stored, err := store.Get("project-1", snapshot.ID)
	if err != nil {
		t.Fatalf("recorded snapshot is not readable: %v", err)
	}
	if stored.SnapshotDigest != snapshot.SnapshotDigest {
		t.Fatalf("stored digest = %s, want %s", stored.SnapshotDigest, snapshot.SnapshotDigest)
	}
}

// 重试要换回原来那一份，而不是再冻一份：契约 §6 说已完成且同请求返回原业务结果。
func TestRecordFreezeReplaysTheSnapshotForTheSameAction(t *testing.T) {
	forEachSnapshotStore(t, testRecordFreezeReplaysTheSnapshotForTheSameAction)
}

func testRecordFreezeReplaysTheSnapshotForTheSameAction(t *testing.T, store snapshotStoreUnderTest) {
	service := NewReleaseService(store)
	first, err := service.Freeze(validDeliveryInput())
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.RecordFreeze(first, freezeAttempt(), "hash-1"); err != nil {
		t.Fatal(err)
	}

	// 第二次冻结同样合法，拿到的却必须是第一次那一份——这正是重放的意义。
	second, err := service.Freeze(validDeliveryInput())
	if err != nil {
		t.Fatal(err)
	}
	if second.ID == first.ID {
		t.Fatal("the test needs two distinct freezes to tell replay from a fresh freeze")
	}

	replayed, isReplay, err := store.RecordFreeze(second, freezeAttempt(), "hash-1")
	if err != nil {
		t.Fatal(err)
	}
	if !isReplay || replayed.ID != first.ID {
		t.Fatalf("RecordFreeze = %s replayed=%v, want the first snapshot %s", replayed.ID, isReplay, first.ID)
	}

	// 读侧也必须能重放：它要在版本比较之前跑。
	fromRead, found, err := store.ReplayFreeze(freezeAttempt(), "hash-1")
	if err != nil {
		t.Fatal(err)
	}
	if !found || fromRead.ID != first.ID {
		t.Fatalf("ReplayFreeze = %s found=%v, want %s", fromRead.ID, found, first.ID)
	}
	if _, found, err := store.ReplayFreeze(freezeAttempt(), "hash-1"); err != nil || !found {
		t.Fatalf("second ReplayFreeze found=%v err=%v", found, err)
	}
	if _, found, err := store.ReplayFreeze(FreezeAttempt{ActorID: "owner", ProjectID: "project-1", Key: "another-action"}, "hash-1"); err != nil || found {
		t.Fatalf("an unrecorded action replayed: found=%v err=%v", found, err)
	}
}

// 同一个键换了请求是冲突，不是重试，也不能把新内容当成旧动作的结果交出去。
// 这里同时要求没有副作用：冲突之后库里仍只有原来那一份。
func TestRecordFreezeRefusesAKeyReusedForADifferentRequest(t *testing.T) {
	forEachSnapshotStore(t, testRecordFreezeRefusesAKeyReusedForADifferentRequest)
}

func testRecordFreezeRefusesAKeyReusedForADifferentRequest(t *testing.T, store snapshotStoreUnderTest) {
	service := NewReleaseService(store)
	first, err := service.Freeze(validDeliveryInput())
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.RecordFreeze(first, freezeAttempt(), "hash-1"); err != nil {
		t.Fatal(err)
	}

	if _, _, err := store.ReplayFreeze(freezeAttempt(), "hash-2"); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("ReplayFreeze = %v, want ErrIdempotencyConflict", err)
	}
	second, err := service.Freeze(validDeliveryInput())
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.RecordFreeze(second, freezeAttempt(), "hash-2"); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("RecordFreeze = %v, want ErrIdempotencyConflict", err)
	}
	if _, err := store.Get("project-1", second.ID); !errors.Is(err, ErrSnapshotNotFound) {
		t.Fatalf("the refused request left a snapshot behind: %v", err)
	}
}

// 取不到就是取不到：传输层要拿这个把它答成 404，而不是把调用方写错的 ID
// 报成服务端故障。
func TestGetReportsAMissingSnapshotAsNotFound(t *testing.T) {
	forEachSnapshotStore(t, testGetReportsAMissingSnapshotAsNotFound)
}

func testGetReportsAMissingSnapshotAsNotFound(t *testing.T, store snapshotStoreUnderTest) {
	if _, err := store.Get("project-1", "snapshot-nope"); !errors.Is(err, ErrSnapshotNotFound) {
		t.Fatalf("Get = %v, want ErrSnapshotNotFound", err)
	}
}

// 一份快照的归属由它自己的 project_id 决定：换个项目去取，取到的必须是「没有」。
func TestSnapshotIsScopedToItsProject(t *testing.T) {
	forEachSnapshotStore(t, testSnapshotIsScopedToItsProject)
}

func testSnapshotIsScopedToItsProject(t *testing.T, store snapshotStoreUnderTest) {
	service := NewReleaseService(store)
	snapshot, err := service.Prepare(validDeliveryInput())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get("project-2", snapshot.ID); !errors.Is(err, ErrSnapshotNotFound) {
		t.Fatalf("Get from another project = %v, want ErrSnapshotNotFound", err)
	}
}
