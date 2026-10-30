package workspace

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
)

// Exercise the production catalog HTTP route and persistent object history.
// The pre-existing confirmed associated card is a defensive legacy fixture:
// the current application does not yet permit confirming such a card.
func TestAcademicCatalogHTTPInvalidatesLegacyAssociatedEvidence(t *testing.T) {
	engine, db, _ := newResearchProviderFixture(t, true)
	association := core.AcademicSourceAssociation{ID: "http-association", ProjectID: "project-1", Version: 1, DOI: "10.5555/http", CatalogVersion: "old-catalog", Status: "recorded"}
	card := core.EvidenceCard{ID: "http-card", ProjectID: "project-1", Version: 1, Status: "CONFIRMED", AcademicAssociationRefs: []core.VersionRef{{ID: association.ID, Version: 1}}, Confirmation: &core.EvidenceConfirmation{CardVersion: 1, ConfirmedBy: "reader"}}
	for _, item := range []struct {
		kind  string
		id    string
		value any
	}{{core.AcademicSourceAssociationKind, association.ID, association}, {core.EvidenceCardKind, card.ID, card}} {
		raw, err := json.Marshal(item.value)
		if err != nil {
			t.Fatal(err)
		}
		for _, table := range []string{"lingdoc_g2_objects", "lingdoc_g2_object_versions"} {
			if err = db.Exec("INSERT INTO "+table+" (project_id, kind, id, version, payload_json) VALUES (?, ?, ?, ?, ?)", "project-1", item.kind, item.id, 1, string(raw)).Error; err != nil {
				t.Fatal(err)
			}
		}
	}
	request := func(user, key string) *httptest.ResponseRecorder {
		t.Helper()
		body := `{"expected_project_version":1,"expected_context_revision":0,"expected_version":0,"rules":[{"id":"journal","source_type":"journal","venue_name":"Example","venue_ids":["1234-5678"]}]}`
		req := httptest.NewRequest(http.MethodPut, "/api/projects/project-1/academic-source-catalog", strings.NewReader(body)).WithContext(actorContext(user))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", key)
		out := httptest.NewRecorder()
		engine.ServeHTTP(out, req)
		return out
	}
	if denied := request("outsider", "http-denied"); denied.Code < 400 || denied.Code >= 500 {
		t.Fatalf("unknown user update status=%d body=%s", denied.Code, denied.Body.String())
	}
	if result := request("reader", "http-catalog-change"); result.Code != http.StatusOK {
		t.Fatalf("catalog update status=%d body=%s", result.Code, result.Body.String())
	}
	var stored struct {
		Version int64  `gorm:"column:version"`
		Payload string `gorm:"column:payload_json"`
	}
	if err := db.Table("lingdoc_g2_objects").Where("project_id = ? AND kind = ? AND id = ?", "project-1", core.EvidenceCardKind, card.ID).Take(&stored).Error; err != nil {
		t.Fatal(err)
	}
	var current core.EvidenceCard
	if err := json.Unmarshal([]byte(stored.Payload), &current); err != nil {
		t.Fatal(err)
	}
	if stored.Version != 2 || current.Status != "NEEDS_REVIEW" || current.InvalidReason != "academic_catalog_changed" || current.Confirmation != nil {
		t.Fatalf("HTTP update did not persist invalidation: %+v", current)
	}
	var history int64
	if err := db.Table("lingdoc_g2_object_versions").Where("project_id = ? AND kind = ? AND id = ?", "project-1", core.EvidenceCardKind, card.ID).Count(&history).Error; err != nil || history != 2 {
		t.Fatalf("history count=%d error=%v", history, err)
	}
	if replay := request("reader", "http-catalog-change"); replay.Code != http.StatusOK {
		t.Fatalf("idempotent replay status=%d body=%s", replay.Code, replay.Body.String())
	}
}
