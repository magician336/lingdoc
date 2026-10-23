package workspacecore

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// The model call ledger is the fact table for model calls and model switches.
//
// Every model call leaves one row (kind = call) and every model switch leaves
// one row (kind = switch). The two row kinds share one table because a switch
// and the calls it caused are two sides of the same event: split them and
// answering "why did this call use that model" needs a join across two tables.
//
// What a row is for:
//
//   - separating a real sample from a mock one mechanically (run_mode),
//   - attributing the cost of one product action (tokens, budget, duration),
//   - making a mid-run model change visible (the switch rows).
//
// The ledger records facts and makes NO authorization decision: the caller
// that owns the product action owns the permission to write its row. The
// columns follow lingdoc_rule_suggestion_runs -- the one shape this repository
// has already proven for "one model call, one row" -- and extend it with the
// provider, the parameters and the budget.

// ModelCallLedgerKindCall is the row kind of one model call.
const ModelCallLedgerKindCall = "call"

// ModelCallLedgerKindSwitch is the row kind of one model switch.
const ModelCallLedgerKindSwitch = "switch"

// The product action a ledger row belongs to. The kind says which product
// action it was; the id says which one.
const (
	// ModelCallLedgerSourceGenerationRun is one generation run.
	ModelCallLedgerSourceGenerationRun = "generation_run"
	// ModelCallLedgerSourceAgentTurn is one assistant turn.
	ModelCallLedgerSourceAgentTurn = "agent_turn"
	// ModelCallLedgerSourceRuleSuggestionRun is one rule suggestion run.
	ModelCallLedgerSourceRuleSuggestionRun = "rule_suggestion_run"
)

// The status of a ledger row: did the provider answer, or not.
const (
	// ModelCallLedgerStatusSucceeded means the provider answered.
	ModelCallLedgerStatusSucceeded = "succeeded"
	// ModelCallLedgerStatusFailed means the call produced no answer.
	ModelCallLedgerStatusFailed = "failed"
)

// ModelCallLedgerEntry is one ledger row.
//
// RunMode carries a value of the run-mode vocabulary the candidate layer owns
// (candidateadoption.RunMode). The ledger does not keep a second list of
// those values: it carries the one the call site produced, and the call site
// is the only place that decides which value applies.
type ModelCallLedgerEntry struct {
	// ID is the ledger row id. It is generated when the caller leaves it empty.
	ID string
	// TenantID is the tenant the row belongs to.
	TenantID uint64
	// ProjectID is the project the row belongs to. It is empty for a row that
	// belongs to a tenant-level action.
	ProjectID string
	// Kind is ModelCallLedgerKindCall or ModelCallLedgerKindSwitch.
	Kind string
	// Provider is the provider that answered the call.
	Provider string
	// ModelID is the model that answered the call.
	ModelID string
	// RunMode is the run-mode vocabulary value of this call.
	RunMode string
	// ParametersJSON is the JSON text of the parameters that were actually sent.
	ParametersJSON string
	// InputTokens and OutputTokens are the token counts the provider reported.
	InputTokens  int
	OutputTokens int
	// ReservedNanoUSD is the budget reserved for this call, and
	// ActualNanoUSD the budget it actually consumed. The ledger records the
	// budget; it does not enforce it.
	ReservedNanoUSD int64
	ActualNanoUSD   int64
	// DurationMS is how long the call took.
	DurationMS int64
	// Status is ModelCallLedgerStatusSucceeded or ModelCallLedgerStatusFailed.
	Status string
	// ErrorCode names the failure of a failed row.
	ErrorCode string
	// SourceKind is the kind of the product action the row belongs to, and
	// SourceID is that action's id.
	SourceKind string
	SourceID   string
	// SwitchedFromModelID, SwitchReason and AffectedStepsJSON carry the facts
	// of a switch. They are set on a switch row and empty on a call row.
	SwitchedFromModelID string
	SwitchReason        string
	AffectedStepsJSON   string
	// CreatedAt is when the row was written.
	CreatedAt time.Time
}

// ModelCallLedger stores the facts about model calls and model switches.
//
// The port is deliberately narrow: it writes rows and reads them back. It
// decides nothing about who may write or read them.
type ModelCallLedger interface {
	// RecordModelCall records one model call. The row kind is call.
	RecordModelCall(ctx context.Context, entry ModelCallLedgerEntry) error
	// RecordModelSwitch records one model switch. The row kind is switch.
	RecordModelSwitch(ctx context.Context, entry ModelCallLedgerEntry) error
	// ModelCallEntriesBySource returns the calls one product action caused.
	ModelCallEntriesBySource(ctx context.Context, tenantID uint64, sourceKind, sourceID string) ([]ModelCallLedgerEntry, error)
	// ModelSwitchEntriesByProject returns the switches recorded for one project.
	ModelSwitchEntriesByProject(ctx context.Context, tenantID uint64, projectID string) ([]ModelCallLedgerEntry, error)
}

