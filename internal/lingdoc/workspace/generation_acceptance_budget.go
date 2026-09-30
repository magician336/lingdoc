package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/lingdoc/generation"
	"github.com/Tencent/WeKnora/internal/models/acceptancebudget"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
)

const (
	generationAcceptanceProfileEnv = "LINGDOC_T10_ACCEPTANCE_PROFILE"
	generationAcceptanceLedgerEnv  = "LINGDOC_T10_ACCEPTANCE_LEDGER"
	generationAcceptanceProfile    = "lingdoc-t10-deepseek-flash-20260929-v1"
	generationAcceptanceModel      = "deepseek-flash"
	generationAcceptanceInputMax   = 16000
	generationAcceptanceOutputMax  = 6000
	generationAcceptanceCallsMax   = 6
	generationAcceptancePerCall    = int64(12_000_000) // $0.012 in nano-USD at the stated peak rates.
	generationAcceptanceTotal      = int64(72_000_000) // $0.072 maximum reservation for six calls.
	generationPromptOverhead       = 1024
	deepSeekInputNanoUSDPerToken   = int64(300)  // $0.30 / 1M tokens.
	deepSeekOutputNanoUSDPerToken  = int64(1200) // $1.20 / 1M tokens.
)

var (
	errAcceptanceLedgerBusy    = errors.New("acceptance ledger busy")
	errAcceptanceLedgerIO      = errors.New("acceptance ledger unavailable")
	errAcceptanceLedgerBlocked = errors.New("acceptance ledger requires reconciliation")
	errAcceptanceBudgetSpent   = errors.New("acceptance request budget exhausted")
	errAcceptanceInputTooLarge = errors.New("acceptance input exceeds the round limit")
	errAcceptanceWrongModel    = errors.New("acceptance model does not match the profile")
)

type generationAcceptanceBudget struct {
	ledgerPath string
	total      *acceptancebudget.Budget
}

type generationAcceptanceLedger struct {
	SchemaVersion int                           `json:"schema_version"`
	Profile       string                        `json:"profile"`
	Model         string                        `json:"model"`
	PricingBasis  string                        `json:"pricing_basis"`
	InputMax      int                           `json:"input_max_tokens"`
	OutputMax     int                           `json:"output_max_tokens"`
	CallsMax      int                           `json:"calls_max"`
	CNYBackstop   int                           `json:"human_backstop_cny"`
	ReserveMax    int64                         `json:"reserve_max_nano_usd"`
	Reserved      int64                         `json:"reserved_nano_usd"`
	Actual        int64                         `json:"actual_nano_usd"`
	Blocked       bool                          `json:"blocked"`
	BlockedReason string                        `json:"blocked_reason,omitempty"`
	Requests      []generationAcceptanceRequest `json:"requests"`
}

type generationAcceptanceRequest struct {
	Number                   int       `json:"number"`
	Model                    string    `json:"model"`
	Status                   string    `json:"status"`
	ReservedNanoUSD          int64     `json:"reserved_nano_usd"`
	EstimatedInputUpperBound int       `json:"estimated_input_token_upper_bound"`
	OutputLimit              int       `json:"output_limit_tokens"`
	InputTokens              int       `json:"input_tokens,omitempty"`
	OutputTokens             int       `json:"output_tokens,omitempty"`
	ActualNanoUSD            int64     `json:"actual_nano_usd,omitempty"`
	FinishReason             string    `json:"finish_reason,omitempty"`
	CreatedAt                time.Time `json:"created_at"`
}

type generationAcceptanceReservation struct {
	budget     *generationAcceptanceBudget
	total      *acceptancebudget.Reservation
	lockPath   string
	lock       *os.File
	requestNum int
	finished   bool
}

func loadGenerationAcceptanceBudget() (*generationAcceptanceBudget, error) {
	profile := strings.TrimSpace(os.Getenv(generationAcceptanceProfileEnv))
	if profile == "" {
		return nil, nil
	}
	if profile != generationAcceptanceProfile {
		return nil, generation.ErrAcceptanceBudgetUnavailable
	}
	ledgerPath := strings.TrimSpace(os.Getenv(generationAcceptanceLedgerEnv))
	if !filepath.IsAbs(ledgerPath) || filepath.Ext(ledgerPath) != ".json" {
		return nil, generation.ErrAcceptanceBudgetUnavailable
	}
	total, err := acceptancebudget.NewFromEnv(profile)
	if err != nil {
		return nil, generation.ErrAcceptanceBudgetUnavailable
	}
	return &generationAcceptanceBudget{ledgerPath: filepath.Clean(ledgerPath), total: total}, nil
}

