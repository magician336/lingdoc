package workspacecore

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/evidence"
	"github.com/Tencent/WeKnora/internal/lingdoc/template"
	"github.com/Tencent/WeKnora/internal/types"
)

func fieldArchiveFixture(t *testing.T) (*Service, Actor, Project) {
	t.Helper()
	s := testStore(t, filepath.Join(t.TempDir(), "field-archive.db"))
	actor := Actor{TenantID: 9862, UserID: "archive-owner"}
	seedTenantMember(t, s, actor)
	raw, _, _, err := s.CreateProject(context.Background(), actor, "archive-project", CreateProjectInput{Name: "archive fixture", TemplateID: "template-demo"})
	if err != nil {
		t.Fatal(err)
	}
	project := asProject(t, raw)
	raw, _, _, err = s.SaveSpec(context.Background(), actor, project.ID, "archive-spec", SaveSpecInput{
		ExpectedSpecRevision: project.SpecRevision, Fields: map[string]string{"research_subject": "subject", "research_goal": "old goal"},
	})
	if err != nil {
		t.Fatal(err)
	}
	return s, actor, asProject(t, raw)
}

func archiveCandidate(p Project, fieldID string) TemplateCopyDefinitionInput {
	d := template.Clone(p.TemplateCopy.Definition)
	d.Fields = slices.DeleteFunc(d.Fields, func(field template.Field) bool { return field.ID == fieldID })
	d.RequiredFields = slices.DeleteFunc(d.RequiredFields, func(id string) bool { return id == fieldID })
	return TemplateCopyDefinitionInput{ExpectedProjectVersion: p.ProjectVersion, ExpectedTemplateCopyVersion: p.TemplateCopyVersion,
		Fields: d.Fields, Sections: d.Sections, Terms: append([]template.Term{}, d.Terms...), RequiredFields: d.RequiredFields, Rules: d.Rules}
}

func archiveInputs(p Project, fieldID string) (CreateFieldArchiveInput, RemoveArchivedFieldInput) {
	definition := archiveCandidate(p, fieldID)
	return CreateFieldArchiveInput{FieldID: fieldID, ExpectedSpecRevision: p.SpecRevision, TemplateCopy: definition},
		RemoveArchivedFieldInput{Confirmed: true, ExpectedArchiveVersion: 1, ExpectedSpecRevision: p.SpecRevision, TemplateCopy: definition}
}