// modelCallLedgerRow is the stored shape of one ledger row.
//
// The nullable columns are pointers so that a column which does not apply to
// the row kind is stored as NULL rather than as an empty string: "this call
// switched no model" and "the switch reason is the empty string" are not the
// same fact, and the second one is not a fact at all.
type modelCallLedgerRow struct {
	ID                  string    `gorm:"column:id;primaryKey"`
	TenantID            uint64    `gorm:"column:tenant_id"`
	ProjectID           *string   `gorm:"column:project_id"`
	Kind                string    `gorm:"column:kind"`
	Provider            *string   `gorm:"column:provider"`
	ModelID             *string   `gorm:"column:model_id"`
	RunMode             *string   `gorm:"column:run_mode"`
	ParametersJSON      *string   `gorm:"column:parameters_json"`
	InputTokens         int64     `gorm:"column:input_tokens"`
	OutputTokens        int64     `gorm:"column:output_tokens"`
	ReservedNanoUSD     int64     `gorm:"column:reserved_nano_usd"`
	ActualNanoUSD       int64     `gorm:"column:actual_nano_usd"`
	DurationMS          int64     `gorm:"column:duration_ms"`
	Status              string    `gorm:"column:status"`
	ErrorCode           *string   `gorm:"column:error_code"`
	SourceKind          *string   `gorm:"column:source_kind"`
	SourceID            *string   `gorm:"column:source_id"`
	SwitchedFromModelID *string   `gorm:"column:switched_from_model_id"`
	SwitchReason        *string   `gorm:"column:switch_reason"`
	AffectedStepsJSON   *string   `gorm:"column:affected_steps_json"`
	CreatedAt           time.Time `gorm:"column:created_at"`
}

func (modelCallLedgerRow) TableName() string { return "lingdoc_model_call_ledger" }

// modelCallLedgerStore is the ledger bound to one database. The database is
// either a workspace transaction or the database itself.
type modelCallLedgerStore struct{ db *gorm.DB }

// NewGORMModelCallLedger returns the ledger bound to one database.
//
// It returns nil when there is no database: a caller that has no database has
// no ledger, and saying so beats handing out a writer that fails on every row.
func NewGORMModelCallLedger(db *gorm.DB) ModelCallLedger {
	if db == nil {
		return nil
	}
	return modelCallLedgerStore{db: db}
}

// ModelCallLedgerIn returns the ledger bound to a workspace transaction, so
// that a row is written in the same transaction as the product action it
// belongs to.
func ModelCallLedgerIn(tx Transaction) (ModelCallLedger, error) {
	provider, ok := tx.(interface{ GORMDB() *gorm.DB })
	if !ok {
		return nil, ErrDependencyUnavailable
	}
	return modelCallLedgerStore{db: provider.GORMDB()}, nil
}

func (s modelCallLedgerStore) RecordModelCall(ctx context.Context, entry ModelCallLedgerEntry) error {
	entry.Kind = ModelCallLedgerKindCall
	return s.record(ctx, entry)
}

func (s modelCallLedgerStore) RecordModelSwitch(ctx context.Context, entry ModelCallLedgerEntry) error {
	entry.Kind = ModelCallLedgerKindSwitch
	return s.record(ctx, entry)
}

func (s modelCallLedgerStore) record(ctx context.Context, entry ModelCallLedgerEntry) error {
	if entry.ID == "" {
		entry.ID = uuid.NewString()
	}
	if entry.CreatedAt.IsZero() {
		entry.CreatedAt = time.Now().UTC()
	}
	if !validModelCallLedgerEntry(entry) {
		return ErrInvalidRequest
	}
	row := modelCallLedgerRow{
		ID:              entry.ID,
		TenantID:        entry.TenantID,
		ProjectID:       modelCallLedgerText(entry.ProjectID),
		Kind:            entry.Kind,
		Provider:        modelCallLedgerText(entry.Provider),
		ModelID:         modelCallLedgerText(entry.ModelID),
		RunMode:         modelCallLedgerText(entry.RunMode),
		ParametersJSON:  modelCallLedgerText(entry.ParametersJSON),
		InputTokens:     int64(entry.InputTokens),
		OutputTokens:    int64(entry.OutputTokens),
		ReservedNanoUSD: entry.ReservedNanoUSD,
		ActualNanoUSD:   entry.ActualNanoUSD,
		DurationMS:      entry.DurationMS,
		Status:          entry.Status,
		ErrorCode:       modelCallLedgerText(entry.ErrorCode),
		SourceKind:      modelCallLedgerText(entry.SourceKind),
		SourceID:        modelCallLedgerText(entry.SourceID),
		CreatedAt:       entry.CreatedAt,
	}
	if entry.Kind == ModelCallLedgerKindSwitch {
		row.SwitchedFromModelID = modelCallLedgerText(entry.SwitchedFromModelID)
		row.SwitchReason = modelCallLedgerText(entry.SwitchReason)
		row.AffectedStepsJSON = modelCallLedgerText(entry.AffectedStepsJSON)
	}
	if err := s.db.WithContext(ctx).Create(&row).Error; err != nil {
		return err
	}
	return nil
}