func (b *generationAcceptanceBudget) reserve(ctx context.Context, modelName string, inputUpperBound int) (*generationAcceptanceReservation, error) {
	local, err := b.reserveChat(ctx, modelName, inputUpperBound)
	if err != nil {
		return nil, err
	}
	totalBudget := b.total
	if totalBudget == nil {
		totalBudget = acceptancebudget.New(generationAcceptanceProfile, b.ledgerPath+".calls.json")
	}
	total, err := totalBudget.Reserve(ctx, modelName, inputUpperBound, generationAcceptanceOutputMax)
	if err != nil {
		if cancelErr := local.cancelNoCall(); cancelErr != nil {
			return nil, errAcceptanceLedgerBlocked
		}
		return nil, mapAcceptanceBudgetError(err)
	}
	local.total = total
	return local, nil
}

func (b *generationAcceptanceBudget) reserveChat(ctx context.Context, modelName string, inputUpperBound int) (*generationAcceptanceReservation, error) {
	if b == nil || inputUpperBound < 0 || inputUpperBound > generationAcceptanceInputMax {
		return nil, errAcceptanceInputTooLarge
	}
	lockPath := b.ledgerPath + ".lock"
	lock, err := acquireAcceptanceLedgerLock(ctx, lockPath)
	if err != nil {
		return nil, errAcceptanceLedgerBusy
	}
	releaseOnError := true
	defer func() {
		if releaseOnError {
			_ = releaseAcceptanceLedgerLock(lock, lockPath)
		}
	}()
	ledger, err := readAcceptanceLedger(b.ledgerPath)
	if err != nil {
		return nil, errAcceptanceLedgerIO
	}
	if !validAcceptanceLedger(ledger) {
		return nil, errAcceptanceLedgerIO
	}
	if ledger.Blocked || acceptanceLedgerHasUnknownCall(ledger) {
		return nil, errAcceptanceLedgerBlocked
	}
	if ledger.Profile != generationAcceptanceProfile || ledger.Model != generationAcceptanceModel ||
		ledger.InputMax != generationAcceptanceInputMax || ledger.OutputMax != generationAcceptanceOutputMax ||
		ledger.CallsMax != generationAcceptanceCallsMax || ledger.ReserveMax != generationAcceptanceTotal ||
		ledger.CNYBackstop != 5 ||
		modelName != generationAcceptanceModel {
		return nil, errAcceptanceWrongModel
	}
	if len(ledger.Requests) >= generationAcceptanceCallsMax || ledger.Reserved+generationAcceptancePerCall > generationAcceptanceTotal {
		return nil, errAcceptanceBudgetSpent
	}
	request := generationAcceptanceRequest{
		Number:                   len(ledger.Requests) + 1,
		Model:                    generationAcceptanceModel,
		Status:                   "in_flight",
		ReservedNanoUSD:          generationAcceptancePerCall,
		EstimatedInputUpperBound: inputUpperBound,
		OutputLimit:              generationAcceptanceOutputMax,
		CreatedAt:                time.Now().UTC(),
	}
	ledger.Requests = append(ledger.Requests, request)
	ledger.Reserved += generationAcceptancePerCall
	if err := writeAcceptanceLedger(b.ledgerPath, ledger); err != nil {
		return nil, errAcceptanceLedgerIO
	}
	releaseOnError = false
	return &generationAcceptanceReservation{budget: b, lockPath: lockPath, lock: lock, requestNum: request.Number}, nil
}

func mapAcceptanceBudgetError(err error) error {
	switch {
	case errors.Is(err, acceptancebudget.ErrBlocked):
		return errAcceptanceLedgerBlocked
	case errors.Is(err, acceptancebudget.ErrCallsSpent):
		return errAcceptanceBudgetSpent
	case errors.Is(err, acceptancebudget.ErrInputTooLarge):
		return errAcceptanceInputTooLarge
	case errors.Is(err, acceptancebudget.ErrWrongModel):
		return errAcceptanceWrongModel
	default:
		return errAcceptanceLedgerIO
	}
}

