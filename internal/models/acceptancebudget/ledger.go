package acceptancebudget

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	ProfileEnv                    = "LINGDOC_T10_ACCEPTANCE_PROFILE"
	LedgerEnv                     = "LINGDOC_T10_ACCEPTANCE_LEDGER"
	Profile                       = "lingdoc-t10-deepseek-flash-20260929-v1"
	CostOnlyProfile               = "lingdoc-demo-cny20-20261008-v2"
	CostOnlyMaxBudgetNanoCNY      = int64(19_999_999_999) // Strictly below CNY 20.
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
	SchemaVersion   int    `json:"schema_version"`
	Profile         string `json:"profile"`
	MaxCalls        int    `json:"max_calls"`
	InputMaxTokens  int    `json:"input_max_tokens"`
	OutputMaxTokens int    `json:"output_max_tokens"`
	// TotalMaxTokens is the ceiling of input plus output of ONE request.
	// It is 0 in a ledger written before the ceiling existed, which reads as
	// "the sum was not limited separately".
	TotalMaxTokens int `json:"total_max_tokens,omitempty"`
	MaxConcurrency int `json:"max_concurrency"`
	// CostBasis names what the money columns of this ledger MEAN.
	//
	// It is the difference between "this call cost nothing" and "this
	// ledger computes no amount". A ledger without a price basis writes 0 in
	// the money columns because nothing was priced, and a reader who does not
	// see the basis would read that 0 as a price.
	//
	// It is empty in a ledger written before the basis existed, which reads
	// as the DeepSeek peak-USD basis those ledgers were written under.
	CostBasis          string    `json:"cost_basis,omitempty"`
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
	Number          int    `json:"number"`
	Model           string `json:"model"`
	Status          string `json:"status"`
	InputUpperBound int    `json:"input_upper_bound_tokens"`
	OutputLimit     int    `json:"output_limit_tokens"`
	InputTokens     int    `json:"input_tokens,omitempty"`
	OutputTokens    int    `json:"output_tokens,omitempty"`
	// TotalTokens is the total token count the provider reported.
	//
	// It is 0 when the provider reported no total; the input and output
	// counters are then the provider's own numbers and the total is their
	// sum. It is never an estimate of a request nobody made.
	TotalTokens int `json:"total_tokens,omitempty"`
	// UsageStatus says whether the token counters came from the provider.
	//
	// "reported" means they are the provider's numbers. "unavailable" means
	// the provider reported no usage: the counters are then empty, because
	// an estimate written as an actual usage is a false record.
	//
	// It is empty in a ledger written before the status existed, which reads
	// as "reported" -- those ledgers only ever recorded provider numbers.
	UsageStatus string `json:"usage_status,omitempty"`
	// CostBasis names what this request's money columns mean. See the
	// ledger-level field of the same name.
	CostBasis       string                            `json:"cost_basis,omitempty"`
	ReservedNanoCNY int64                             `json:"reserved_nano_cny"`
	ActualNanoCNY   int64                             `json:"actual_nano_cny,omitempty"`
	ReservedNanoUSD int64                             `json:"reserved_nano_usd,omitempty"`
	ActualNanoUSD   int64                             `json:"actual_nano_usd,omitempty"`
	FinishReason    string                            `json:"finish_reason,omitempty"`
	CreatedAt       time.Time                         `json:"created_at"`
	Reconciliation  *EmbeddingHTTPErrorReconciliation `json:"reconciliation,omitempty"`
}

