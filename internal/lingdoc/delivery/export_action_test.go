package delivery

import (
	"errors"
	"fmt"
	"sync"
	"testing"
)

// 契约把 Idempotency-Key 标成 startExport 的必填头，§6 要求「已完成且同请求，
// 返回原业务结果」。同键重放要换回**原来那一份**，而不是再渲一份——否则一次
// 「其实已经导出好了、只是响应丢了」的重试会在交付历史里多出一条。
func TestExportReplaysTheArtifactForTheSameAction(t *testing.T) {
	snapshots, snapshot := preparedSnapshot(t)
	exports := NewMemoryExportStore()
	renders := 0
	service := NewExportService(snapshots, exports, frozenRenderer(func(DeliveryInput) ([]byte, error) {
		renders++
		return []byte("PK\x03\x04exported docx"), nil
	}), FrozenValidatorFunc(fileValidationNotUnderTest), CurrentnessFunc(currentExportInput), ExportAccessFunc(allowExport))

	first, replayed, err := service.Start("owner", snapshot.ProjectID, snapshot.ID, exportActionKey)
	if err != nil || replayed {
		t.Fatalf("first Start = %+v replayed=%v err=%v", first, replayed, err)
	}
	second, replayed, err := service.Start("owner", snapshot.ProjectID, snapshot.ID, exportActionKey)
	if err != nil {
		t.Fatal(err)
	}
	if !replayed || second.ID != first.ID {
		t.Fatalf("replay = %+v replayed=%v, want the first artifact %s", second, replayed, first.ID)
	}
	if renders != 1 {
		t.Fatalf("renderer ran %d times for one action", renders)
	}
	exports.mu.RLock()
	defer exports.mu.RUnlock()
	if len(exports.exports) != 1 {
		t.Fatalf("one action left %d artifacts behind", len(exports.exports))
	}
}

// 失败产物同样入账：§6 说重试是**新动作**（换新键），所以同一个键必须永远换回
// 同一个结果——包括失败。否则拿旧键重放会变成成功，而调用方以为自己只做过一次。
func TestExportReplaysAFailedArtifactRatherThanRetryingIt(t *testing.T) {
	snapshots, snapshot := preparedSnapshot(t)
	renders := 0
	service := NewExportService(snapshots, NewMemoryExportStore(), frozenRenderer(func(DeliveryInput) ([]byte, error) {
		renders++
		return nil, errors.New("renderer outage")
	}), FrozenValidatorFunc(fileValidationNotUnderTest), CurrentnessFunc(currentExportInput), ExportAccessFunc(allowExport))

	first, _, err := service.Start("owner", snapshot.ProjectID, snapshot.ID, exportActionKey)
	if err != nil || first.Status != ExportFailed {
		t.Fatalf("first = %+v, %v", first, err)
	}
	second, replayed, err := service.Start("owner", snapshot.ProjectID, snapshot.ID, exportActionKey)
	if err != nil || !replayed || second.ID != first.ID || second.Status != ExportFailed {
		t.Fatalf("replayed failure = %+v replayed=%v err=%v", second, replayed, err)
	}
	if renders != 1 {
		t.Fatalf("renderer ran %d times, want the failure to be replayed not retried", renders)
	}
}

// 同一个键换了请求是冲突，不是重试。这里顺带要求没有副作用：冲突之后库里仍只有
// 原来那一份。
func TestExportRefusesAKeyReusedForADifferentSnapshot(t *testing.T) {
	snapshots, snapshot := preparedSnapshot(t)
	second := snapshot
	second.ID = "snapshot-other"
	if err := snapshots.Save(second); err != nil {
		t.Fatal(err)
	}
	exports := NewMemoryExportStore()
	service := NewExportService(snapshots, exports, frozenRenderer(func(DeliveryInput) ([]byte, error) {
		return []byte("PK\x03\x04docx"), nil
	}), FrozenValidatorFunc(fileValidationNotUnderTest), CurrentnessFunc(currentExportInput), ExportAccessFunc(allowExport))

	if _, _, err := service.Start("owner", snapshot.ProjectID, snapshot.ID, exportActionKey); err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.Start("owner", snapshot.ProjectID, second.ID, exportActionKey); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("reused key on another snapshot = %v, want ErrIdempotencyConflict", err)
	}
	exports.mu.RLock()
	defer exports.mu.RUnlock()
	if len(exports.exports) != 1 {
		t.Fatalf("the refused request left %d artifacts behind", len(exports.exports))
	}
}

