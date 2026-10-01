package acceptancebudget

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	ProfileEnv                    = "LINGDOC_T10_ACCEPTANCE_PROFILE"
	LedgerEnv                     = "LINGDOC_T10_ACCEPTANCE_LEDGER"
	Profile                       = "lingdoc-t10-deepseek-flash-20260929-v1"
	MaxCalls                      = 6
	InputMax                      = 16000
	OutputMax                     = 6000
	MaxConcurrency                = 1
	DeepSeekModel                 = "deepseek-flash"
	QwenEmbedding                 = "qwen3.7-text-embedding"
	QwenBaseHostTail              = ".cn-beijing.maas.aliyuncs.com"
	QwenBasePath                  = "/compatible-mode/v1"
	DeepSeekInputNanoUSDPerToken  = int64(300)
	DeepSeekOutputNanoUSDPerToken = int64(1200)
	DeepSeekBudgetFXNanoCNYPerUSD = int64(10)
	QwenNanoCNYPerToken           = int64(500)
	DeepSeekPerCallNanoUSD        = int64(InputMax)*DeepSeekInputNanoUSDPerToken + int64(OutputMax)*DeepSeekOutputNanoUSDPerToken
	DeepSeekTotalNanoUSD          = DeepSeekPerCallNanoUSD * MaxCalls
	HumanBackstopCNY              = 5
	MaxBudgetNanoCNY              = int64(5_000_000_000)
	ledgerVersion                 = 1
	maxBudgetNanoCNY              = MaxBudgetNanoCNY // compatibility alias for package-local checks
)

var (
	ErrUnavailable   = errors.New("model acceptance budget unavailable")
	ErrBlocked       = errors.New("model acceptance budget requires reconciliation")
	ErrCallsSpent    = errors.New("model acceptance request budget exhausted")
	ErrInputTooLarge = errors.New("model acceptance input exceeds the round limit")
	ErrWrongModel    = errors.New("model is outside the acceptance budget profile")
)

type Budget struct {
	profile    string
	ledgerPath string
}

type Ledger struct {
	SchemaVersion      int       `json:"schema_version"`
	Profile            string    `json:"profile"`
	MaxCalls           int       `json:"max_calls"`
	InputMaxTokens     int       `json:"input_max_tokens"`
	OutputMaxTokens    int       `json:"output_max_tokens"`
	MaxConcurrency     int       `json:"max_concurrency"`
	FXReserveCNYPerUSD int       `json:"usd_to_cny_reserve_rate"`
	MaxReserveNanoCNY  int64     `json:"max_reserve_nano_cny"`
	ReservedNanoCNY    int64     `json:"reserved_nano_cny"`
	ActualNanoCNY      int64     `json:"actual_nano_cny"`
	ReservedNanoUSD    int64     `json:"reserved_nano_usd"`
	ActualNanoUSD      int64     `json:"actual_nano_usd"`
	Blocked            bool      `json:"blocked"`
	BlockedReason      string    `json:"blocked_reason,omitempty"`
	Requests           []Request `json:"requests"`
}

