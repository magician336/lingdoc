package workspacecore

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

// Persisted synthetic server objects exercise typed provenance resolution;
// these fixtures do not claim a live model run or human scientific review.
func seedActualTurn(t *testing.T, s *Service, a Actor, pid, sessionID, turnID, reply string) {
	t.Helper()
	err := s.repository.Transaction(context.Background(), TransactionOptions{}, func(tx Transaction) error {
		store := tx.(ObjectStore)
		session := AgentSession{ID: sessionID, ProjectID: pid, Version: 1, Agent: "research", CreatedBy: a.UserID, CreatedAt: time.Now().UTC()}
		turn := AgentTurn{ID: turnID, ProjectID: pid, SessionID: sessionID, Version: 1, Agent: "research", Reply: reply, Status: "completed", CreatedBy: a.UserID, CreatedAt: time.Now().UTC(), Context: AgentContextPackage{ProjectID: pid, SourceRefs: []AgentSourceRef{}}}
		for _, entry := range []struct {
			kind, id string
			value    any
		}{{AgentSessionKind, sessionID, session}, {AgentTurnKind, turnID, turn}} {
			raw, err := json.Marshal(entry.value)
			if err != nil {
				return err
			}
			if err = store.InsertObject(StoredObject{ProjectID: pid, Kind: entry.kind, ID: entry.id, Version: 1, Payload: raw}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestOriginUsesRealTurnAndServerCreator(t *testing.T) {
	s := testStore(t, filepath.Join(t.TempDir(), "origin.db"))
	a := Actor{TenantID: 891, UserID: "owner"}
	seedTenantMember(t, s, a)
	raw, _, _, err := s.CreateProject(context.Background(), a, "g1-origin-create", CreateProjectInput{Name: "synthetic", TemplateID: "template-demo"})
	if err != nil {
		t.Fatal(err)
	}
	p := asProject(t, raw)
	seedActualTurn(t, s, a, p.ID, "actual-session", "actual-turn", "真实合成回复")
	record := &ProvenanceRecord{SourceType: "agent_turn", SourceID: "actual-turn", SourceVersion: "1", CreatedBy: "forged-user", Accessible: false, Locator: "fake locator"}
	input := SaveSpecInput{ExpectedSpecRevision: 0, Fields: map[string]string{"research_subject": "真实合成回复"}, FieldMetadata: map[string]SpecFieldInput{"research_subject": {Origin: "human", Status: "draft", Provenance: record}}}
	raw, _, _, err = s.SaveSpec(context.Background(), a, p.ID, "g1-origin-spec1", input)
	if err != nil {
		t.Fatal(err)
	}
	p = asProject(t, raw)
	f := p.SpecFields["research_subject"]
	if f.Origin != "ai_generated" || f.Status != "pending_confirmation" || f.Provenance.CreatedBy != a.UserID || !f.Provenance.Accessible || f.Provenance.Locator != "reply" {
		t.Fatalf("server origin %+v", f)
	}
	input.ExpectedSpecRevision = p.SpecRevision
	input.Fields["research_subject"] = "人类修改合成回复"
	raw, _, _, err = s.SaveSpec(context.Background(), a, p.ID, "g1-origin-spec2", input)
	if err != nil {
		t.Fatal(err)
	}
	p = asProject(t, raw)
	if p.SpecFields["research_subject"].Origin != "ai_assisted_human" {
		t.Fatal(p.SpecFields)
	}
	input.ExpectedSpecRevision = p.SpecRevision
	input.FieldMetadata = nil
	input.Fields["research_subject"] = "纯人工改写"
	raw, _, _, err = s.SaveSpec(context.Background(), a, p.ID, "g1-origin-spec3", input)
	if err != nil {
		t.Fatal(err)
	}
	p = asProject(t, raw)
	if f := p.SpecFields["research_subject"]; f.Origin != "human" || f.Provenance != nil {
		t.Fatalf("manual metadata retained AI %+v", f)
	}
}

func TestUnknownLegacySourceIsRetainedHiddenAndCannotActivate(t *testing.T) {
	s := testStore(t, filepath.Join(t.TempDir(), "unknown.db"))
	a := Actor{TenantID: 892, UserID: "owner"}
	seedTenantMember(t, s, a)
	raw, _, _, err := s.CreateProject(context.Background(), a, "g1-unknown-create", CreateProjectInput{Name: "synthetic", TemplateID: "template-demo"})
	if err != nil {
		t.Fatal(err)
	}
	p := asProject(t, raw)
	record := &ProvenanceRecord{SourceType: "conversation", SourceID: "missing-session", SourceVersion: "missing-turn", CreatedBy: "forged", Accessible: true, Locator: "secret locator", Purpose: "secret purpose", AuthorizationScope: "secret scope"}
	input := SaveSpecInput{ExpectedSpecRevision: 0, Fields: map[string]string{"research_subject": "secret field", "research_goal": "human goal"}, FieldMetadata: map[string]SpecFieldInput{"research_subject": {Origin: "ai_generated", Status: "pending_confirmation", Provenance: record}}}
	raw, _, _, err = s.SaveSpec(context.Background(), a, p.ID, "g1-unknown-spec", input)
	if err != nil {
		t.Fatal(err)
	}
	visible := asProject(t, raw)
	f := visible.SpecFields["research_subject"]
	if f.Value != "" || visible.Spec["research_subject"] != "" || f.Provenance.Accessible || f.Provenance.CreatedBy != a.UserID || f.Provenance.Locator != "" || f.Provenance.Purpose != "" || f.Provenance.AuthorizationScope != "" {
		t.Fatalf("unverified leaked %+v", f)
	}
	if err = s.repository.Transaction(context.Background(), TransactionOptions{ReadOnly: true}, func(tx Transaction) error {
		raw, err := tx.Project(a.TenantID, p.ID)
		if err == nil && raw.Spec["research_subject"] != "secret field" {
			t.Fatal("raw source-derived field was erased")
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	replay, _, replayed, err := s.SaveSpec(context.Background(), a, p.ID, "g1-unknown-spec", input)
	if err != nil || !replayed || asProject(t, replay).Spec["research_subject"] != "" {
		t.Fatalf("unsafe replay %s %v", replay, err)
	}
	diff, err := s.ActivationDiff(context.Background(), a, p.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range diff.ChangedFields {
		if field.Key == "research_subject" && field.Value != "" {
			t.Fatal("diff leaked field")
		}
	}
	_, _, _, err = s.ActivateProject(context.Background(), a, p.ID, "g1-unknown-activate", ActivateProjectInput{ExpectedSpecRevision: visible.SpecRevision, ExpectedProjectVersion: visible.ProjectVersion, ReviewedProjectVersion: visible.ProjectVersion})
	if !errors.Is(err, ErrSourceUnavailable) {
		t.Fatalf("unknown source activated: %v", err)
	}
}

func TestTypedTurnRejectsCrossProjectAndVersionMutation(t *testing.T) {
	s := testStore(t, filepath.Join(t.TempDir(), "version.db"))
	a := Actor{TenantID: 893, UserID: "owner"}
	seedTenantMember(t, s, a)
	create := func(key string) Project {
		raw, _, _, err := s.CreateProject(context.Background(), a, key, CreateProjectInput{Name: "synthetic", TemplateID: "template-demo"})
		if err != nil {
			t.Fatal(err)
		}
		return asProject(t, raw)
	}
	p := create("g1-vers-project1")
	other := create("g1-vers-project2")
	seedActualTurn(t, s, a, p.ID, "s1", "t1", "versioned reply")
	record := &ProvenanceRecord{SourceType: "agent_turn", SourceID: "t1", SourceVersion: "1", Accessible: true}
	input := SaveSpecInput{ExpectedSpecRevision: 0, Fields: map[string]string{"research_subject": "versioned reply"}, FieldMetadata: map[string]SpecFieldInput{"research_subject": {Origin: "ai_generated", Status: "pending_confirmation", Provenance: record}}}
	raw, _, _, err := s.SaveSpec(context.Background(), a, other.ID, "g1-vers-cross-spec", input)
	if err != nil {
		t.Fatal(err)
	}
	if asProject(t, raw).Spec["research_subject"] != "" {
		t.Fatal("cross project source visible")
	}
	_, _, _, err = s.SaveSpec(context.Background(), a, p.ID, "g1-vers-spec", input)
	if err != nil {
		t.Fatal(err)
	}
	err = s.repository.Transaction(context.Background(), TransactionOptions{}, func(tx Transaction) error {
		store := tx.(ObjectStore)
		old, err := store.Object(p.ID, AgentTurnKind, "t1")
		if err != nil {
			return err
		}
		var turn AgentTurn
		if err = json.Unmarshal(old.Payload, &turn); err != nil {
			return err
		}
		turn.Version++
		turn.Reply = "changed"
		next := old
		next.Version++
		next.Payload, _ = json.Marshal(turn)
		return store.AppendObject(old, next)
	})
	if err != nil {
		t.Fatal(err)
	}
	current, err := s.GetProject(context.Background(), a, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.Spec["research_subject"] != "" || !current.SpecFields["research_subject"].Provenance.NeedsReview {
		t.Fatal("stale turn exposed")
	}
	raw, _, replayed, err := s.SaveSpec(context.Background(), a, p.ID, "g1-vers-spec", input)
	if err != nil || !replayed || asProject(t, raw).Spec["research_subject"] != "" {
		t.Fatalf("stale source replay: %s %v", raw, err)
	}
}