func (r *generationAcceptanceReservation) complete(status string, inputTokens, outputTokens int, finishReason string, block bool) error {
	if r == nil || r.finished || r.lock == nil {
		return errAcceptanceLedgerIO
	}
	ledger, err := readAcceptanceLedger(r.budget.ledgerPath)
	if err != nil || r.requestNum < 1 || r.requestNum > len(ledger.Requests) {
		if r.total != nil {
			_ = r.total.Unknown("chat_ledger_unavailable")
		}
		r.release()
		return errAcceptanceLedgerIO
	}
	request := &ledger.Requests[r.requestNum-1]
	if request.Number != r.requestNum || request.Status != "in_flight" {
		if r.total != nil {
			_ = r.total.Unknown("chat_ledger_sequence_mismatch")
		}
		r.release()
		return errAcceptanceLedgerIO
	}
	request.Status = status
	request.InputTokens = inputTokens
	request.OutputTokens = outputTokens
	request.FinishReason = finishReason
	request.ActualNanoUSD = actualDeepSeekCost(inputTokens, outputTokens)
	ledger.Actual += request.ActualNanoUSD
	if block {
		ledger.Blocked = true
		ledger.BlockedReason = status
	}
	if ledger.Actual > ledger.Reserved || ledger.Actual > ledger.ReserveMax {
		ledger.Blocked = true
		ledger.BlockedReason = "actual usage exceeded reservation"
	}
	writeErr := writeAcceptanceLedger(r.budget.ledgerPath, ledger)
	var totalErr error
	if r.total != nil {
		if block {
			totalErr = r.total.Complete(status, inputTokens, outputTokens, finishReason, true)
		} else {
			totalErr = r.total.Complete(status, inputTokens, outputTokens, finishReason, false)
		}
	}
	r.release()
	if writeErr != nil {
		return errAcceptanceLedgerIO
	}
	if totalErr != nil {
		if errors.Is(totalErr, acceptancebudget.ErrCallsSpent) {
			if status == "outcome_unknown" || status == "usage_unknown" {
				return errAcceptanceLedgerBlocked
			}
			return errAcceptanceBudgetSpent
		}
		if errors.Is(totalErr, acceptancebudget.ErrBlocked) {
			return errAcceptanceLedgerBlocked
		}
		return errAcceptanceLedgerIO
	}
	if block || ledger.Blocked {
		if status == "outcome_unknown" || status == "usage_unknown" {
			return errAcceptanceLedgerBlocked
		}
		return errAcceptanceBudgetSpent
	}
	return nil
}

func (r *generationAcceptanceReservation) unknown(status string) error {
	return r.complete(status, 0, 0, "", true)
}

func (r *generationAcceptanceReservation) cancelNoCall() error {
	if r == nil || r.finished || r.lock == nil {
		return errAcceptanceLedgerIO
	}
	ledger, err := readAcceptanceLedger(r.budget.ledgerPath)
	if err != nil || r.requestNum != len(ledger.Requests) {
		_ = r.unknown("cancel_reconciliation_required")
		return errAcceptanceLedgerBlocked
	}
	request := ledger.Requests[r.requestNum-1]
	if request.Status != "in_flight" {
		_ = r.unknown("cancel_reconciliation_required")
		return errAcceptanceLedgerBlocked
	}
	ledger.Requests = ledger.Requests[:len(ledger.Requests)-1]
	ledger.Reserved -= request.ReservedNanoUSD
	writeErr := writeAcceptanceLedger(r.budget.ledgerPath, ledger)
	r.release()
	if writeErr != nil {
		return errAcceptanceLedgerIO
	}
	return nil
}

func (r *generationAcceptanceReservation) release() {
	if r == nil || r.finished {
		return
	}
	r.finished = true
	_ = releaseAcceptanceLedgerLock(r.lock, r.lockPath)
	r.lock = nil
}

func selectDeepSeekAcceptanceModel(models []*types.Model) (*types.Model, bool) {
	var selected *types.Model
	for _, model := range models {
		if model == nil || model.Name != generationAcceptanceModel {
			continue
		}
		if selected != nil {
			return nil, false
		}
		selected = model
	}
	if selected == nil || selected.Type != types.ModelTypeKnowledgeQA || selected.Source != types.ModelSourceRemote ||
		selected.Status != types.ModelStatusActive || selected.Parameters.Provider != "generic" ||
		strings.TrimRight(selected.Parameters.BaseURL, "/") != "https://api.deepseek.com" ||
		selected.Parameters.ExtraConfig[chat.ExtraConfigThinkingControl] != "thinking_type" {
		return nil, false
	}
	return selected, true
}

func acceptanceGenerationChatOptions() *chat.ChatOptions {
	disabled := false
	return &chat.ChatOptions{
		Temperature:         0.2,
		MaxCompletionTokens: generationAcceptanceOutputMax,
		Thinking:            &disabled,
		Format:              json.RawMessage(`{"type":"json_object"}`),
	}
}

func acceptanceInputUpperBound(messages []chat.Message) (int, error) {
	encoded, err := json.Marshal(messages)
	if err != nil {
		return 0, err
	}
	return len(encoded) + generationPromptOverhead, nil
}