type Request struct {
	Number          int       `json:"number"`
	Model           string    `json:"model"`
	Status          string    `json:"status"`
	InputUpperBound int       `json:"input_upper_bound_tokens"`
	OutputLimit     int       `json:"output_limit_tokens"`
	InputTokens     int       `json:"input_tokens,omitempty"`
	OutputTokens    int       `json:"output_tokens,omitempty"`
	ReservedNanoCNY int64     `json:"reserved_nano_cny"`
	ActualNanoCNY   int64     `json:"actual_nano_cny,omitempty"`
	ReservedNanoUSD int64     `json:"reserved_nano_usd,omitempty"`
	ActualNanoUSD   int64     `json:"actual_nano_usd,omitempty"`
	FinishReason    string    `json:"finish_reason,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
}

type Reservation struct {
	budget     *Budget
	lockPath   string
	lock       *os.File
	requestNum int
	model      string
	finished   bool
}

func NewFromEnv(profile string) (*Budget, error) {
	if profile == "" || strings.TrimSpace(os.Getenv(ProfileEnv)) != profile || profile != Profile {
		return nil, ErrUnavailable
	}
	ledgerPath := strings.TrimSpace(os.Getenv(LedgerEnv))
	if !filepath.IsAbs(ledgerPath) || filepath.Ext(ledgerPath) != ".json" {
		return nil, ErrUnavailable
	}
	return New(profile, ledgerPath+".calls.json"), nil
}

func New(profile, ledgerPath string) *Budget {
	return &Budget{profile: profile, ledgerPath: filepath.Clean(ledgerPath)}
}

func (b *Budget) Reserve(ctx context.Context, model string, inputUpperBound, outputLimit int) (*Reservation, error) {
	if b == nil || b.profile != Profile || !filepath.IsAbs(b.ledgerPath) {
		return nil, ErrUnavailable
	}
	if inputUpperBound < 0 || inputUpperBound > InputMax || outputLimit < 0 || outputLimit > OutputMax {
		return nil, ErrInputTooLarge
	}
	reserveNanoCNY, reserveNanoUSD, err := reserveAmounts(model, inputUpperBound, outputLimit)
	if err != nil {
		return nil, err
	}
	lockPath := b.ledgerPath + ".lock"
	lock, err := AcquireLock(ctx, lockPath)
	if err != nil {
		return nil, ErrUnavailable
	}
	ledger, err := readLedger(b.ledgerPath)
	if err != nil || !validLedger(ledger, b.profile) {
		_ = ReleaseLock(lock, lockPath)
		return nil, ErrUnavailable
	}
	if ledger.Blocked || hasUnresolved(ledger) {
		_ = ReleaseLock(lock, lockPath)
		return nil, ErrBlocked
	}
	if len(ledger.Requests) >= MaxCalls || ledger.ReservedNanoCNY+reserveNanoCNY > MaxBudgetNanoCNY {
		_ = ReleaseLock(lock, lockPath)
		return nil, ErrCallsSpent
	}
	request := Request{
		Number:          len(ledger.Requests) + 1,
		Model:           model,
		Status:          "in_flight",
		InputUpperBound: inputUpperBound,
		OutputLimit:     outputLimit,
		ReservedNanoCNY: reserveNanoCNY,
		ReservedNanoUSD: reserveNanoUSD,
		CreatedAt:       time.Now().UTC(),
	}
	ledger.Requests = append(ledger.Requests, request)
	ledger.ReservedNanoCNY += reserveNanoCNY
	ledger.ReservedNanoUSD += reserveNanoUSD
	if err := writeLedger(b.ledgerPath, ledger); err != nil {
		_ = ReleaseLock(lock, lockPath)
		return nil, ErrUnavailable
	}
	return &Reservation{budget: b, lockPath: lockPath, lock: lock, requestNum: request.Number, model: model}, nil
}

func (r *Reservation) Complete(status string, inputTokens, outputTokens int, finishReason string, block bool) error {
	if r == nil || r.finished || r.lock == nil {
		return ErrUnavailable
	}
	ledger, err := readLedger(r.budget.ledgerPath)
	if err != nil || r.requestNum < 1 || r.requestNum > len(ledger.Requests) {
		r.release()
		return ErrUnavailable
	}
	request := &ledger.Requests[r.requestNum-1]
	if request.Number != r.requestNum || request.Status != "in_flight" || request.Model != r.model {
		ledger.Blocked = true
		ledger.BlockedReason = "reservation sequence mismatch"
		_ = writeLedger(r.budget.ledgerPath, ledger)
		r.release()
		return ErrBlocked
	}
	actualNanoCNY, actualNanoUSD, costErr := actualAmounts(r.model, inputTokens, outputTokens)
	if costErr != nil || inputTokens < 0 || outputTokens < 0 || inputTokens > request.InputUpperBound ||
		outputTokens > request.OutputLimit {
		block = true
		status = "limit_violation"
		if costErr != nil {
			actualNanoCNY, actualNanoUSD = request.ReservedNanoCNY+1, request.ReservedNanoUSD+1
		}
	}
	request.Status = status
	request.InputTokens = inputTokens
	request.OutputTokens = outputTokens
	request.FinishReason = finishReason
	request.ActualNanoCNY = actualNanoCNY
	request.ActualNanoUSD = actualNanoUSD
	ledger.ActualNanoCNY += actualNanoCNY
	ledger.ActualNanoUSD += actualNanoUSD
	if block {
		ledger.Blocked = true
		ledger.BlockedReason = status
	}
	if ledger.ActualNanoCNY > ledger.ReservedNanoCNY || ledger.ActualNanoCNY > ledger.MaxReserveNanoCNY {
		ledger.Blocked = true
		ledger.BlockedReason = "actual usage exceeded reservation"
	}
	writeErr := writeLedger(r.budget.ledgerPath, ledger)
	r.release()
	if writeErr != nil {
		return ErrUnavailable
	}
	if ledger.Blocked {
		return ErrCallsSpent
	}
	return nil
}

func (r *Reservation) Unknown(status string) error {
	return r.Complete(status, r.upperInputBound(), 0, "", true)
}

func (r *Reservation) CancelNoCall() error {
	if r == nil || r.finished || r.lock == nil {
		return ErrUnavailable
	}
	ledger, err := readLedger(r.budget.ledgerPath)
	if err != nil || r.requestNum != len(ledger.Requests) {
		return r.Unknown("cancel_reconciliation_required")
	}
	request := ledger.Requests[r.requestNum-1]
	if request.Status != "in_flight" || request.Model != r.model {
		return r.Unknown("cancel_reconciliation_required")
	}
	ledger.Requests = ledger.Requests[:len(ledger.Requests)-1]
	ledger.ReservedNanoCNY -= request.ReservedNanoCNY
	ledger.ReservedNanoUSD -= request.ReservedNanoUSD
	writeErr := writeLedger(r.budget.ledgerPath, ledger)
	r.release()
	if writeErr != nil {
		return ErrUnavailable
	}
	return nil
}

func (r *Reservation) upperInputBound() int {
	if r == nil || r.requestNum < 1 {
		return 0
	}
	ledger, err := readLedger(r.budget.ledgerPath)
	if err != nil || r.requestNum > len(ledger.Requests) {
		return 0
	}
	return ledger.Requests[r.requestNum-1].InputUpperBound
}

func (r *Reservation) release() {
	if r == nil || r.finished {
		return
	}
	r.finished = true
	_ = ReleaseLock(r.lock, r.lockPath)
	r.lock = nil
}

func reserveAmounts(model string, inputUpperBound, outputLimit int) (int64, int64, error) {
	switch model {
	case DeepSeekModel:
		usd := int64(inputUpperBound)*DeepSeekInputNanoUSDPerToken + int64(outputLimit)*DeepSeekOutputNanoUSDPerToken
		return usd * DeepSeekBudgetFXNanoCNYPerUSD, usd, nil
	case QwenEmbedding:
		if outputLimit != 0 {
			return 0, 0, ErrWrongModel
		}
		return int64(inputUpperBound) * QwenNanoCNYPerToken, 0, nil
	default:
		return 0, 0, ErrWrongModel
	}
}

func actualAmounts(model string, inputTokens, outputTokens int) (int64, int64, error) {
	if inputTokens < 0 || outputTokens < 0 {
		return 0, 0, ErrInputTooLarge
	}
	switch model {
	case DeepSeekModel:
		usd := int64(inputTokens)*DeepSeekInputNanoUSDPerToken + int64(outputTokens)*DeepSeekOutputNanoUSDPerToken
		return usd * DeepSeekBudgetFXNanoCNYPerUSD, usd, nil
	case QwenEmbedding:
		if outputTokens != 0 {
			return 0, 0, ErrWrongModel
		}
		return int64(inputTokens) * QwenNanoCNYPerToken, 0, nil
	default:
		return 0, 0, ErrWrongModel
	}
}

func freshLedger(profile string) Ledger {
	return Ledger{
		SchemaVersion:      ledgerVersion,
		Profile:            profile,
		MaxCalls:           MaxCalls,
		InputMaxTokens:     InputMax,
		OutputMaxTokens:    OutputMax,
		MaxConcurrency:     MaxConcurrency,
		FXReserveCNYPerUSD: int(DeepSeekBudgetFXNanoCNYPerUSD),
		MaxReserveNanoCNY:  MaxBudgetNanoCNY,
		Requests:           []Request{},
	}
}

func readLedger(path string) (Ledger, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return freshLedger(Profile), nil
	}
	if err != nil {
		return Ledger{}, err
	}
	var ledger Ledger
	if err := json.Unmarshal(data, &ledger); err != nil || ledger.SchemaVersion != ledgerVersion {
		return Ledger{}, ErrUnavailable
	}
	return ledger, nil
}

func validLedger(ledger Ledger, profile string) bool {
	if ledger.SchemaVersion != ledgerVersion || ledger.Profile != profile || ledger.MaxCalls != MaxCalls ||
		ledger.InputMaxTokens != InputMax || ledger.OutputMaxTokens != OutputMax || ledger.MaxConcurrency != MaxConcurrency ||
		ledger.FXReserveCNYPerUSD != int(DeepSeekBudgetFXNanoCNYPerUSD) || ledger.MaxReserveNanoCNY != MaxBudgetNanoCNY ||
		len(ledger.Requests) > MaxCalls || ledger.ReservedNanoCNY < 0 || ledger.ReservedNanoCNY > MaxBudgetNanoCNY ||
		ledger.ActualNanoCNY < 0 || ledger.ActualNanoCNY > ledger.ReservedNanoCNY || ledger.ReservedNanoUSD < 0 ||
		ledger.ActualNanoUSD < 0 || ledger.ActualNanoUSD > ledger.ReservedNanoUSD {
		return false
	}
	var reserveCNY, actualCNY, reserveUSD, actualUSD int64
	for index, request := range ledger.Requests {
		if request.Number != index+1 || request.InputUpperBound < 0 || request.InputUpperBound > InputMax ||
			request.OutputLimit < 0 || request.OutputLimit > OutputMax || request.InputTokens < 0 || request.OutputTokens < 0 {
			return false
		}
		expectedCNY, expectedUSD, err := reserveAmounts(request.Model, request.InputUpperBound, request.OutputLimit)
		if err != nil || expectedCNY != request.ReservedNanoCNY || expectedUSD != request.ReservedNanoUSD {
			return false
		}
		switch request.Status {
		case "in_flight", "outcome_unknown", "usage_unknown", "http_error", "response_received", "incomplete", "limit_violation", "cancel_reconciliation_required":
		default:
			return false
		}
		if request.Status == "in_flight" || request.Status == "outcome_unknown" || request.Status == "usage_unknown" ||
			request.Status == "http_error" || request.Status == "limit_violation" || request.Status == "cancel_reconciliation_required" {
			if !ledger.Blocked {
				return false
			}
		}
		reserveCNY += request.ReservedNanoCNY
		actualCNY += request.ActualNanoCNY
		reserveUSD += request.ReservedNanoUSD
		actualUSD += request.ActualNanoUSD
	}
	return reserveCNY == ledger.ReservedNanoCNY && actualCNY == ledger.ActualNanoCNY && reserveUSD == ledger.ReservedNanoUSD && actualUSD == ledger.ActualNanoUSD
}

func hasUnresolved(ledger Ledger) bool {
	for _, request := range ledger.Requests {
		if request.Status == "in_flight" || request.Status == "outcome_unknown" || request.Status == "usage_unknown" ||
			request.Status == "http_error" || request.Status == "limit_violation" || request.Status == "cancel_reconciliation_required" {
			return true
		}
	}
	return false
}

// LockLeaseTTL bounds how long a crashed process can keep a ledger blocked.
// Normal reservations complete well before this lease expires; callers that
// need a longer operation must use a separate, renewable coordination scheme.
const LockLeaseTTL = 10 * time.Minute

// AcquireLock creates a process-independent lock file and reclaims a lock
// whose lease has expired. The metadata is diagnostic; the mtime is the lease
// clock so a partially written lock file is still recoverable after a crash.
func AcquireLock(ctx context.Context, path string) (*os.File, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	for {
		lock, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			_, _ = fmt.Fprintf(lock, "pid=%d acquired_at=%s\n", os.Getpid(), time.Now().UTC().Format(time.RFC3339Nano))
			return lock, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, err
		}
		if lockLeaseExpired(path) {
			if removeErr := os.Remove(path); removeErr == nil || errors.Is(removeErr, os.ErrNotExist) {
				continue
			}
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
}

func lockLeaseExpired(path string) bool {
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return true
	}
	if err != nil {
		return false
	}
	return time.Now().UTC().After(info.ModTime().Add(LockLeaseTTL))
}

// ReleaseLock closes and removes a ledger lock. Removal tolerates a prior
// stale-lock recovery so a completed owner cannot turn that recovery into an
// error path.
func ReleaseLock(lock *os.File, path string) error {
	var owned os.FileInfo
	if lock != nil {
		var err error
		owned, err = lock.Stat()
		if err != nil {
			_ = lock.Close()
			return err
		}
		if err := lock.Close(); err != nil {
			return err
		}
	}
	current, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	// A stale-lock recovery may have replaced this path while the original
	// owner was finishing. Never remove the replacement owner's lock.
	if owned != nil && !os.SameFile(owned, current) {
		return nil
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func writeLedger(path string, ledger Ledger) error {
	data, err := json.MarshalIndent(ledger, "", "  ")
	if err != nil {
		return err
	}
	tmpPath := fmt.Sprintf("%s.tmp-%d-%d", path, os.Getpid(), time.Now().UnixNano())
	if err := os.WriteFile(tmpPath, data, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	return nil
}