// 并发下的同键请求只有一个能落笔，输的一方拿回赢家的那一份——一次用户动作在交付
// 历史里只能有一条。
func TestConcurrentSameActionLandsOneArtifact(t *testing.T) {
	snapshots, snapshot := preparedSnapshot(t)
	exports := NewMemoryExportStore()
	service := NewExportService(snapshots, exports, frozenRenderer(func(DeliveryInput) ([]byte, error) {
		return []byte("PK\x03\x04docx"), nil
	}), FrozenValidatorFunc(fileValidationNotUnderTest), CurrentnessFunc(currentExportInput), ExportAccessFunc(allowExport))

	const callers = 8
	results := make([]string, callers)
	errs := make([]error, callers)
	var wg sync.WaitGroup
	for i := range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			artifact, _, err := service.Start("owner", snapshot.ProjectID, snapshot.ID, exportActionKey)
			results[i], errs[i] = artifact.ID, err
		}()
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("caller %d = %v", i, err)
		}
		if results[i] != results[0] {
			t.Fatalf("caller %d got artifact %s, caller 0 got %s", i, results[i], results[0])
		}
	}
	exports.mu.RLock()
	defer exports.mu.RUnlock()
	if len(exports.exports) != 1 {
		t.Fatalf("concurrent retries left %d artifacts behind", len(exports.exports))
	}
}

// §7-5：文件要先过校验才能提供下载。渲染器说成功不等于文件里有该有的东西——
// F13「导出器产生损坏文件」正是这个形状。
func TestExportPersistsValidationFailureButNeverDownloadsIt(t *testing.T) {
	snapshots, snapshot := preparedSnapshot(t)
	exports := NewMemoryExportStore()
	rendered := []byte("PK\x03\x04structurally wrong docx")
	var seen []byte
	service := NewExportService(snapshots, exports, frozenRenderer(func(DeliveryInput) ([]byte, error) {
		return rendered, nil
	}), FrozenValidatorFunc(func(input DeliveryInput, file []byte) error {
		if input.ProjectID != snapshot.ProjectID {
			t.Fatalf("validator got project %q", input.ProjectID)
		}
		seen = file
		return errors.New("附录缺少保留原因")
	}), CurrentnessFunc(currentExportInput), ExportAccessFunc(allowExport))

	artifact, _, err := service.Start("owner", snapshot.ProjectID, snapshot.ID, exportActionKey)
	if err != nil {
		t.Fatal(err)
	}
	if artifact.Status != ExportFailed || artifact.FailureCode != "validation_failed" {
		t.Fatalf("artifact = %+v, want a validation_failed result", artifact)
	}
	// 校验器拿到的是**刚渲染出来的字节**，不是渲染器的自述。
	if string(seen) != string(rendered) {
		t.Fatalf("validator saw %q, want the rendered bytes", seen)
	}
	if _, _, err := service.Download("owner", snapshot.ProjectID, artifact.ID); !errors.Is(err, ErrExportUnavailable) {
		t.Fatalf("file that failed validation was downloadable: %v", err)
	}
}

// 校验失败与渲染失败是两种不同的失败，恢复动作也不同，所以不并成同一个码。
func TestValidationFailureIsDistinctFromRenderFailure(t *testing.T) {
	snapshots, snapshot := preparedSnapshot(t)
	service := NewExportService(snapshots, NewMemoryExportStore(), frozenRenderer(func(DeliveryInput) ([]byte, error) {
		return []byte("PK\x03\x04docx"), nil
	}), FrozenValidatorFunc(func(DeliveryInput, []byte) error {
		return errors.New("结构不符")
	}), CurrentnessFunc(currentExportInput), ExportAccessFunc(allowExport))

	artifact, _, err := service.Start("owner", snapshot.ProjectID, snapshot.ID, exportActionKey)
	if err != nil {
		t.Fatal(err)
	}
	if artifact.FailureCode == "render_failed" {
		t.Fatal("a validation failure was reported as a render failure")
	}
	if artifact.FailureCode != "validation_failed" {
		t.Fatalf("failure code = %q", artifact.FailureCode)
	}
}

