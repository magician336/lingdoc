package workspace

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
)

// This exercises real routes, project authorization, the transactional object
// store, idempotency and audit using a synthetic already-completed run.
func TestResearchRoundFeedbackHTTP(t *testing.T) {
	engine, db, _ := newResearchProviderFixture(t, true)
	pool, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	pool.SetMaxOpenConns(1)
	run := core.ResearchRun{ID: "feedback-run", ProjectID: "project-1", Version: 1, TaskID: "task-1", TaskVersion: 1, Basis: core.VersionBasis{ProjectVersion: 1, AssetVersions: []core.ProjectAssetVersion{}}, Status: "completed", RunMode: "real", StartedAt: time.Now().UTC(), Rounds: []core.ResearchRound{{Number: 1, Query: "synthetic query", Status: "completed", Provider: "synthetic", RetrievedAt: time.Now().UTC(), ResultIDs: []string{}}}, Results: []core.ResearchResult{}}
	raw, err := json.Marshal(run)
	if err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"lingdoc_g2_objects", "lingdoc_g2_object_versions"} {
		if err := db.Exec("INSERT INTO "+table+" (project_id,kind,id,version,payload_json) VALUES (?,?,?,?,?)", run.ProjectID, core.ResearchRunKind, run.ID, 1, string(raw)).Error; err != nil {
			t.Fatal(err)
		}
	}
	path := "/api/projects/project-1/research-runs/feedback-run/feedback"
	call := func(user, key, body string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body)).WithContext(actorContext(user))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Idempotency-Key", key)
		response := httptest.NewRecorder()
		engine.ServeHTTP(response, request)
		return response
	}
	read := func() core.ResearchRun {
		request := httptest.NewRequest(http.MethodGet, "/api/projects/project-1/research-runs/feedback-run", nil).WithContext(actorContext("reader"))
		response := httptest.NewRecorder()
		engine.ServeHTTP(response, request)
		if response.Code != 200 {
			t.Fatalf("read status=%d: %s", response.Code, response.Body.String())
		}
		var envelope struct {
			Data core.ResearchRun `json:"data"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
			t.Fatal(err)
		}
		return envelope.Data
	}
	input := `{"expected_project_version":1,"expected_context_revision":0,"expected_version":1,"round_number":1,"feedback":"  检索范围需补充反例  "}`
	// A worker may still append rounds or settle quota; reject feedback until
	// the run has stopped so this write cannot overwrite progress.
	running := run
	running.Status = "running"
	runningJSON, err := json.Marshal(running)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("UPDATE lingdoc_g2_objects SET payload_json=? WHERE project_id=? AND kind=? AND id=?", string(runningJSON), run.ProjectID, core.ResearchRunKind, run.ID).Error; err != nil {
		t.Fatal(err)
	}
	if active := call("reader", "feedback-active", input); active.Code < 400 || active.Code >= 500 {
		t.Fatalf("active run should reject feedback, got %d", active.Code)
	}
	if err := db.Exec("UPDATE lingdoc_g2_objects SET payload_json=? WHERE project_id=? AND kind=? AND id=?", string(raw), run.ProjectID, core.ResearchRunKind, run.ID).Error; err != nil {
		t.Fatal(err)
	}
	first := call("reader", "feedback-1", input)
	if first.Code != 200 {
		t.Fatalf("save status=%d body=%s", first.Code, first.Body.String())
	}
	got := read()
	if got.Version != 2 || got.Rounds[0].Feedback != "检索范围需补充反例" || got.Rounds[0].FeedbackBy != "reader" || got.Rounds[0].FeedbackAt == nil {
		t.Fatalf("feedback readback=%+v", got.Rounds[0])
	}
	if replay := call("reader", "feedback-1", input); replay.Code != 200 {
		t.Fatalf("replay status=%d %s", replay.Code, replay.Body.String())
	}
	if got = read(); got.Version != 2 {
		t.Fatalf("replay wrote a second version: %d", got.Version)
	}
	if conflict := call("reader", "feedback-1", strings.Replace(input, "补充反例", "修改", 1)); conflict.Code != http.StatusConflict {
		t.Fatalf("idempotency conflict status=%d", conflict.Code)
	}
	if conflict := call("reader", "feedback-1", strings.Replace(input, "检索范围", " 检索范围", 1)); conflict.Code != http.StatusConflict {
		t.Fatalf("whitespace change must also conflict, status=%d", conflict.Code)
	}
	if stale := call("reader", "feedback-2", input); stale.Code != http.StatusConflict {
		t.Fatalf("stale version status=%d", stale.Code)
	}
	if missing := call("reader", "feedback-missing", `{"expected_project_version":1,"expected_context_revision":0,"expected_version":2,"round_number":9,"feedback":"note"}`); missing.Code != http.StatusNotFound {
		t.Fatalf("unknown round status=%d", missing.Code)
	}
	if outsider := call("outsider", "feedback-outsider", input); outsider.Code != http.StatusNotFound && outsider.Code != http.StatusForbidden {
		t.Fatalf("outsider status=%d", outsider.Code)
	}
	var versions int64
	if err := db.Table("lingdoc_g2_object_versions").Where("project_id=? AND kind=? AND id=?", run.ProjectID, core.ResearchRunKind, run.ID).Count(&versions).Error; err != nil {
		t.Fatal(err)
	}
	if versions != 2 {
		t.Fatalf("history versions=%d want=2", versions)
	}
	var audits int64
	if err := db.Table("lingdoc_project_audits").Where("project_id=? AND action=?", run.ProjectID, "g2.saveResearchRoundFeedback").Count(&audits).Error; err != nil {
		t.Fatal(err)
	}
	if audits != 1 {
		t.Fatalf("feedback audit count=%d", audits)
	}
	// A failed audit must roll back the new run version and operation receipt.
	if err := db.Exec(`CREATE TRIGGER reject_feedback BEFORE INSERT ON lingdoc_project_audits WHEN NEW.action = 'g2.saveResearchRoundFeedback' BEGIN SELECT RAISE(ABORT,'synthetic failure'); END`).Error; err != nil {
		t.Fatal(err)
	}
	second := fmt.Sprintf(`{"expected_project_version":1,"expected_context_revision":0,"expected_version":%d,"round_number":1,"feedback":"second"}`, got.Version)
	if failure := call("reader", "feedback-audit-failure", second); failure.Code < 500 {
		t.Fatalf("expected audit error, got %d", failure.Code)
	}
	if got = read(); got.Version != 2 || got.Rounds[0].Feedback != "检索范围需补充反例" {
		t.Fatalf("audit failure changed run: %+v", got)
	}
	if err := db.Exec("DROP TRIGGER reject_feedback").Error; err != nil {
		t.Fatal(err)
	}
	if retry := call("reader", "feedback-audit-failure", second); retry.Code != 200 {
		t.Fatalf("retry after rollback status=%d %s", retry.Code, retry.Body.String())
	}
	// Both writers use the same expected version. Exactly one can advance it.
	const raceBody = `{"expected_project_version":1,"expected_context_revision":0,"expected_version":3,"round_number":1,"feedback":"concurrent"}`
	var wg sync.WaitGroup
	statuses := make(chan int, 2)
	for _, key := range []string{"feedback-race-a", "feedback-race-b"} {
		wg.Add(1)
		go func(key string) { defer wg.Done(); statuses <- call("reader", key, raceBody).Code }(key)
	}
	wg.Wait()
	close(statuses)
	wins, conflicts := 0, 0
	for status := range statuses {
		if status == 200 {
			wins++
		} else if status == 409 {
			conflicts++
		} else {
			t.Fatalf("concurrent status=%d", status)
		}
	}
	if wins != 1 || conflicts != 1 {
		t.Fatalf("concurrent outcomes success=%d conflict=%d", wins, conflicts)
	}
}