func TestFieldArchiveTwoStagesAndReplay(t *testing.T) {
	s, actor, project := fieldArchiveFixture(t)
	create, remove := archiveInputs(project, "research_goal")
	first, err := s.CreateFieldArchiveResult(context.Background(), actor, project.ID, "archive-create", create)
	if err != nil || first.Replayed || first.Archive.ID == "" || first.Archive.Status != "recorded" {
		t.Fatalf("create archive: %+v err=%v", first, err)
	}
	view, err := s.GetFieldArchive(context.Background(), actor, project.ID, first.Archive.ID)
	if err != nil || !view.ValuePresent || view.Value != "old goal" || !view.MetadataPresent || view.Metadata == nil || view.Metadata.Value != "old goal" || view.ValueRedacted {
		t.Fatalf("recorded data not readable: %+v err=%v", view, err)
	}
	before, err := s.GetProject(context.Background(), actor, project.ID)
	if err != nil || before.ProjectVersion != project.ProjectVersion || before.Spec["research_goal"] != "old goal" {
		t.Fatalf("first stage changed current value: %+v err=%v", before, err)
	}
	if replay, err := s.CreateFieldArchiveResult(context.Background(), actor, project.ID, "archive-create", create); err != nil || !replay.Replayed || replay.Archive.ID != first.Archive.ID {
		t.Fatalf("archive replay: %+v err=%v", replay, err)
	}
	unconfirmed := remove
	unconfirmed.Confirmed = false
	if _, err := s.RemoveArchivedFieldResult(context.Background(), actor, project.ID, first.Archive.ID, "archive-unconfirmed", unconfirmed); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("unconfirmed removal accepted: %v", err)
	}
	machine := types.WithPrincipal(context.Background(), types.Principal{Type: types.PrincipalAPITenant, ID: "machine"})
	if _, err := s.RemoveArchivedFieldResult(machine, actor, project.ID, first.Archive.ID, "archive-machine", remove); err == nil {
		t.Fatalf("machine confirmed removal: %v", err)
	}
	wrongTarget := remove
	wrongTarget.TemplateCopy = create.TemplateCopy
	wrongTarget.TemplateCopy.Fields = append(append([]template.Field{}, wrongTarget.TemplateCopy.Fields...), template.Field{ID: "research_goal", Label: "Goal", Type: "string"})
	if _, err := s.RemoveArchivedFieldResult(context.Background(), actor, project.ID, first.Archive.ID, "archive-wrong-target", wrongTarget); err == nil {
		t.Fatal("removal accepted a target that retained the field")
	}
	removed, err := s.RemoveArchivedFieldResult(context.Background(), actor, project.ID, first.Archive.ID, "archive-remove", remove)
	if err != nil || removed.Replayed || removed.Archive.Status != "removed" || removed.Archive.ArchiveVersion != 2 ||
		removed.Archive.ProjectVersion != project.ProjectVersion+1 || removed.Archive.SpecRevision != project.SpecRevision+1 {
		t.Fatalf("confirm removal: %+v err=%v", removed, err)
	}
	if replay, err := s.RemoveArchivedFieldResult(context.Background(), actor, project.ID, first.Archive.ID, "archive-remove", remove); err != nil || !replay.Replayed {
		t.Fatalf("remove replay: %+v err=%v", replay, err)
	}
	after, err := s.GetProject(context.Background(), actor, project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := after.Spec["research_goal"]; exists {
		t.Fatal("removed value remains current")
	}
	if _, exists := after.SpecFields["research_goal"]; exists {
		t.Fatal("removed metadata remains current")
	}
	view, err = s.GetFieldArchive(context.Background(), actor, project.ID, first.Archive.ID)
	if err != nil || view.Status != "removed" || view.Value != "old goal" || view.Metadata == nil || view.Metadata.Value != "old goal" {
		t.Fatalf("historical archive changed after removal: %+v err=%v", view, err)
	}
	definition := archiveCandidate(after, "research_goal")
	if _, _, _, err := s.SaveTemplateCopy(context.Background(), actor, project.ID, "archive-finish-template", definition); err != nil {
		t.Fatalf("target definition could not be saved after archival: %v", err)
	}
}

func TestFieldArchiveRejectsStaleAndCrossProject(t *testing.T) {
	s, actor, project := fieldArchiveFixture(t)
	create, remove := archiveInputs(project, "research_goal")
	receipt, err := s.CreateFieldArchiveResult(context.Background(), actor, project.ID, "stale-archive", create)
	if err != nil {
		t.Fatal(err)
	}
	otherRaw, _, _, err := s.CreateProject(context.Background(), actor, "archive-other-project", CreateProjectInput{Name: "other", TemplateID: "template-demo"})
	if err != nil {
		t.Fatal(err)
	}
	other := asProject(t, otherRaw)
	if _, err := s.GetFieldArchive(context.Background(), actor, other.ID, receipt.Archive.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-project archive leaked: %v", err)
	}
	foreign := actor
	foreign.TenantID++
	if _, err := s.GetFieldArchive(context.Background(), foreign, project.ID, receipt.Archive.ID); err == nil {
		t.Fatal("cross-tenant archive leaked")
	}
	raw, _, _, err := s.SaveSpec(context.Background(), actor, project.ID, "archive-concurrent-spec", SaveSpecInput{
		ExpectedSpecRevision: project.SpecRevision, Fields: map[string]string{"research_subject": "subject", "research_goal": "new goal"},
	})
	if err != nil {
		t.Fatal(err)
	}
	current := asProject(t, raw)
	if _, err := s.RemoveArchivedFieldResult(context.Background(), actor, project.ID, receipt.Archive.ID, "archive-stale-remove", remove); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("stale snapshot removed a changed value: %v", err)
	}
	after, err := s.GetProject(context.Background(), actor, project.ID)
	if err != nil || after.Spec["research_goal"] != "new goal" || after.ProjectVersion != current.ProjectVersion {
		t.Fatalf("rejected stale removal changed project: %+v err=%v", after, err)
	}
}

