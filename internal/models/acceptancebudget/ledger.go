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
	// ErrTokenBudgetExhausted means the CUMULATIVE token reservation of the
	// window would pass the profile's cumulative ceiling.
	//
	// It is a refusal that happens BEFORE the request leaves the process,
	// and it is separate from ErrCallsSpent: the call count may still have
	// room while the token budget does not, and a reader needs to know which
	// of the two stopped the request.
	ErrTokenBudgetExhausted = errors.New("model acceptance cumulative token budget exhausted")
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
	// CumulativeTokenMax is the ceiling of the CUMULATIVE token reservation
	// of this window.
	//
	// It is the profile's ceiling. It is 0 in a ledger of a profile that has
	// no cumulative token ceiling, and in a ledger written before the field
	// existed. A ledger may TIGHTEN it (an operator can lower one window's
	// budget) but it may never RAISE it: the profile's number is the hard
	// maximum.
	CumulativeTokenMax int `json:"cumulative_token_max,omitempty"`
	// ReservedTokens is the cumulative token RESERVATION of this window.
	//
	// It is a reservation, not a usage: it says how many tokens the window
	// could still consume, never how many it did consume. The provider's own
	// numbers live in the request rows (input_tokens / output_tokens /
	// total_tokens) and in ActualTokens below.
	//
	// It is 0 in a ledger of a profile that has no cumulative token ceiling,
	// so such a ledger stays exactly as it was before the field existed.
	ReservedTokens int `json:"reserved_tokens,omitempty"`
	// ActualTokens is the cumulative token usage the provider REPORTED.
	//
	// It is the sum of the reported totals. It excludes any call whose usage
	// was unavailable, because nobody can prove what that call consumed; the
	// request rows say which calls those were through their usage_status.
	//
	// It is never derived from the reservation, and the reservation is never
	// derived from it.
	ActualTokens   int `json:"actual_tokens,omitempty"`
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
	Number int    `json:"number"`
	Model  string `json:"model"`
	Status string `json:"status"`
	// InputUpperBound is the input bound this request reserved against.
	//
	// Its dimension is a UTF-8 BYTE LENGTH plus a fixed overhead: it is an
	// UPPER BOUND of the token count and NEVER a token count. A token is at
	// least one byte, so the byte length of a prompt can never be smaller
	// than its token count.
	//
	// The historical JSON key says "tokens" and stays as it is, because an
	// old ledger file has to keep reading exactly as it did. The dimension is
	// stated here, next to the field, instead of in the key.
	//
	// This is the quantity the input-ceiling criterion of the authoritative
	// checklist measures the provider's reported input tokens against. See
	// EvaluateCallLimits.
	InputUpperBound int `json:"input_upper_bound_tokens"`
	// OutputLimit is the output limit this request carried.
	//
	// Its dimension is TOKENS, and it is exact: it is the
	// max_completion_tokens that was sent to the provider.
	//
	// This is the quantity the output-ceiling criterion of the authoritative
	// checklist measures the provider's reported output tokens against. See
	// EvaluateCallLimits.
	OutputLimit  int `json:"output_limit_tokens"`
	InputTokens  int `json:"input_tokens,omitempty"`
	OutputTokens int `json:"output_tokens,omitempty"`
	// TotalTokens is the total token count the provider reported.
	//
	// It is 0 when the provider reported no total; the input and output
	// counters are then the provider's own numbers and the total is their
	// sum. It is never an estimate of a request nobody made.
	TotalTokens int `json:"total_tokens,omitempty"`
	// UsageStatus says whether the token counters came from the provider.
	//
	// "reported" means they are the provider's numbers. "unavailable" means
	// the provider reported no usage: the amount is 不可核实 (unverifiable),
	// the counters are then empty, and an estimate is never written as an
	// actual usage.
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
	// The cumulative token gate.
	//
	// The reservation of THIS call is the conservative token bound of the
	// call: the input upper bound plus the output limit that travelled in
	// the request.
	//
	// The input upper bound is NOT an exact token count: it is a UTF-8 BYTE
	// length (plus a fixed overhead) that the caller computed before the
	// request, and a token is at least one byte, so the byte length can
	// never be smaller than the token count. It is an UPPER BOUND, which
	// makes the reservation conservative: it may refuse a request that would
	// have fitted, and it can never let one through that exceeds the
	// ceiling. The output limit IS exact: it is the max_completion_tokens
	// the provider was asked for.
	//
	// The check happens HERE, while the ledger lock is held and BEFORE the
	// caller receives a reservation, so a request that does not fit never
	// leaves the process.
	reservedTokens := inputUpperBound + outputLimit
	if ledger.tracksCumulativeTokens() {
		if ledger.ReservedTokens+reservedTokens > ledger.CumulativeTokenMax {
			_ = ReleaseLock(lock, lockPath)
			return nil, ErrTokenBudgetExhausted
		}
		if ledgerUsageUnverifiable(ledger) {
			// The remaining budget cannot be PROVEN. Nobody knows what the
			// unaccounted call consumed, so the window cannot promise that
			// the next call stays inside the ceiling. A window whose usage
			// is unverifiable stops here rather than spending on a guess.
			_ = ReleaseLock(lock, lockPath)
			return nil, ErrTokenBudgetExhausted
		}
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
	if ledger.tracksCumulativeTokens() {
		ledger.ReservedTokens += reservedTokens
	}
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
	// The authoritative checklist decides whether this call exceeded a limit.
	// The shared budget ledger and the round ledger read the SAME checklist,
	// so one and the same answer cannot be written down as a violation in one
	// ledger and as a normal response in the other.
	verdict := EvaluateCallLimits(r.budget.profile, CallFacts{
		InputUpperBound: request.InputUpperBound,
		OutputLimit:     request.OutputLimit,
		InputTokens:     inputTokens,
		OutputTokens:    outputTokens,
	})
	if costErr != nil || verdict.Violated {
		// The ceilings above are checked BEFORE the request as well: the
		// reservation refuses an oversized input, and the output ceiling
		// travels in the request. This block only accounts for what the
		// provider reported: the call has already happened, so it can mark
		// the record and freeze the ledger, but it cannot undo the call.
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
	if ledger.tracksCumulativeTokens() {
		// The reported usage is the truth about the tokens this call
		// consumed. It is recorded NEXT TO the reservation, never in place
		// of it, and it never feeds back into the reservation.
		ledger.ActualTokens += totalTokens
	}
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
	if ledger.tracksCumulativeTokens() {
		// The call provably never happened, so its reservation goes back.
		// The counter is DECREMENTED, never reset: the other reservations of
		// the window stay counted.
		ledger.ReservedTokens -= request.InputUpperBound + request.OutputLimit
	}
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
	if model == QwenChatModel {
		// This model has no price basis, so there is no amount to compute --
		// not even for a counter that cannot be true. Returning the priced
		// profile's error here would make the caller write a reserved+1
		// amount onto a row that reserved nothing, which is exactly the
		// "reserved 0 but actual is an amount" combination the cost basis
		// exists to prevent. The impossible counter is caught by the caller
		// as a limit violation; the amount stays 0, which under the
		// "not_applicable" basis reads as "not computed", never as "free".
		return 0, 0, nil
	}
	if inputTokens < 0 || outputTokens < 0 {
		return 0, 0, ErrInputTooLarge
	}
	switch model {
	case DeepSeekModel:
		usd := int64(inputTokens)*DeepSeekInputNanoUSDPerToken + int64(outputTokens)*DeepSeekOutputNanoUSDPerToken
		return usd * DeepSeekBudgetFXNanoCNYPerUSD, usd, nil
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
	_, ok := profileConfigs[profile]
	return ok
}

// ProfileLimits retains the original six-call profile and makes zero an explicit
// no-count-limit value for the separately authorized cost-only profile.
//
// The Qwen profiles are the third shape: they control the call count and the
// tokens, and no money at all. Their nano-CNY ceiling is 0 because the
// profiles compute no amount -- the zero is "no price basis", not "no budget".
func ProfileLimits(profile string) (calls int, nanoCNY int64, backstopCNY int) {
	config := profileConfigOf(profile)
	return config.maxCalls, config.nanoCNY, config.backstopCNY
}

func freshLedger(profile string) Ledger {
	config := profileConfigOf(profile)
	inputMax, outputMax, totalMax := config.inputMax, config.outputMax, config.totalMax
	fxReserve := int(DeepSeekBudgetFXNanoCNYPerUSD)
	if config.costBasis == CostBasisNotApplicable {
		fxReserve = 0
	}
	return Ledger{
		SchemaVersion:      ledgerVersion,
		Profile:            profile,
		MaxCalls:           config.maxCalls,
		InputMaxTokens:     inputMax,
		OutputMaxTokens:    outputMax,
		TotalMaxTokens:     totalMax,
		CumulativeTokenMax: config.cumulativeTokenMax,
		MaxConcurrency:     MaxConcurrency,
		CostBasis:          config.costBasis,
		FXReserveCNYPerUSD: fxReserve,
		MaxReserveNanoCNY:  config.nanoCNY,
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
		!validLedgerCumulativeTokenMax(ledger, profile) ||
		(calls > 0 && len(ledger.Requests) > calls) || ledger.ReservedNanoCNY < 0 || ledger.ReservedNanoCNY > budget ||
		ledger.ActualNanoCNY < 0 || ledger.ActualNanoCNY > ledger.ReservedNanoCNY || ledger.ReservedNanoUSD < 0 ||
		ledger.ActualNanoUSD < 0 || ledger.ActualNanoUSD > ledger.ReservedNanoUSD {
		return false
	}
	var reserveCNY, actualCNY, reserveUSD, actualUSD int64
	var reservedTokens, actualTokens int
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
		// The two token counters are recomputed from the rows, so a ledger
		// whose aggregates disagree with its own history is refused. The
		// reservation and the reported usage are summed separately: they are
		// different quantities and neither one is derived from the other.
		reservedTokens += request.InputUpperBound + request.OutputLimit
		actualTokens += request.TotalTokens
	}
	if ledger.tracksCumulativeTokens() {
		if ledger.ReservedTokens != reservedTokens || ledger.ActualTokens != actualTokens {
			return false
		}
	}
	return reserveCNY == ledger.ReservedNanoCNY && actualCNY == ledger.ActualNanoCNY && reserveUSD == ledger.ReservedNanoUSD && actualUSD == ledger.ActualNanoUSD && consistentRecordedCosts(ledger)
}

// validLedgerCumulativeTokenMax accepts the cumulative token ceiling of one
// ledger.
//
// The profile's number is the HARD MAXIMUM: a ledger may tighten it (an
// operator can lower one window's budget) but it may never raise it, and a
// profile that has a ceiling does not accept a ledger that declares none --
// such a window would run without the gate.
func validLedgerCumulativeTokenMax(ledger Ledger, profile string) bool {
	ceiling := ProfileCumulativeTokenMax(profile)
	if ceiling <= 0 {
		return ledger.CumulativeTokenMax == 0
	}
	return ledger.CumulativeTokenMax > 0 && ledger.CumulativeTokenMax <= ceiling
}

// tracksCumulativeTokens reports whether this ledger keeps the cumulative
// token counters.
//
// The counters exist for the profiles that HAVE a cumulative token ceiling.
// A profile without one does not keep them, so its ledger stays exactly as
// it was before the counters existed.
func (l Ledger) tracksCumulativeTokens() bool {
	return l.CumulativeTokenMax > 0
}

// ledgerUsageUnverifiable reports whether any recorded call has a usage that
// could not be verified.
//
// Such a call is not a smaller usage: it is an UNKNOWN usage. The window
// cannot promise that the next call stays inside the ceiling, because the
// ceiling has to be measured against a number nobody can produce.
func ledgerUsageUnverifiable(ledger Ledger) bool {
	for _, request := range ledger.Requests {
		if request.UsageStatus == UsageStatusUnavailable {
			return true
		}
	}
	return false
}

// validLedgerTotalMax accepts the token ceiling of the profile.
//
// A ledger written before the ceiling existed carries 0, which reads as "the
// sum was not limited separately". The Qwen profiles have no such legacy
// ledgers, so they insist on the ceiling they were created with.
func validLedgerTotalMax(ledger Ledger, profile string, totalMax int) bool {
	if ledger.TotalMaxTokens == totalMax {
		return true
	}
	return !IsQwenProfile(profile) && ledger.TotalMaxTokens == 0
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
	return !IsQwenProfile(profile) && recorded == ""
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