func (s modelCallLedgerStore) ModelCallEntriesBySource(ctx context.Context, tenantID uint64, sourceKind, sourceID string) ([]ModelCallLedgerEntry, error) {
	var rows []modelCallLedgerRow
	err := s.db.WithContext(ctx).
		Where("tenant_id = ? AND kind = ? AND source_kind = ? AND source_id = ?", tenantID, ModelCallLedgerKindCall, sourceKind, sourceID).
		Order("created_at").Find(&rows).Error
	if err != nil {
		return nil, err
	}
	return modelCallLedgerEntriesFromRows(rows), nil
}

func (s modelCallLedgerStore) ModelSwitchEntriesByProject(ctx context.Context, tenantID uint64, projectID string) ([]ModelCallLedgerEntry, error) {
	var rows []modelCallLedgerRow
	err := s.db.WithContext(ctx).
		Where("tenant_id = ? AND kind = ? AND project_id = ?", tenantID, ModelCallLedgerKindSwitch, projectID).
		Order("created_at").Find(&rows).Error
	if err != nil {
		return nil, err
	}
	return modelCallLedgerEntriesFromRows(rows), nil
}

// validModelCallLedgerEntry refuses a row that does not match its own kind.
//
// A call row that also carries switch facts is a row nobody can read: the
// reader would have to guess which of the two row kinds it is looking at. A
// switch row without the model it switched away from or without a reason is
// not a switch at all -- "the model changed" is not a fact, "the model changed
// because X" is.
func validModelCallLedgerEntry(entry ModelCallLedgerEntry) bool {
	if entry.ID == "" || entry.TenantID == 0 || entry.Status == "" || entry.SourceKind == "" {
		return false
	}
	if entry.ParametersJSON != "" && !json.Valid([]byte(entry.ParametersJSON)) {
		return false
	}
	switch entry.Kind {
	case ModelCallLedgerKindCall:
		return entry.SwitchedFromModelID == "" && entry.SwitchReason == "" && entry.AffectedStepsJSON == ""
	case ModelCallLedgerKindSwitch:
		return entry.SwitchedFromModelID != "" && entry.SwitchReason != "" && json.Valid([]byte(entry.AffectedStepsJSON))
	}
	return false
}

// modelCallLedgerText stores a value, or NULL when there is no value.
func modelCallLedgerText(value string) *string {
	if value == "" {
		return nil
	}
	copied := value
	return &copied
}

func modelCallLedgerEntriesFromRows(rows []modelCallLedgerRow) []ModelCallLedgerEntry {
	entries := make([]ModelCallLedgerEntry, 0, len(rows))
	for _, row := range rows {
		entries = append(entries, ModelCallLedgerEntry{
			ID:                  row.ID,
			TenantID:            row.TenantID,
			ProjectID:           modelCallLedgerValue(row.ProjectID),
			Kind:                row.Kind,
			Provider:            modelCallLedgerValue(row.Provider),
			ModelID:             modelCallLedgerValue(row.ModelID),
			RunMode:             modelCallLedgerValue(row.RunMode),
			ParametersJSON:      modelCallLedgerValue(row.ParametersJSON),
			InputTokens:         int(row.InputTokens),
			OutputTokens:        int(row.OutputTokens),
			ReservedNanoUSD:     row.ReservedNanoUSD,
			ActualNanoUSD:       row.ActualNanoUSD,
			DurationMS:          row.DurationMS,
			Status:              row.Status,
			ErrorCode:           modelCallLedgerValue(row.ErrorCode),
			SourceKind:          modelCallLedgerValue(row.SourceKind),
			SourceID:            modelCallLedgerValue(row.SourceID),
			SwitchedFromModelID: modelCallLedgerValue(row.SwitchedFromModelID),
			SwitchReason:        modelCallLedgerValue(row.SwitchReason),
			AffectedStepsJSON:   modelCallLedgerValue(row.AffectedStepsJSON),
			CreatedAt:           row.CreatedAt,
		})
	}
	return entries
}

func modelCallLedgerValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

// ModelCallFailureCode names the failure of a model call.
//
// It reuses the failure codes the agent turn already reports, so that one
// failure is not named two ways in two places: a call that timed out is
// "timeout" in the turn and "timeout" in the ledger.
func ModelCallFailureCode(err error) string { return agentFailureCode(err) }

var _ ModelCallLedger = modelCallLedgerStore{}