// Usage is the token usage one provider call reported.
//
// Reported says whether the provider answered with usage at all. A call whose
// provider reported nothing carries no counters: the ledger records
// "unavailable" instead of an estimate.
type Usage struct {
	InputTokens  int
	OutputTokens int
	TotalTokens  int
	Reported     bool
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
	if profile == "" || strings.TrimSpace(os.Getenv(ProfileEnv)) != profile || !SupportedProfile(profile) {
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
	if b == nil || !SupportedProfile(b.profile) || !filepath.IsAbs(b.ledgerPath) {
		return nil, ErrUnavailable
	}
	inputMax, outputMax, totalMax := ProfileTokenLimits(b.profile)
	if inputUpperBound < 0 || inputUpperBound > inputMax || outputLimit < 0 || outputLimit > outputMax {
		return nil, ErrInputTooLarge
	}
	if inputUpperBound+outputLimit > totalMax {
		return nil, ErrInputTooLarge
	}
	// The profile's model allowlist is the gate: a model the profile does not
	// approve never reaches the provider, whatever the caller asked for.
	if !ProfileAllowsModel(b.profile, model) {
		return nil, ErrWrongModel
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
	ledger, err := readLedger(b.ledgerPath, b.profile)
	if err != nil {
		_ = ReleaseLock(lock, lockPath)
		return nil, ErrUnavailable
	}
	if migrated, ok := migrateLedgerProfile(ledger, b.profile); ok {
		ledger = migrated
		if err := writeLedger(b.ledgerPath, ledger); err != nil {
			_ = ReleaseLock(lock, lockPath)
			return nil, ErrUnavailable
		}
	}
	if !validLedger(ledger, b.profile) {
		_ = ReleaseLock(lock, lockPath)
		return nil, ErrUnavailable
	}
	if ledger.Blocked || hasUnresolved(ledger) {
		_ = ReleaseLock(lock, lockPath)
		return nil, ErrBlocked
	}
	if (ledger.MaxCalls > 0 && len(ledger.Requests) >= ledger.MaxCalls) || reserveNanoCNY > ledger.MaxReserveNanoCNY-ledger.ReservedNanoCNY {
		_ = ReleaseLock(lock, lockPath)
		return nil, ErrCallsSpent
	}
	request := Request{
		Number:          len(ledger.Requests) + 1,
		Model:           model,
		Status:          "in_flight",
		InputUpperBound: inputUpperBound,
		OutputLimit:     outputLimit,
		CostBasis:       ProfileCostBasis(b.profile),
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

// Complete records the outcome of one call whose provider reported its usage.
//
// The token counters it writes are the provider's own numbers, and the money
// it writes is computed from the profile's price basis.
func (r *Reservation) Complete(status string, inputTokens, outputTokens int, finishReason string, block bool) error {
	return r.CompleteUsage(status, Usage{
		InputTokens:  inputTokens,
		OutputTokens: outputTokens,
		TotalTokens:  inputTokens + outputTokens,
		Reported:     true,
	}, finishReason, block)
}

// CompleteUsage records the outcome of one call together with the usage the
// provider reported.
//
// The counters are written as they are given. Reported says whether they came
// from the provider: a call whose provider reported no usage is recorded as
// "unavailable", and the caller leaves the counters empty rather than filling
// them with an estimate.
func (r *Reservation) CompleteUsage(status string, usage Usage, finishReason string, block bool) error {
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
	inputTokens, outputTokens := usage.InputTokens, usage.OutputTokens
	totalTokens := usage.TotalTokens
	if totalTokens <= 0 && usage.Reported {
		// The provider reported no total of its own: the total is the sum of
		// the two counters it did report. It is arithmetic on reported
		// numbers, not an estimate of the request.
		totalTokens = inputTokens + outputTokens
	}
	if !usage.Reported {
		totalTokens = 0
	}
	actualNanoCNY, actualNanoUSD, costErr := actualAmounts(r.model, inputTokens, outputTokens)
	_, _, totalMax := ProfileTokenLimits(r.budget.profile)
	if costErr != nil || inputTokens < 0 || outputTokens < 0 || inputTokens > request.InputUpperBound ||
		outputTokens > request.OutputLimit || inputTokens+outputTokens > totalMax {
		block = true
		status = "limit_violation"
		if costErr != nil {
			actualNanoCNY, actualNanoUSD = request.ReservedNanoCNY+1, request.ReservedNanoUSD+1
		}
	}
	request.Status = status
	request.InputTokens = inputTokens
	request.OutputTokens = outputTokens
	request.TotalTokens = totalTokens
	if usage.Reported {
		request.UsageStatus = UsageStatusReported
	} else {
		request.UsageStatus = UsageStatusUnavailable
	}
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

// Unknown records a call whose outcome could not be confirmed.
//
// A profile with a price basis keeps charging the full reserved upper bound:
// that is the priced profile's rule and its reconciliation depends on it. A
// profile without a price basis writes no counters at all, because the
// estimate of a request whose usage nobody saw is not a usage.
func (r *Reservation) Unknown(status string) error {
	if r == nil {
		return ErrUnavailable
	}
	if !ProfilePriced(r.budget.profile) {
		return r.CompleteUsage(status, Usage{Reported: false}, "", true)
	}
	return r.CompleteUsage(status, Usage{InputTokens: r.upperInputBound(), Reported: false}, "", true)
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
	case QwenChatModel:
		// The Qwen profile has no price basis: no amount is computed here.
		// The 0 is recorded under the "not_applicable" cost basis, so it
		// reads as "not computed" and never as "the call was free".
		return 0, 0, nil
	case QwenEmbedding, QwenEmbeddingFlash:
		if outputLimit != 0 {
			return 0, 0, ErrWrongModel
		}
		price, _ := EmbeddingInputNanoCNYPerToken(model)
		return int64(inputUpperBound) * price, 0, nil
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
	case QwenChatModel:
		// No price basis: the actual amount is not computed, and the ledger
		// says so through its cost basis.
		return 0, 0, nil
	case QwenEmbedding, QwenEmbeddingFlash:
		if outputTokens != 0 {
			return 0, 0, ErrWrongModel
		}
		price, _ := EmbeddingInputNanoCNYPerToken(model)
		return int64(inputTokens) * price, 0, nil
	default:
		return 0, 0, ErrWrongModel
	}
}

func SupportedProfile(profile string) bool {
	return profile == Profile || profile == CostOnlyProfile || profile == QwenProfile
}

// ProfileLimits retains the original six-call profile and makes zero an explicit
// no-count-limit value for the separately authorized cost-only profile.
//
// The Qwen profile is the third shape: it allows exactly one call and no
// money at all. Its nano-CNY ceiling is 0 because the profile computes no
// amount -- the zero is "no price basis", not "no budget".
func ProfileLimits(profile string) (calls int, nanoCNY int64, backstopCNY int) {
	switch profile {
	case CostOnlyProfile:
		return 0, CostOnlyMaxBudgetNanoCNY, 20
	case QwenProfile:
		return QwenMaxCalls, 0, 0
	default:
		return MaxCalls, MaxBudgetNanoCNY, HumanBackstopCNY
	}
}

func freshLedger(profile string) Ledger {
	calls, budget, _ := ProfileLimits(profile)
	inputMax, outputMax, totalMax := ProfileTokenLimits(profile)
	fxReserve := int(DeepSeekBudgetFXNanoCNYPerUSD)
	if !ProfilePriced(profile) {
		fxReserve = 0
	}
	return Ledger{
		SchemaVersion:      ledgerVersion,
		Profile:            profile,
		MaxCalls:           calls,
		InputMaxTokens:     inputMax,
		OutputMaxTokens:    outputMax,
		TotalMaxTokens:     totalMax,
		MaxConcurrency:     MaxConcurrency,
		CostBasis:          ProfileCostBasis(profile),
		FXReserveCNYPerUSD: fxReserve,
		MaxReserveNanoCNY:  budget,
		Requests:           []Request{},
	}
}

func readLedger(path string, profiles ...string) (Ledger, error) {
	profile := Profile
	if len(profiles) > 0 {
		profile = profiles[0]
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return freshLedger(profile), nil
	}
	if err != nil {
		return Ledger{}, err
	}
	return decodeLedger(data)
}

// migrateLedgerProfile upgrades only a fully validated, settled legacy ledger.
// Unknown, blocked, or otherwise inconsistent history is deliberately left
// untouched so profile changes cannot erase an unresolved provider call.
func migrateLedgerProfile(ledger Ledger, target string) (Ledger, bool) {
	if ledger.Profile == target || target != CostOnlyProfile || ledger.Profile != Profile ||
		ledger.Blocked || hasUnresolved(ledger) || !validLedger(ledger, Profile) {
		return ledger, false
	}
	migrated := ledger
	migrated.Profile = target
	migrated.MaxCalls, migrated.MaxReserveNanoCNY, _ = ProfileLimits(target)
	if !validLedger(migrated, target) {
		return ledger, false
	}
	return migrated, true
}

func validLedger(ledger Ledger, profile string) bool {
	calls, budget, _ := ProfileLimits(profile)
	inputMax, outputMax, totalMax := ProfileTokenLimits(profile)
	costBasis := ProfileCostBasis(profile)
	fxReserve := int(DeepSeekBudgetFXNanoCNYPerUSD)
	if !ProfilePriced(profile) {
		fxReserve = 0
	}
	if !SupportedProfile(profile) || ledger.SchemaVersion != ledgerVersion || ledger.Profile != profile || ledger.MaxCalls != calls ||
		ledger.InputMaxTokens != inputMax || ledger.OutputMaxTokens != outputMax || ledger.MaxConcurrency != MaxConcurrency ||
		ledger.FXReserveCNYPerUSD != fxReserve || ledger.MaxReserveNanoCNY != budget ||
		!validLedgerTotalMax(ledger, profile, totalMax) || !validLedgerCostBasis(ledger.CostBasis, profile, costBasis) ||
		(calls > 0 && len(ledger.Requests) > calls) || ledger.ReservedNanoCNY < 0 || ledger.ReservedNanoCNY > budget ||
		ledger.ActualNanoCNY < 0 || ledger.ActualNanoCNY > ledger.ReservedNanoCNY || ledger.ReservedNanoUSD < 0 ||
		ledger.ActualNanoUSD < 0 || ledger.ActualNanoUSD > ledger.ReservedNanoUSD {
		return false
	}
	var reserveCNY, actualCNY, reserveUSD, actualUSD int64
	for index, request := range ledger.Requests {
		if !ProfileAllowsModel(profile, request.Model) {
			return false
		}
		if request.Reconciliation != nil && (profile != CostOnlyProfile || !validEmbeddingHTTPErrorReconciliation(request)) {
			return false
		}
		if request.Number != index+1 || request.InputUpperBound < 0 || request.InputUpperBound > inputMax ||
			request.OutputLimit < 0 || request.OutputLimit > outputMax || request.InputTokens < 0 || request.OutputTokens < 0 ||
			request.TotalTokens < 0 || request.InputTokens+request.OutputTokens > totalMax {
			return false
		}
		if !validUsageStatus(request.UsageStatus) || !validLedgerCostBasis(request.CostBasis, profile, costBasis) {
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
		if requestUnresolved(request) {
			if !ledger.Blocked {
				return false
			}
		}
		reserveCNY += request.ReservedNanoCNY
		actualCNY += request.ActualNanoCNY
		reserveUSD += request.ReservedNanoUSD
		actualUSD += request.ActualNanoUSD
	}
	return reserveCNY == ledger.ReservedNanoCNY && actualCNY == ledger.ActualNanoCNY && reserveUSD == ledger.ReservedNanoUSD && actualUSD == ledger.ActualNanoUSD && consistentRecordedCosts(ledger)
}

// validLedgerTotalMax accepts the token ceiling of the profile.
//
// A ledger written before the ceiling existed carries 0, which reads as "the
// sum was not limited separately". The Qwen profile has no such legacy
// ledgers, so it insists on the ceiling it was created with.
func validLedgerTotalMax(ledger Ledger, profile string, totalMax int) bool {
	if ledger.TotalMaxTokens == totalMax {
		return true
	}
	return profile != QwenProfile && ledger.TotalMaxTokens == 0
}

// validLedgerCostBasis accepts the cost basis of the profile.
//
// An empty basis is a ledger written before the basis existed; it reads as
// the priced basis those ledgers were written under. A profile without a
// price basis has no legacy ledgers and must carry its own basis.
func validLedgerCostBasis(recorded, profile, costBasis string) bool {
	if recorded == costBasis {
		return true
	}
	return profile != QwenProfile && recorded == ""
}

func validUsageStatus(status string) bool {
	switch status {
	case "", UsageStatusReported, UsageStatusUnavailable:
		return true
	default:
		return false
	}
}

func hasUnresolved(ledger Ledger) bool {
	for _, request := range ledger.Requests {
		if request.Reconciliation != nil && ledger.Profile != CostOnlyProfile {
			return true
		}
		if requestUnresolved(request) {
			return true
		}
	}
	return false
}

// LockLeaseTTL is diagnostic only for old/incomplete markers. A live v2 owner
// holds an OS guard throughout Reserve/Complete, even after its mtime expires.
const LockLeaseTTL = 10 * time.Minute

var (
	lockLeaseHeartbeatInterval = LockLeaseTTL / 3
	activeLockLeases           sync.Map // map[*os.File]*lockLease
)

type lockLease struct {
	stop  chan struct{}
	done  chan struct{}
	guard *os.File
}

// AcquireLock serializes the complete reservation with an OS-owned guard. It
// never reclaims a live descriptor on the strength of a timestamp. Old binary
// callers must be quiesced before switching this ledger to the v2 protocol.
func AcquireLock(ctx context.Context, path string) (*os.File, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	guard, err := acquireProcessGuard(ctx, path+".guard")
	if err != nil {
		return nil, err
	}
	keepGuard := false
	defer func() {
		if !keepGuard {
			_ = releaseProcessGuard(guard)
		}
	}()
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		lock, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			if _, writeErr := fmt.Fprintf(lock, "%spid=%d acquired_at=%s\n", guardedLockProtocol, os.Getpid(), time.Now().UTC().Format(time.RFC3339Nano)); writeErr != nil {
				_ = lock.Close()
				_ = os.Remove(path)
				return nil, writeErr
			}
			startLockLease(lock, guard)
			keepGuard = true
			return lock, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, err
		}
		recovered, err := recoverGuardedMarker(path)
		if err != nil {
			return nil, err
		}
		if recovered {
			continue
		}
		if err := waitForLock(ctx); err != nil {
			return nil, err
		}
	}
}

func lockLeaseExpiredInfo(info os.FileInfo) bool {
	return info != nil && time.Now().UTC().After(info.ModTime().Add(LockLeaseTTL))
}

// Only call while holding path+".guard". Since all v2 owners hold that guard
// until their final ledger write, a complete v2 marker here belongs to an exited
// owner. Unknown/old/partial markers are never deleted by age.
func recoverGuardedMarker(path string) (bool, error) {
	marker, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	header := make([]byte, len(guardedLockProtocol))
	_, readErr := io.ReadFull(marker, header)
	info, statErr := marker.Stat()
	closeErr := marker.Close()
	if statErr != nil || closeErr != nil {
		return false, errors.Join(statErr, closeErr)
	}
	if readErr != nil || string(header) != guardedLockProtocol {
		if lockLeaseExpiredInfo(info) {
			return false, ErrLegacyLock
		}
		return false, nil
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	return true, nil
}

func startLockLease(lock, guard *os.File) {
	lease := &lockLease{stop: make(chan struct{}), done: make(chan struct{}), guard: guard}
	interval := lockLeaseHeartbeatInterval
	if interval <= 0 {
		interval = time.Second
	}
	activeLockLeases.Store(lock, lease)
	go func() {
		defer close(lease.done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-lease.stop:
				return
			case <-ticker.C:
				if _, err := fmt.Fprintf(lock, "heartbeat=%s\n", time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
					return
				}
				_ = lock.Sync()
			}
		}
	}()
}

func stopLockLease(lock *os.File) *lockLease {
	value, ok := activeLockLeases.LoadAndDelete(lock)
	if !ok {
		return nil
	}
	lease := value.(*lockLease)
	close(lease.stop)
	<-lease.done
	return lease
}

// ReleaseLock removes only this owner marker, while still holding the stable
// guard. Closing/unlocking the guard happens after the final write and cleanup.
// A nil/foreign/repeated release cannot delete another owner's marker.
func ReleaseLock(lock *os.File, path string) (result error) {
	if lock == nil {
		return nil
	}
	if filepath.Clean(path) != filepath.Clean(lock.Name()) {
		return ErrUnavailable
	}
	lease := stopLockLease(lock)
	if lease == nil {
		return ErrUnavailable
	}
	defer func() { result = errors.Join(result, releaseProcessGuard(lease.guard)) }()
	owned, err := lock.Stat()
	if err != nil {
		_ = lock.Close()
		return err
	}
	if err := lock.Close(); err != nil {
		return err
	}
	current, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !os.SameFile(owned, current) {
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
