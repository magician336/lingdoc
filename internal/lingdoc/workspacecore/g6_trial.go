package workspacecore

import (
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"math"
	"slices"
	"strings"
	"time"
)

type TrialWindow struct {
	ID                 string            `json:"id"`
	ProjectID          string            `json:"project_id"`
	Version            int64             `json:"version"`
	Status             string            `json:"status"`
	StartsAt           time.Time         `json:"starts_at"`
	EndsAt             time.Time         `json:"ends_at"`
	IncludedProjectIDs []string          `json:"included_project_ids"`
	IncludedUserIDs    []string          `json:"included_user_ids"`
	DecisionOwners     map[string]string `json:"decision_owners"`
	TargetSamples      int               `json:"target_samples"`
	MinimumSamples     int               `json:"minimum_samples"`
	TemplateVersion    string            `json:"template_version"`
	RulesetHash        string            `json:"ruleset_hash"`
	RuntimeModes       []string          `json:"runtime_modes"`
	HumanSupport       string            `json:"human_support"`
	Redaction          string            `json:"redaction"`
	ExitConditions     []string          `json:"exit_conditions"`
	CreatedBy          string            `json:"created_by"`
	CreatedAt          time.Time         `json:"created_at"`
}
type TrialWindowActionInput struct {
	ExpectedVersion int64  `json:"expected_version"`
	Action          string `json:"action"`
	Reason          string `json:"reason"`
}
type MetricObservation struct {
	Name        string   `json:"name"`
	Numerator   *float64 `json:"numerator"`
	Denominator *float64 `json:"denominator"`
	Missing     bool     `json:"missing"`
	Version     string   `json:"version"`
}
type TrialSample struct {
	ID                    string              `json:"id"`
	WindowID              string              `json:"window_id"`
	SampleVersion         string              `json:"sample_version"`
	FixtureDigest         string              `json:"fixture_digest"`
	TenantAlias           string              `json:"tenant_alias"`
	ProjectID             string              `json:"project_id"`
	TemplateVersion       string              `json:"template_version"`
	RulesetHash           string              `json:"ruleset_hash"`
	ContextRevision       int64               `json:"context_revision"`
	RuntimeMode           string              `json:"runtime_mode"`
	DependencyVersions    map[string]string   `json:"dependency_versions"`
	PermissionSnapshot    string              `json:"permission_snapshot"`
	InputHash             string              `json:"input_hash"`
	OutputHash            string              `json:"output_hash"`
	ExecutedAt            time.Time           `json:"executed_at"`
	ExecutorID            string              `json:"executor_id"`
	ReviewerID            string              `json:"reviewer_id,omitempty"`
	CorrelationID         string              `json:"correlation_id"`
	RuntimeEventIDs       []string            `json:"runtime_event_ids"`
	ProcessEvidence       map[string]string   `json:"process_evidence"`
	QualityResult         string              `json:"quality_result"`
	Severity              string              `json:"severity"`
	HumanDecision         string              `json:"human_decision,omitempty"`
	ReviewReason          string              `json:"review_reason,omitempty"`
	WordRoundtripEvidence string              `json:"word_roundtrip_evidence,omitempty"`
	RepairOwnerID         string              `json:"repair_owner_id,omitempty"`
	RepairVersion         string              `json:"repair_version,omitempty"`
	RerunEvidence         string              `json:"rerun_evidence,omitempty"`
	Metrics               []MetricObservation `json:"metrics"`
	Version               int64               `json:"version"`
	ReviewedAt            *time.Time          `json:"reviewed_at,omitempty"`
}
type TrialReviewInput struct {
	ExpectedVersion       int64  `json:"expected_version"`
	QualityResult         string `json:"quality_result"`
	HumanDecision         string `json:"human_decision"`
	Reason                string `json:"reason"`
	WordRoundtripEvidence string `json:"word_roundtrip_evidence"`
	RepairOwnerID         string `json:"repair_owner_id,omitempty"`
	RepairVersion         string `json:"repair_version,omitempty"`
	RerunEvidence         string `json:"rerun_evidence,omitempty"`
}
type TrialMetricResult struct {
	Name           string   `json:"name"`
	Numerator      float64  `json:"numerator"`
	Denominator    float64  `json:"denominator"`
	Value          *float64 `json:"value"`
	MissingSamples []string `json:"missing_samples"`
}
type TrialReport struct {
	WindowID      string              `json:"window_id"`
	WindowVersion int64               `json:"window_version"`
	Readiness     string              `json:"readiness"`
	Submitted     int                 `json:"submitted"`
	Eligible      int                 `json:"eligible"`
	Excluded      map[string][]string `json:"excluded"`
	Metrics       []TrialMetricResult `json:"metrics"`
	Blockers      []string            `json:"blockers"`
	InputHash     string              `json:"input_hash"`
}
type TrialDecisionRecord struct {
	ID         string            `json:"id"`
	WindowID   string            `json:"window_id"`
	Report     TrialReport       `json:"report"`
	Decision   string            `json:"decision"`
	Reason     string            `json:"reason"`
	Signatures map[string]string `json:"signatures"`
	Status     string            `json:"status"`
	Version    int64             `json:"version"`
	DecidedAt  *time.Time        `json:"decided_at,omitempty"`
	CreatedAt  time.Time         `json:"created_at"`
}
type TrialDecisionInput struct {
	Decision   string `json:"decision"`
	Reason     string `json:"reason"`
	ReportHash string `json:"report_hash"`
}
type TrialDecisionSignInput struct {
	ExpectedVersion int64  `json:"expected_version"`
	Responsibility  string `json:"responsibility"`
	Reason          string `json:"reason"`
}