func TestFieldArchiveConcurrentConfirmationAppliesOnlyOnce(t *testing.T) {
	s, actor, project := fieldArchiveFixture(t)
	create, remove := archiveInputs(project, "research_goal")
	receipt, err := s.CreateFieldArchiveResult(context.Background(), actor, project.ID, "concurrent-archive", create)
	if err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	var wait sync.WaitGroup
	for _, key := range []string{"concurrent-remove-a", "concurrent-remove-b"} {
		wait.Add(1)
		go func(key string) {
			defer wait.Done()
			<-start
			_, err := s.RemoveArchivedFieldResult(context.Background(), actor, project.ID, receipt.Archive.ID, key, remove)
			results <- err
		}(key)
	}
	close(start)
	wait.Wait()
	close(results)
	successes, conflicts := 0, 0
	for err := range results {
		if err == nil {
			successes++
		} else if errors.Is(err, ErrVersionConflict) {
			conflicts++
		} else {
			t.Fatalf("unexpected concurrent result: %v", err)
		}
	}
	after, err := s.GetProject(context.Background(), actor, project.ID)
	if err != nil || successes != 1 || conflicts != 1 || after.ProjectVersion != project.ProjectVersion+1 || after.SpecRevision != project.SpecRevision+1 {
		t.Fatalf("concurrent confirmations did not serialize: success=%d conflict=%d project=%+v err=%v", successes, conflicts, after, err)
	}
	var audits int64
	if err := s.testDB().Table("lingdoc_project_audits").Where("project_id = ? AND action = ?", project.ID, "project_template.remove_archived_field").Count(&audits).Error; err != nil || audits != 1 {
		t.Fatalf("concurrent confirmation audit count=%d err=%v", audits, err)
	}
}

func TestFieldArchiveRetainsMetadataWithoutCurrentValue(t *testing.T) {
	s, actor, project := fieldArchiveFixture(t)
	values := mapsCloneWithout(project.Spec, "research_goal")
	raw, err := json.Marshal(values)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.testDB().Model(&projectRow{}).Where("id = ?", project.ID).Update("spec_json", string(raw)).Error; err != nil {
		t.Fatal(err)
	}
	project, err = s.GetProject(context.Background(), actor, project.ID)
	if err != nil {
		t.Fatal(err)
	}
	create, remove := archiveInputs(project, "research_goal")
	receipt, err := s.CreateFieldArchiveResult(context.Background(), actor, project.ID, "metadata-only-create", create)
	if err != nil {
		t.Fatal(err)
	}
	view, err := s.GetFieldArchive(context.Background(), actor, project.ID, receipt.Archive.ID)
	if err != nil || view.ValuePresent || !view.MetadataPresent || view.Metadata == nil || view.Metadata.Value != "old goal" {
		t.Fatalf("metadata-only history lost: %+v err=%v", view, err)
	}
	if _, err := s.RemoveArchivedFieldResult(context.Background(), actor, project.ID, receipt.Archive.ID, "metadata-only-remove", remove); err != nil {
		t.Fatalf("metadata-only removal failed: %v", err)
	}
	view, err = s.GetFieldArchive(context.Background(), actor, project.ID, receipt.Archive.ID)
	if err != nil || view.Metadata == nil || view.Metadata.Value != "old goal" {
		t.Fatalf("metadata was not retained after removal: %+v err=%v", view, err)
	}
}