// 入参在领域层也要挡一道：传输层挡过一次不代表这一层可以假设调用方守规矩。
func TestExportStartRejectsIncompleteArguments(t *testing.T) {
	snapshots, snapshot := preparedSnapshot(t)
	service := NewExportService(snapshots, NewMemoryExportStore(), frozenRenderer(func(DeliveryInput) ([]byte, error) {
		return []byte("PK\x03\x04docx"), nil
	}), FrozenValidatorFunc(fileValidationNotUnderTest), CurrentnessFunc(currentExportInput), ExportAccessFunc(allowExport))

	tests := []struct {
		name       string
		actor      string
		project    string
		snapshotID string
		key        string
	}{
		{name: "no actor", project: snapshot.ProjectID, snapshotID: snapshot.ID, key: exportActionKey},
		{name: "no project", actor: "owner", snapshotID: snapshot.ID, key: exportActionKey},
		{name: "no snapshot", actor: "owner", project: snapshot.ProjectID, key: exportActionKey},
		{name: "short key", actor: "owner", project: snapshot.ProjectID, snapshotID: snapshot.ID, key: "short"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, _, err := service.Start(test.actor, test.project, test.snapshotID, test.key); !errors.Is(err, ErrInvalidRequest) {
				t.Fatalf("Start = %v, want ErrInvalidRequest", err)
			}
		})
	}
}

// 取不到就是取不到：传输层要拿这个把它答成 404，而不是把调用方写错的 ID
// 报成服务端故障。
func TestGetExportReportsAMissingArtifactAsNotFound(t *testing.T) {
	exports := NewMemoryExportStore()
	if _, err := exports.GetExport("project-1", "export-nope"); !errors.Is(err, ErrExportNotFound) {
		t.Fatalf("GetExport = %v, want ErrExportNotFound", err)
	}
}

// 没有校验器就不许构造：静默少一层校验正是装配处最该拦下的那种状态。
func TestExportServiceRequiresARendererValidatorAndRecorder(t *testing.T) {
	snapshots, _ := preparedSnapshot(t)
	renderer := frozenRenderer(func(DeliveryInput) ([]byte, error) { return []byte("docx"), nil })
	validator := FrozenValidatorFunc(fileValidationNotUnderTest)
	access := ExportAccessFunc(allowExport)

	if service := NewExportService(snapshots, NewMemoryExportStore(), nil, validator, CurrentnessFunc(currentExportInput), access); service != nil {
		t.Fatal("export service was built without a renderer")
	}
	if service := NewExportService(snapshots, NewMemoryExportStore(), renderer, nil, CurrentnessFunc(currentExportInput), access); service != nil {
		t.Fatal("export service was built without a validator")
	}
	if service := NewExportService(snapshots, newPlainExportStore(), renderer, validator, CurrentnessFunc(currentExportInput), access); service != nil {
		t.Fatal("export service was built on a store that cannot record which action produced an artifact")
	}
}

// plainExportStore 是一个只有读写、没有动作记录的产物库：它满足 ExportStore，
// 但**不**满足 ExportRecorder（不能内嵌 MemoryExportStore——那会把两个方法一起
// 提升上来，断言照样成立，这个替身就什么也证明不了）。
type plainExportStore struct {
	mu      sync.Mutex
	exports map[string]ExportArtifact
}

func newPlainExportStore() ExportStore {
	return &plainExportStore{exports: map[string]ExportArtifact{}}
}

func (s *plainExportStore) SaveExport(artifact ExportArtifact) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.exports[exportIndex(artifact.ProjectID, artifact.ID)] = artifact
	return nil
}

func (s *plainExportStore) GetExport(projectID, exportID string) (ExportArtifact, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	artifact, ok := s.exports[exportIndex(projectID, exportID)]
	if !ok {
		return ExportArtifact{}, fmt.Errorf("%w: %s", ErrExportNotFound, exportID)
	}
	return artifact, nil
}