func actualDeepSeekCost(inputTokens, outputTokens int) int64 {
	if inputTokens < 0 || outputTokens < 0 {
		return generationAcceptanceTotal + 1
	}
	return int64(inputTokens)*deepSeekInputNanoUSDPerToken + int64(outputTokens)*deepSeekOutputNanoUSDPerToken
}

func acceptanceLedgerHasUnknownCall(ledger generationAcceptanceLedger) bool {
	for _, request := range ledger.Requests {
		if request.Status == "in_flight" || request.Status == "outcome_unknown" {
			return true
		}
	}
	return false
}

func acquireAcceptanceLedgerLock(ctx context.Context, path string) (*os.File, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, errAcceptanceLedgerIO
	}
	for {
		lock, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			_, _ = fmt.Fprintf(lock, "pid=%d acquired_at=%s\n", os.Getpid(), time.Now().UTC().Format(time.RFC3339Nano))
			return lock, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, errAcceptanceLedgerIO
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
}

func releaseAcceptanceLedgerLock(lock *os.File, path string) error {
	if lock != nil {
		if err := lock.Close(); err != nil {
			return errAcceptanceLedgerIO
		}
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return errAcceptanceLedgerIO
	}
	return nil
}

func readAcceptanceLedger(path string) (generationAcceptanceLedger, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return generationAcceptanceLedger{
			SchemaVersion: 1,
			Profile:       generationAcceptanceProfile,
			Model:         generationAcceptanceModel,
			PricingBasis:  "DeepSeek official peak rates captured for 2026-09-29: input $0.30/M, output $1.20/M; cache treated as miss.",
			InputMax:      generationAcceptanceInputMax,
			OutputMax:     generationAcceptanceOutputMax,
			CallsMax:      generationAcceptanceCallsMax,
			CNYBackstop:   5,
			ReserveMax:    generationAcceptanceTotal,
			Requests:      []generationAcceptanceRequest{},
		}, nil
	}
	if err != nil {
		return generationAcceptanceLedger{}, err
	}
	var ledger generationAcceptanceLedger
	if err := json.Unmarshal(data, &ledger); err != nil || ledger.SchemaVersion != 1 {
		return generationAcceptanceLedger{}, errAcceptanceLedgerIO
	}
	return ledger, nil
}

func validAcceptanceLedger(ledger generationAcceptanceLedger) bool {
	if ledger.SchemaVersion != 1 || ledger.Profile != generationAcceptanceProfile || ledger.Model != generationAcceptanceModel ||
		ledger.InputMax != generationAcceptanceInputMax || ledger.OutputMax != generationAcceptanceOutputMax ||
		ledger.CallsMax != generationAcceptanceCallsMax || ledger.CNYBackstop != 5 || ledger.ReserveMax != generationAcceptanceTotal ||
		len(ledger.Requests) > generationAcceptanceCallsMax || ledger.Reserved != int64(len(ledger.Requests))*generationAcceptancePerCall ||
		ledger.Actual < 0 || ledger.Actual > ledger.Reserved {
		return false
	}
	var actualTotal int64
	for i, request := range ledger.Requests {
		if request.Number != i+1 || request.Model != generationAcceptanceModel || request.ReservedNanoUSD != generationAcceptancePerCall ||
			request.EstimatedInputUpperBound < 0 || request.EstimatedInputUpperBound > generationAcceptanceInputMax ||
			request.OutputLimit != generationAcceptanceOutputMax || request.InputTokens < 0 || request.OutputTokens < 0 {
			return false
		}
		switch request.Status {
		case "in_flight", "outcome_unknown", "usage_unknown", "response_received", "incomplete", "limit_violation":
		default:
			return false
		}
		if request.Status == "outcome_unknown" || request.Status == "usage_unknown" || request.Status == "in_flight" || request.Status == "limit_violation" {
			if !ledger.Blocked {
				return false
			}
		}
		actualTotal += request.ActualNanoUSD
	}
	return actualTotal == ledger.Actual
}

func writeAcceptanceLedger(path string, ledger generationAcceptanceLedger) error {
	data, err := json.MarshalIndent(ledger, "", "  ")
	if err != nil {
		return errAcceptanceLedgerIO
	}
	tmpPath := fmt.Sprintf("%s.tmp-%d-%d", path, os.Getpid(), time.Now().UnixNano())
	if err := os.WriteFile(tmpPath, data, 0o600); err != nil {
		return errAcceptanceLedgerIO
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return errAcceptanceLedgerIO
	}
	return nil
}