func TestFieldArchivePreservesRawRowsAndHistoricalFieldID(t *testing.T) {
	s, actor, project := fieldArchiveFixture(t)
	values := map[string]string{"research_subject": "subject", "research_goal": "old goal", " legacy": "historical value"}
	raw, _, _, err := s.SaveSpec(context.Background(), actor, project.ID, "archive-legacy-key", SaveSpecInput{ExpectedSpecRevision: project.SpecRevision, Fields: values})
	if err != nil {
		t.Fatal(err)
	}
	project = asProject(t, raw)
	// Historical rows may have values without a persisted SpecField. The read
	// projection synthesizes metadata, which an archive must not invent.
	if err := s.testDB().Model(&projectRow{}).Where("id = ?", project.ID).Update("spec_metadata_json", `{}`).Error; err != nil {
		t.Fatal(err)
	}
	var before projectRow
	if err := s.testDB().Where("id = ?", project.ID).Take(&before).Error; err != nil {
		t.Fatal(err)
	}
	create, remove := archiveInputs(project, " legacy")
	receipt, err := s.CreateFieldArchiveResult(context.Background(), actor, project.ID, "archive-legacy-key-create", create)
	if err != nil {
		t.Fatal(err)
	}
	var after projectRow
	if err := s.testDB().Where("id = ?", project.ID).Take(&after).Error; err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("archive creation rewrote the project row: before=%+v after=%+v err=%v", before, after, err)
	}
	view, err := s.GetFieldArchive(context.Background(), actor, project.ID, receipt.Archive.ID)
	if err != nil || view.FieldID != " legacy" || view.Value != "historical value" || !view.ValuePresent || view.MetadataPresent || view.Metadata != nil {
		t.Fatalf("raw legacy field was changed or invented metadata: %+v err=%v", view, err)
	}
	if _, err := s.RemoveArchivedFieldResult(context.Background(), actor, project.ID, receipt.Archive.ID, "archive-legacy-key-remove", remove); err != nil {
		t.Fatal(err)
	}
	if err := s.testDB().Where("id = ?", project.ID).Take(&after).Error; err != nil {
		t.Fatal(err)
	}
	if after.SpecMetadataJSON != "{}" {
		t.Fatalf("removal invented unrelated metadata: %s", after.SpecMetadataJSON)
	}
}

func mapsCloneWithout(source map[string]string, key string) map[string]string {
	copy := make(map[string]string, len(source))
	for k, value := range source {
		if k != key {
			copy[k] = value
		}
	}
	return copy
}