var trialMetricNames = []string{"manual_effort_minutes", "source_support_rate", "missed_edits", "rule_false_positive_rate", "model_false_positive_rate", "unconfirmed_items", "export_loss_items"}
var trialProcessKeys = []string{"authorization", "main_path", "failure_path", "manual_review", "repair_or_rollback", "key_scenario_rerun"}

func safeEvidenceRef(value string) bool {
	return value != "" && runtimeSafeCode.MatchString(value) && !strings.Contains(strings.ToLower(value), "bearer")
}
func validTrialWindow(input TrialWindow) bool {
	if len(input.DecisionOwners) != 3 {
		return false
	}
	for _, role := range []string{"product", "qa", "safety"} {
		if input.DecisionOwners[role] == "" {
			return false
		}
	}
	return !input.StartsAt.IsZero() && input.EndsAt.After(input.StartsAt) && input.TargetSamples > 0 && input.MinimumSamples > 0 && input.MinimumSamples <= input.TargetSamples && len(input.IncludedProjectIDs) > 0 && len(input.IncludedUserIDs) > 0 && input.TemplateVersion != "" && input.RulesetHash != "" && len(input.RuntimeModes) == 1 && input.RuntimeModes[0] == "real" && safeEvidenceRef(input.HumanSupport) && safeEvidenceRef(input.Redaction) && len(input.ExitConditions) > 0
}
func (s *Service) CreateTrialWindow(ctx context.Context, actor Actor, projectID, key string, input TrialWindow) (json.RawMessage, int, bool, error) {
	if err := RequireHumanDecision(ctx); err != nil {
		return nil, 0, false, err
	}
	if !validTrialWindow(input) {
		return nil, 0, false, ErrInvalidRequest
	}
	return s.operation(ctx, actor, "trialCreateWindow", projectID, key, input, projectID, "manage", func(tx Transaction, p Project) (any, int, error) {
		store, ok := tx.(runtimeTransaction)
		if !ok {
			return nil, 0, ErrInvalidState
		}
		if !slices.Contains(input.IncludedProjectIDs, projectID) {
			return nil, 0, ErrInvalidRequest
		}
		seen := map[string]bool{}
		for _, id := range input.IncludedProjectIDs {
			if seen[id] {
				return nil, 0, ErrInvalidRequest
			}
			seen[id] = true
			memberProject, err := s.authorizeProject(ctx, tx, actor, id, "manage")
			if err != nil {
				return nil, 0, err
			}
			if memberProject.TemplateVersion != input.TemplateVersion || memberProject.TemplateCopy == nil || memberProject.TemplateCopy.RulesetHash != input.RulesetHash {
				return nil, 0, ErrVersionConflict
			}
		}
		for _, id := range input.IncludedUserIDs {
			active, err := tx.ActiveMember(Actor{TenantID: actor.TenantID, UserID: id})
			if err != nil {
				return nil, 0, err
			}
			if !active {
				return nil, 0, ErrNotFound
			}
		}
		for _, responsibleID := range input.DecisionOwners {
			responsible := actor
			responsible.UserID = responsibleID
			if _, err := s.authorizeProject(ctx, tx, responsible, projectID, "read"); err != nil {
				return nil, 0, err
			}
		}
		input.ID = uuid.NewString()
		input.ProjectID = projectID
		input.Status = "DRAFT"
		input.Version = 1
		input.CreatedBy = actor.UserID
		input.CreatedAt = time.Now().UTC()
		if err := store.G6Insert(actor.TenantID, projectID, "trial_window", input.ID, 1, input); err != nil {
			return nil, 0, err
		}
		if err := g3Audit(tx, actor, projectID, "trial.window.create", input.ID, map[string]any{"version": 1, "status": "DRAFT"}); err != nil {
			return nil, 0, err
		}
		return input, 201, nil
	})
}
func (s *Service) ActOnTrialWindow(ctx context.Context, actor Actor, projectID, windowID, key string, input TrialWindowActionInput) (json.RawMessage, int, bool, error) {
	if err := RequireHumanDecision(ctx); err != nil {
		return nil, 0, false, err
	}
	if input.ExpectedVersion < 1 || !slices.Contains([]string{"lock", "pause", "close"}, input.Action) || strings.TrimSpace(input.Reason) == "" {
		return nil, 0, false, ErrInvalidRequest
	}
	return s.operation(ctx, actor, "trialWindowAction", projectID+"/"+windowID, key, input, projectID, "manage", func(tx Transaction, p Project) (any, int, error) {
		store, ok := tx.(runtimeTransaction)
		if !ok {
			return nil, 0, ErrInvalidState
		}
		var old TrialWindow
		version, err := store.G6Read(actor.TenantID, projectID, "trial_window", windowID, &old)
		if err != nil {
			return nil, 0, err
		}
		if version != input.ExpectedVersion {
			return nil, 0, ErrVersionConflict
		}
		if old.Status == "CLOSED" {
			return nil, 0, ErrInvalidState
		}
		next := old
		switch input.Action {
		case "lock":
			if old.Status != "DRAFT" || !validTrialWindow(old) {
				return nil, 0, ErrInvalidState
			}
			next.Status = "LOCKED"
		case "pause":
			if old.Status != "LOCKED" {
				return nil, 0, ErrInvalidState
			}
			next.Status = "PAUSED"
		case "close":
			next.Status = "CLOSED"
		}
		next.Version++
		if err := store.G6CAS(actor.TenantID, projectID, "trial_window", windowID, version, next); err != nil {
			return nil, 0, err
		}
		if err := g3Audit(tx, actor, projectID, "trial.window."+input.Action, windowID, map[string]any{"version": next.Version, "reason": input.Reason}); err != nil {
			return nil, 0, err
		}
		return next, 200, nil
	})
}
func (s *Service) ListTrialWindows(ctx context.Context, actor Actor, projectID string) ([]TrialWindow, error) {
	var result []TrialWindow
	err := s.repository.Transaction(ctx, TransactionOptions{ReadOnly: true}, func(tx Transaction) error {
		if _, err := s.authorizeProject(ctx, tx, actor, projectID, "read"); err != nil {
			return err
		}
		store, ok := tx.(runtimeTransaction)
		if !ok {
			return ErrInvalidState
		}
		rows, err := store.G6List(actor.TenantID, projectID, "trial_window")
		if err != nil {
			return err
		}
		result = []TrialWindow{}
		for _, row := range rows {
			var value TrialWindow
			if err := json.Unmarshal([]byte(row.BodyJSON), &value); err != nil {
				return err
			}
			result = append(result, value)
		}
		return nil
	})
	return result, err
}
func validTrialSample(sample TrialSample) bool {
	if !safeEvidenceRef(sample.SampleVersion) || !safeEvidenceRef(sample.FixtureDigest) || !safeEvidenceRef(sample.InputHash) || !safeEvidenceRef(sample.OutputHash) || !safeEvidenceRef(sample.PermissionSnapshot) || sample.ExecutedAt.IsZero() || !validRuntimeMode(sample.RuntimeMode) || !safeEvidenceRef(sample.CorrelationID) || !slices.Contains([]string{"P0", "P1", "P2", "P3", ""}, sample.Severity) {
		return false
	}
	seen := map[string]bool{}
	for _, metric := range sample.Metrics {
		if !slices.Contains(trialMetricNames, metric.Name) || seen[metric.Name] || !safeEvidenceRef(metric.Version) {
			return false
		}
		seen[metric.Name] = true
		for _, value := range []*float64{metric.Numerator, metric.Denominator} {
			if value != nil && (math.IsNaN(*value) || math.IsInf(*value, 0) || *value < 0) {
				return false
			}
		}
	}
	for key, value := range sample.ProcessEvidence {
		if !slices.Contains(trialProcessKeys, key) || !safeEvidenceRef(value) {
			return false
		}
	}
	for name, version := range sample.DependencyVersions {
		if !safeEvidenceRef(name) || !safeEvidenceRef(version) {
			return false
		}
	}
	return true
}
func (s *Service) RecordTrialSample(ctx context.Context, actor Actor, projectID, windowID, key string, input TrialSample) (json.RawMessage, int, bool, error) {
	if !validTrialSample(input) {
		return nil, 0, false, ErrInvalidRequest
	}
	return s.operation(ctx, actor, "trialRecordSample", projectID+"/"+windowID, key, input, projectID, "write", func(tx Transaction, p Project) (any, int, error) {
		store, ok := tx.(runtimeTransaction)
		if !ok {
			return nil, 0, ErrInvalidState
		}
		var window TrialWindow
		if _, err := store.G6Read(actor.TenantID, projectID, "trial_window", windowID, &window); err != nil {
			return nil, 0, err
		}
		if window.Status != "LOCKED" {
			return nil, 0, ErrInvalidState
		}
		if !slices.Contains(window.IncludedProjectIDs, input.ProjectID) || !slices.Contains(window.IncludedUserIDs, actor.UserID) || input.ExecutedAt.Before(window.StartsAt) || input.ExecutedAt.After(window.EndsAt) || input.ExecutedAt.After(time.Now().UTC()) {
			return nil, 0, ErrInvalidRequest
		}
		sampleProject, err := s.authorizeProject(ctx, tx, actor, input.ProjectID, "read")
		if err != nil {
			return nil, 0, err
		}
		if input.TemplateVersion != window.TemplateVersion || input.RulesetHash != window.RulesetHash || input.ContextRevision != sampleProject.CurrentContextRevision || sampleProject.TemplateVersion != input.TemplateVersion || sampleProject.TemplateCopy == nil || sampleProject.TemplateCopy.RulesetHash != input.RulesetHash {
			return nil, 0, ErrVersionConflict
		}
		if input.QualityResult != "" && input.QualityResult != "NOT RUN" {
			return nil, 0, ErrInvalidRequest
		}
		input.ID = uuid.NewString()
		input.WindowID = windowID
		input.TenantAlias = "tenant." + hashImpactValue(actor.TenantID)[:16]
		input.ExecutorID = actor.UserID
		input.ReviewerID = ""
		input.QualityResult = "NOT RUN"
		input.HumanDecision = ""
		input.ReviewReason = ""
		input.ReviewedAt = nil
		input.Version = 1
		input.WordRoundtripEvidence = ""
		input.RepairOwnerID = ""
		input.RepairVersion = ""
		input.RerunEvidence = ""
		sampleKey := windowID + "/" + input.FixtureDigest + "/" + input.SampleVersion
		var previous TrialSample
		if _, err := store.G6Read(actor.TenantID, projectID, "trial_sample", sampleKey, &previous); err == nil {
			return nil, 0, ErrVersionConflict
		} else if err != ErrNotFound {
			return nil, 0, err
		}
		if err := store.G6Insert(actor.TenantID, projectID, "trial_sample", sampleKey, 1, input); err != nil {
			return nil, 0, err
		}
		if err := g3Audit(tx, actor, projectID, "trial.sample.record", input.ID, map[string]any{"sample_version": input.SampleVersion, "quality_result": "NOT RUN"}); err != nil {
			return nil, 0, err
		}
		return input, 201, nil
	})
}
func trialSamples(tx Transaction, tenant uint64, project, windowID string) ([]TrialSample, error) {
	store, ok := tx.(runtimeTransaction)
	if !ok {
		return nil, ErrInvalidState
	}
	rows, err := store.G6List(tenant, project, "trial_sample")
	if err != nil {
		return nil, err
	}
	result := []TrialSample{}
	for _, row := range rows {
		var value TrialSample
		if err := json.Unmarshal([]byte(row.BodyJSON), &value); err != nil {
			return nil, err
		}
		if value.WindowID == windowID {
			result = append(result, value)
		}
	}
	return result, nil
}
func (s *Service) ListTrialSamples(ctx context.Context, actor Actor, projectID, windowID string) ([]TrialSample, error) {
	var result []TrialSample
	err := s.repository.Transaction(ctx, TransactionOptions{ReadOnly: true}, func(tx Transaction) error {
		if _, err := s.authorizeProject(ctx, tx, actor, projectID, "read"); err != nil {
			return err
		}
		var err error
		result, err = trialSamples(tx, actor.TenantID, projectID, windowID)
		return err
	})
	return result, err
}
func (s *Service) ReviewTrialSample(ctx context.Context, actor Actor, projectID, windowID, sampleID, key string, input TrialReviewInput) (json.RawMessage, int, bool, error) {
	if err := RequireHumanDecision(ctx); err != nil {
		return nil, 0, false, err
	}
	if input.ExpectedVersion < 1 || !slices.Contains([]string{"PASS", "FAIL", "BLOCKED", "NOT RUN"}, input.QualityResult) || !slices.Contains([]string{"accept", "reject", "needs_work"}, input.HumanDecision) || strings.TrimSpace(input.Reason) == "" || !safeEvidenceRef(input.WordRoundtripEvidence) {
		return nil, 0, false, ErrInvalidRequest
	}
	return s.operation(ctx, actor, "trialReviewSample", projectID+"/"+sampleID, key, input, projectID, "manage", func(tx Transaction, p Project) (any, int, error) {
		samples, err := trialSamples(tx, actor.TenantID, projectID, windowID)
		if err != nil {
			return nil, 0, err
		}
		for _, old := range samples {
			if old.ID != sampleID {
				continue
			}
			if old.Version != input.ExpectedVersion {
				return nil, 0, ErrVersionConflict
			}
			if old.ReviewedAt != nil {
				return nil, 0, ErrInvalidState
			}
			if input.QualityResult == "PASS" && input.HumanDecision != "accept" {
				return nil, 0, ErrInvalidRequest
			}
			next := old
			next.Version++
			next.QualityResult = input.QualityResult
			next.HumanDecision = input.HumanDecision
			next.ReviewerID = actor.UserID
			next.ReviewReason = input.Reason
			next.WordRoundtripEvidence = input.WordRoundtripEvidence
			next.RepairOwnerID = input.RepairOwnerID
			next.RepairVersion = input.RepairVersion
			next.RerunEvidence = input.RerunEvidence
			now := time.Now().UTC()
			next.ReviewedAt = &now
			store := tx.(runtimeTransaction)
			if err := store.G6CAS(actor.TenantID, projectID, "trial_sample", windowID+"/"+old.FixtureDigest+"/"+old.SampleVersion, old.Version, next); err != nil {
				return nil, 0, err
			}
			if err := g3Audit(tx, actor, projectID, "trial.sample.review", sampleID, map[string]any{"quality_result": next.QualityResult, "human_decision": next.HumanDecision, "reviewer": actor.UserID, "word_evidence": next.WordRoundtripEvidence}); err != nil {
				return nil, 0, err
			}
			return next, 200, nil
		}
		return nil, 0, ErrNotFound
	})
}
func buildTrialReport(tx Transaction, actor Actor, projectID, windowID string) (TrialReport, error) {
	store, ok := tx.(runtimeTransaction)
	if !ok {
		return TrialReport{}, ErrInvalidState
	}
	var window TrialWindow
	if _, err := store.G6Read(actor.TenantID, projectID, "trial_window", windowID, &window); err != nil {
		return TrialReport{}, err
	}
	samples, err := trialSamples(tx, actor.TenantID, projectID, windowID)
	if err != nil {
		return TrialReport{}, err
	}
	result := TrialReport{WindowID: windowID, WindowVersion: window.Version, Readiness: "NOT READY", Submitted: len(samples), Excluded: map[string][]string{}, Metrics: []TrialMetricResult{}, Blockers: []string{}}
	eligible := []TrialSample{}
	for _, sample := range samples {
		reason := ""
		if sample.RuntimeMode != "real" {
			reason = "non_real"
		} else if sample.Severity == "P0" || sample.Severity == "P1" {
			reason = "p0_p1"
			result.Blockers = append(result.Blockers, "p0_p1:"+sample.ID)
		} else if sample.QualityResult != "PASS" || sample.HumanDecision != "accept" || sample.ReviewerID == "" || sample.ReviewedAt == nil {
			reason = "human_review_missing"
		} else if sample.Severity == "P2" && (sample.RepairOwnerID == "" || !safeEvidenceRef(sample.RepairVersion) || !safeEvidenceRef(sample.RerunEvidence)) {
			reason = "p2_remedy_missing"
			result.Blockers = append(result.Blockers, reason+":"+sample.ID)
		}
		if reason == "" {
			for _, key := range trialProcessKeys {
				if !safeEvidenceRef(sample.ProcessEvidence[key]) {
					reason = "process_evidence_missing"
				}
			}
		}
		if reason == "" && (len(sample.DependencyVersions) == 0 || len(sample.RuntimeEventIDs) == 0 || sample.WordRoundtripEvidence == "") {
			reason = "real_evidence_missing"
		}
		if reason == "" {
			categories := map[string]bool{}
			for _, id := range sample.RuntimeEventIDs {
				var event RuntimeEvent
				if _, err := store.G6Read(actor.TenantID, sample.ProjectID, "event", id, &event); err != nil {
					reason = "runtime_event_missing"
					break
				}
				if event.CorrelationID != sample.CorrelationID || event.RuntimeMode != "real" || event.ContextRevision != sample.ContextRevision || event.TemplateVersion != sample.TemplateVersion || event.RulesetHash != sample.RulesetHash || event.CreatedAt.Before(window.StartsAt) || event.CreatedAt.After(window.EndsAt) || event.Origin != "runtime" {
					reason = "runtime_evidence_mismatch"
					break
				}
				if event.Status == "success" {
					categories[event.Category] = true
				}
			}
			for _, category := range []string{"request", "dependency", "preflight", "export", "download"} {
				if !categories[category] {
					reason = "runtime_chain_incomplete"
				}
			}
		}
		if reason != "" {
			result.Excluded[reason] = append(result.Excluded[reason], sample.ID)
			if sample.RuntimeMode == "real" && strings.Contains(reason, "runtime") {
				result.Blockers = append(result.Blockers, reason+":"+sample.ID)
			}
			continue
		}
		eligible = append(eligible, sample)
	}
	result.Eligible = len(eligible)
	for _, name := range trialMetricNames {
		metric := TrialMetricResult{Name: name, MissingSamples: []string{}}
		for _, sample := range eligible {
			found := false
			for _, observation := range sample.Metrics {
				if observation.Name != name {
					continue
				}
				found = true
				if observation.Missing || observation.Numerator == nil || observation.Denominator == nil || *observation.Denominator <= 0 {
					metric.MissingSamples = append(metric.MissingSamples, sample.ID)
				} else {
					metric.Numerator += *observation.Numerator
					metric.Denominator += *observation.Denominator
				}
			}
			if !found {
				metric.MissingSamples = append(metric.MissingSamples, sample.ID)
			}
		}
		if metric.Denominator > 0 && len(metric.MissingSamples) == 0 && result.Eligible >= window.MinimumSamples {
			value := metric.Numerator / metric.Denominator
			if slices.Contains([]string{"missed_edits", "unconfirmed_items", "export_loss_items"}, name) {
				value = metric.Numerator
			}
			metric.Value = &value
		}
		result.Metrics = append(result.Metrics, metric)
	}
	if len(result.Blockers) > 0 {
		result.Readiness = "BLOCKED"
	} else if result.Eligible >= window.MinimumSamples && result.Eligible >= window.TargetSamples {
		ready := true
		for _, metric := range result.Metrics {
			if metric.Value == nil {
				ready = false
			}
		}
		if ready {
			result.Readiness = "READY"
		}
	}
	if window.Status == "DRAFT" || window.Status == "PAUSED" {
		result.Readiness = "NOT READY"
	}
	result.InputHash = hashImpactValue(struct {
		Window    TrialWindow
		Samples   []TrialSample
		Readiness string
	}{window, samples, result.Readiness})
	return result, nil
}
func (s *Service) GetTrialReport(ctx context.Context, actor Actor, projectID, windowID string) (TrialReport, error) {
	var result TrialReport
	err := s.repository.Transaction(ctx, TransactionOptions{ReadOnly: true}, func(tx Transaction) error {
		if _, err := s.authorizeProject(ctx, tx, actor, projectID, "read"); err != nil {
			return err
		}
		var err error
		result, err = buildTrialReport(tx, actor, projectID, windowID)
		return err
	})
	return result, err
}
func (s *Service) CreateTrialDecision(ctx context.Context, actor Actor, projectID, windowID, key string, input TrialDecisionInput) (json.RawMessage, int, bool, error) {
	if err := RequireHumanDecision(ctx); err != nil {
		return nil, 0, false, err
	}
	if !slices.Contains([]string{"expand", "continue", "pause"}, input.Decision) || strings.TrimSpace(input.Reason) == "" || input.ReportHash == "" {
		return nil, 0, false, ErrInvalidRequest
	}
	return s.operation(ctx, actor, "trialCreateDecision", projectID+"/"+windowID, key, input, projectID, "manage", func(tx Transaction, p Project) (any, int, error) {
		report, err := buildTrialReport(tx, actor, projectID, windowID)
		if err != nil {
			return nil, 0, err
		}
		if input.ReportHash != report.InputHash {
			return nil, 0, ErrVersionConflict
		}
		if input.Decision == "expand" && report.Readiness != "READY" {
			return nil, 0, ErrInvalidState
		}
		if input.Decision == "continue" && report.Readiness == "BLOCKED" {
			return nil, 0, ErrInvalidState
		}
		record := TrialDecisionRecord{ID: uuid.NewString(), WindowID: windowID, Report: report, Decision: input.Decision, Reason: input.Reason, Signatures: map[string]string{}, Status: "WAITING_HUMAN", Version: 1, CreatedAt: time.Now().UTC()}
		if err := tx.(runtimeTransaction).G6Insert(actor.TenantID, projectID, "trial_decision", record.ID, 1, record); err != nil {
			return nil, 0, err
		}
		if err := g3Audit(tx, actor, projectID, "trial.decision.propose", record.ID, map[string]any{"decision": input.Decision, "report_hash": report.InputHash}); err != nil {
			return nil, 0, err
		}
		return record, 201, nil
	})
}
func (s *Service) SignTrialDecision(ctx context.Context, actor Actor, projectID, decisionID, key string, input TrialDecisionSignInput) (json.RawMessage, int, bool, error) {
	if err := RequireHumanDecision(ctx); err != nil {
		return nil, 0, false, err
	}
	if input.ExpectedVersion < 1 || !slices.Contains([]string{"product", "qa", "safety"}, input.Responsibility) || strings.TrimSpace(input.Reason) == "" {
		return nil, 0, false, ErrInvalidRequest
	}
	return s.operation(ctx, actor, "trialSignDecision", projectID+"/"+decisionID, key, input, projectID, "manage", func(tx Transaction, p Project) (any, int, error) {
		store, ok := tx.(runtimeTransaction)
		if !ok {
			return nil, 0, ErrInvalidState
		}
		var old TrialDecisionRecord
		version, err := store.G6Read(actor.TenantID, projectID, "trial_decision", decisionID, &old)
		if err != nil {
			return nil, 0, err
		}
		if version != input.ExpectedVersion {
			return nil, 0, ErrVersionConflict
		}
		if old.Status != "WAITING_HUMAN" || old.Signatures[input.Responsibility] != "" {
			return nil, 0, ErrInvalidState
		}
		var lockedWindow TrialWindow
		if _, err := store.G6Read(actor.TenantID, projectID, "trial_window", old.WindowID, &lockedWindow); err != nil {
			return nil, 0, err
		}
		if lockedWindow.DecisionOwners[input.Responsibility] != actor.UserID {
			return nil, 0, ErrNotFound
		}
		report, err := buildTrialReport(tx, actor, projectID, old.WindowID)
		if err != nil {
			return nil, 0, err
		}
		if report.InputHash != old.Report.InputHash {
			return nil, 0, ErrVersionConflict
		}
		next := old
		next.Signatures = map[string]string{}
		for role, id := range old.Signatures {
			next.Signatures[role] = id
		}
		next.Signatures[input.Responsibility] = actor.UserID
		next.Version++
		if len(next.Signatures) == 3 {
			next.Status = "DECIDED"
			now := time.Now().UTC()
			next.DecidedAt = &now
			if next.Decision == "pause" {
				var window TrialWindow
				windowVersion, err := store.G6Read(actor.TenantID, projectID, "trial_window", next.WindowID, &window)
				if err != nil {
					return nil, 0, err
				}
				window.Status = "PAUSED"
				window.Version++
				if err := store.G6CAS(actor.TenantID, projectID, "trial_window", next.WindowID, windowVersion, window); err != nil {
					return nil, 0, err
				}
			}
		}
		if err := store.G6CAS(actor.TenantID, projectID, "trial_decision", decisionID, version, next); err != nil {
			return nil, 0, err
		}
		if err := g3Audit(tx, actor, projectID, "trial.decision.sign", decisionID, map[string]any{"responsibility": input.Responsibility, "actor": actor.UserID, "status": next.Status, "reason": input.Reason}); err != nil {
			return nil, 0, err
		}
		return next, 200, nil
	})
}
func (s *Service) ListTrialDecisions(ctx context.Context, actor Actor, projectID, windowID string) ([]TrialDecisionRecord, error) {
	var result []TrialDecisionRecord
	err := s.repository.Transaction(ctx, TransactionOptions{ReadOnly: true}, func(tx Transaction) error {
		if _, err := s.authorizeProject(ctx, tx, actor, projectID, "read"); err != nil {
			return err
		}
		store, ok := tx.(runtimeTransaction)
		if !ok {
			return ErrInvalidState
		}
		rows, err := store.G6List(actor.TenantID, projectID, "trial_decision")
		if err != nil {
			return err
		}
		result = []TrialDecisionRecord{}
		for _, row := range rows {
			var value TrialDecisionRecord
			if err := json.Unmarshal([]byte(row.BodyJSON), &value); err != nil {
				return err
			}
			if value.WindowID == windowID {
				result = append(result, value)
			}
		}
		return nil
	})
	return result, err
}