func TestFieldArchiveProvenanceVisibilityIsRechecked(t *testing.T) {
	s, actor, project := fieldArchiveFixture(t)
	seedActualTurn(t, s, actor, project.ID, "archive-session", "archive-turn", "synthetic source value")
	raw, _, _, err := s.SaveSpec(context.Background(), actor, project.ID, "archive-sourced-spec", SaveSpecInput{
		ExpectedSpecRevision: project.SpecRevision, Fields: map[string]string{"research_subject": "subject", "research_goal": "synthetic source value"},
		FieldMetadata: map[string]SpecFieldInput{"research_goal": {Origin: "human", Status: "draft", Provenance: &ProvenanceRecord{SourceType: "agent_turn", SourceID: "archive-turn", SourceVersion: "1"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	project = asProject(t, raw)
	create, remove := archiveInputs(project, "research_goal")
	receipt, err := s.CreateFieldArchiveResult(context.Background(), actor, project.ID, "sourced-archive", create)
	if err != nil {
		t.Fatal(err)
	}
	view, err := s.GetFieldArchive(context.Background(), actor, project.ID, receipt.Archive.ID)
	if err != nil || view.Value != "synthetic source value" || view.Metadata == nil || view.Metadata.Provenance == nil || !view.Metadata.Provenance.Accessible {
		t.Fatalf("accessible source not visible: %+v err=%v", view, err)
	}
	if err := s.repository.Transaction(context.Background(), TransactionOptions{}, func(tx Transaction) error {
		store := tx.(ObjectStore)
		old, err := store.Object(project.ID, AgentTurnKind, "archive-turn")
		if err != nil {
			return err
		}
		var turn AgentTurn
		if err := json.Unmarshal(old.Payload, &turn); err != nil {
			return err
		}
		turn.Version++
		turn.Reply = "changed after archive"
		next := old
		next.Version++
		next.Payload, err = json.Marshal(turn)
		if err != nil {
			return err
		}
		return store.AppendObject(old, next)
	}); err != nil {
		t.Fatal(err)
	}
	view, err = s.GetFieldArchive(context.Background(), actor, project.ID, receipt.Archive.ID)
	if err != nil || !view.ValueRedacted || view.Value != "" || view.Metadata == nil || view.Metadata.Value != "" || view.Metadata.Provenance == nil || view.Metadata.Provenance.Locator != "" || !view.Metadata.Provenance.NeedsReview {
		t.Fatalf("revoked source leaked: %+v err=%v", view, err)
	}
	if _, err := s.RemoveArchivedFieldResult(context.Background(), actor, project.ID, receipt.Archive.ID, "sourced-remove-denied", remove); !errors.Is(err, ErrSourceUnavailable) {
		t.Fatalf("revoked source removal accepted: %v", err)
	}
	var stored fieldArchiveRecord
	if err := s.repository.Transaction(context.Background(), TransactionOptions{ReadOnly: true}, func(tx Transaction) error {
		_, err := tx.(projectRecordTransaction).GetProjectRecord(project.ID, fieldArchiveKind, receipt.Archive.ID, &stored)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if stored.Status != "recorded" || stored.Value != "synthetic source value" || stored.Metadata == nil || stored.Metadata.Provenance == nil || stored.Metadata.Provenance.Locator == "" {
		t.Fatalf("raw historical record was not preserved: %+v", stored)
	}
	if _, err := s.GetFieldArchive(context.Background(), Actor{TenantID: actor.TenantID, UserID: "outsider"}, project.ID, receipt.Archive.ID); err == nil {
		t.Fatal("outsider read archive")
	}
}

// Synthetic persisted agent objects and a fake source reader exercise real
// authorization loss. This fixture performs no model call.
func TestFieldArchiveHidesRevokedSourceAndBlocksRemoval(t *testing.T) {
	f := newAgentFixture(t, true)
	guardSource(t, f.svc, f.owner, f.project, "source-1", "knowledge-1")
	// The other fixture tables already exist, so apply the knowledge-base
	// statement from the production baseline and its later production change.
	base, err := os.ReadFile(filepath.Join("..", "..", "..", "migrations", "sqlite", "000000_init.up.sql"))
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, statement := range strings.Split(stripSQLLineComments(string(base)), ";") {
		statement = strings.TrimSpace(statement)
		if !strings.HasPrefix(statement, "CREATE TABLE IF NOT EXISTS "+"knowledge_bases (") {
			continue
		}
		if found {
			t.Fatal("duplicate knowledge base definition in production migration")
		}
		if err := f.svc.testDB().Exec(statement).Error; err != nil {
			t.Fatal(err)
		}
		found = true
	}
	if !found {
		t.Fatal("knowledge base definition missing from production migration")
	}
	additional, err := os.ReadFile(filepath.Join("..", "..", "..", "migrations", "sqlite", "000003_knowledge_base_auto_tag_config.up.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if err := f.svc.testDB().Exec(string(additional)).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.svc.testDB().Exec("INSERT INTO knowledge_bases(id,tenant_id,name,embedding_model_id,summary_model_id) VALUES(?,?,?,?,?)", "guard-kb", f.owner.TenantID, "synthetic archive source", "embedding-fixture", "summary-fixture").Error; err != nil {
		t.Fatal(err)
	}
	var knowledge types.Knowledge
	if err := f.svc.testDB().Where("id = ?", "knowledge-1").Take(&knowledge).Error; err != nil {
		t.Fatal(err)
	}
	processed := time.Time{}
	if knowledge.ProcessedAt != nil {
		processed = *knowledge.ProcessedAt
	}
	fingerprint := evidence.FingerprintOf(evidence.KnowledgeSignal{KnowledgeID: knowledge.ID, ParseStatus: knowledge.ParseStatus,
		FileHash: knowledge.FileHash, FileSize: knowledge.FileSize, ProcessedAt: processed})
	if err := f.svc.testDB().Create(&evidence.ProjectAssetRevision{ID: "archive-asset-revision", AssetID: "guard-asset-source-1", RevisionNo: 1,
		KnowledgeID: knowledge.ID, ContentHash: fingerprint.Digest, Status: evidence.RevisionActive}).Error; err != nil {
		t.Fatal(err)
	}
	source := f.source.sources["source-1"]
	source.AssetID = "guard-asset-source-1"
	f.source.sources["source-1"] = source
	seedActualTurn(t, f.svc, f.owner, f.project.ID, "source-session", "source-turn", "synthetic cited value")
	if err := f.svc.repository.Transaction(context.Background(), TransactionOptions{}, func(tx Transaction) error {
		store := tx.(ObjectStore)
		old, err := store.Object(f.project.ID, AgentTurnKind, "source-turn")
		if err != nil {
			return err
		}
		var turn AgentTurn
		if err := json.Unmarshal(old.Payload, &turn); err != nil {
			return err
		}
		turn.Version++
		turn.Context.SourceRefs = []AgentSourceRef{{SourceID: source.SourceID, AssetID: source.AssetID, AssetRevision: source.AssetRevision, QuotedTextHash: source.QuotedTextHash}}
		next := old
		next.Version++
		next.Payload, err = json.Marshal(turn)
		if err != nil {
			return err
		}
		return store.AppendObject(old, next)
	}); err != nil {
		t.Fatal(err)
	}
	raw, _, _, err := f.svc.SaveSpec(context.Background(), f.owner, f.project.ID, "source-backed-spec", SaveSpecInput{
		ExpectedSpecRevision: f.project.SpecRevision, Fields: map[string]string{"research_subject": "subject", "research_goal": "synthetic cited value"},
		FieldMetadata: map[string]SpecFieldInput{"research_goal": {Origin: "human", Status: "draft", Provenance: &ProvenanceRecord{SourceType: "agent_turn", SourceID: "source-turn", SourceVersion: "2"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	project := asProject(t, raw)
	create, remove := archiveInputs(project, "research_goal")
	result, err := f.svc.CreateFieldArchiveResult(context.Background(), f.owner, project.ID, "source-backed-archive", create)
	if err != nil {
		t.Fatal(err)
	}
	view, err := f.svc.GetFieldArchive(context.Background(), f.owner, project.ID, result.Archive.ID)
	if err != nil || view.Value != "synthetic cited value" || view.Metadata == nil || view.Metadata.Provenance == nil || !view.Metadata.Provenance.Accessible {
		t.Fatalf("source-backed archive was not visible: %+v err=%v", view, err)
	}
	f.source.denied["source-1"] = true
	view, err = f.svc.GetFieldArchive(context.Background(), f.owner, project.ID, result.Archive.ID)
	if err != nil || !view.ValueRedacted || view.Value != "" || view.Metadata == nil || view.Metadata.Value != "" || view.Metadata.Provenance == nil || view.Metadata.Provenance.Locator != "" {
		t.Fatalf("revoked source was exposed: %+v err=%v", view, err)
	}
	if _, err := f.svc.RemoveArchivedFieldResult(context.Background(), f.owner, project.ID, result.Archive.ID, "source-backed-remove", remove); !errors.Is(err, ErrSourceUnavailable) {
		t.Fatalf("revoked source did not block confirmation: %v", err)
	}
	var record fieldArchiveRecord
	if err := f.svc.repository.Transaction(context.Background(), TransactionOptions{ReadOnly: true}, func(tx Transaction) error {
		_, err := tx.(projectRecordTransaction).GetProjectRecord(project.ID, fieldArchiveKind, result.Archive.ID, &record)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if record.Status != "recorded" || record.Value != "synthetic cited value" || record.Metadata == nil || record.Metadata.Provenance == nil {
		t.Fatalf("revocation altered the durable archive: %+v", record)
	}
}
