package acceptancebudget

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	embeddingHTTPErrorReconciliationKind = "embedding_http_error_full_reserve_v1"
	fullReserveChargeBasis               = "full_reserved_upper_bound_not_provider_usage"
	unknownProviderUsage                 = "unknown"
)

// EmbeddingHTTPErrorEvidence is a local operator attestation tied to one exact
// ledger snapshot and request. ResponseEvidenceSHA256 identifies the reviewed
// HTTP error artifact; it does not claim the provider supplied billing usage.
type EmbeddingHTTPErrorEvidence struct {
	LedgerSHA256           string `json:"ledger_sha256"`
	RequestNumber          int    `json:"request_number"`
	OriginalStatus         string `json:"original_status"`
	OriginalBlockedReason  string `json:"original_blocked_reason"`
	HTTPStatus             int    `json:"http_status"`
	ResponseEvidenceSHA256 string `json:"response_evidence_sha256"`
	Operator               string `json:"operator"`
	Reason                 string `json:"reason"`
}

type EmbeddingHTTPErrorReconciliation struct {
	Kind                  string                     `json:"kind"`
	Evidence              EmbeddingHTTPErrorEvidence `json:"evidence"`
	OriginalRequestSHA256 string                     `json:"original_request_sha256"`
	ChargeBasis           string                     `json:"charge_basis"`
	ProviderTokenUsage    string                     `json:"provider_token_usage"`
	ChargedNanoCNY        int64                      `json:"charged_nano_cny"`
	ChargedNanoUSD        int64                      `json:"charged_nano_usd"`
	ReconciledAt          time.Time                  `json:"reconciled_at"`
}

type ReconcileEmbeddingHTTPErrorInput struct {
	ExpectedLedgerSHA256     string
	RequestNumber            int
	ExpectedStatus           string
	ExpectedBlockedReason    string
	ExpectedReason           string
	ConfirmFullReserveCharge bool
	Evidence                 EmbeddingHTTPErrorEvidence
}

// ReconcileEmbeddingHTTPError is an explicit local operator operation. No
// provider/client/HTTP path calls it. It preserves the failed request, including
// unknown token counters, and never refunds either reserved or actual cost.
//
// It is available on the profiles that price embedding calls (the
// cost-only profile and the controlled embedding profile), because a provider
// 4xx on such a call freezes the window and the authorized budget would
// otherwise be retired by the FIRST failure. See
// ProfileSupportsEmbeddingHTTPErrorReconciliation for what the capability
// means and what it deliberately does not cover.
func (b *Budget) ReconcileEmbeddingHTTPError(ctx context.Context, input ReconcileEmbeddingHTTPErrorInput) error {
	if b == nil || !ProfileSupportsEmbeddingHTTPErrorReconciliation(b.profile) || !filepath.IsAbs(b.ledgerPath) ||
		!input.ConfirmFullReserveCharge || !validSHA256(input.ExpectedLedgerSHA256) ||
		input.RequestNumber < 1 || input.ExpectedStatus != "http_error" || input.ExpectedBlockedReason != "http_error" ||
		!validEvidence(input.Evidence) || input.Evidence.LedgerSHA256 != input.ExpectedLedgerSHA256 ||
		input.Evidence.RequestNumber != input.RequestNumber || input.Evidence.Reason != input.ExpectedReason {
		return ErrUnavailable
	}
	lockPath := b.ledgerPath + ".lock"
	lock, err := AcquireLock(ctx, lockPath)
	if err != nil {
		return ErrUnavailable
	}
	defer func() { _ = ReleaseLock(lock, lockPath) }()
	data, err := os.ReadFile(b.ledgerPath)
	if err != nil || digestSHA256(data) != input.ExpectedLedgerSHA256 {
		return ErrUnavailable
	}
	ledger, err := decodeLedger(data)
	if err != nil || !validLedger(ledger, b.profile) || !consistentRecordedCosts(ledger) ||
		!ledger.Blocked || ledger.BlockedReason != input.ExpectedBlockedReason || input.RequestNumber != len(ledger.Requests) {
		return ErrUnavailable
	}
	request := &ledger.Requests[input.RequestNumber-1]
	if request.Status != input.ExpectedStatus || request.Reconciliation != nil ||
		!fullReserveEmbeddingHTTPError(*request) {
		return ErrUnavailable
	}
	for index, other := range ledger.Requests {
		if index != input.RequestNumber-1 && requestUnresolved(other) {
			return ErrBlocked
		}
	}
	requestHash := requestSHA256(*request)
	request.Reconciliation = &EmbeddingHTTPErrorReconciliation{
		Kind: embeddingHTTPErrorReconciliationKind, Evidence: input.Evidence,
		OriginalRequestSHA256: requestHash, ChargeBasis: fullReserveChargeBasis,
		ProviderTokenUsage: unknownProviderUsage, ChargedNanoCNY: request.ReservedNanoCNY,
		ChargedNanoUSD: request.ReservedNanoUSD, ReconciledAt: time.Now().UTC(),
	}
	// Amounts and the original status/token fields are deliberately unchanged.
	ledger.Blocked = false
	ledger.BlockedReason = ""
	if !validLedger(ledger, b.profile) || hasUnresolved(ledger) {
		return ErrBlocked
	}
	if err := writeLedger(b.ledgerPath, ledger); err != nil {
		return ErrUnavailable
	}
	return nil
}

func fullReserveEmbeddingHTTPError(request Request) bool {
	price, allowed := EmbeddingInputNanoCNYPerToken(request.Model)
	return allowed && request.Status == "http_error" && request.OutputLimit == 0 &&
		request.InputUpperBound > 0 && request.InputUpperBound <= InputMax &&
		request.InputTokens == request.InputUpperBound && request.OutputTokens == 0 && request.FinishReason == "" &&
		!request.CreatedAt.IsZero() && request.ActualNanoCNY == request.ReservedNanoCNY &&
		request.ReservedNanoCNY == int64(request.InputUpperBound)*price &&
		request.ActualNanoUSD == request.ReservedNanoUSD && request.ReservedNanoUSD == 0
}

func validEmbeddingHTTPErrorReconciliation(request Request) bool {
	r := request.Reconciliation
	return r != nil && fullReserveEmbeddingHTTPError(request) && r.Kind == embeddingHTTPErrorReconciliationKind &&
		validEvidence(r.Evidence) && r.Evidence.RequestNumber == request.Number &&
		validSHA256(r.OriginalRequestSHA256) && r.OriginalRequestSHA256 == requestSHA256(request) &&
		r.ChargeBasis == fullReserveChargeBasis && r.ProviderTokenUsage == unknownProviderUsage &&
		r.ChargedNanoCNY == request.ReservedNanoCNY && r.ChargedNanoUSD == request.ReservedNanoUSD &&
		!r.ReconciledAt.IsZero() && !r.ReconciledAt.Before(request.CreatedAt)
}

func validEvidence(e EmbeddingHTTPErrorEvidence) bool {
	return validSHA256(e.LedgerSHA256) && e.RequestNumber > 0 && e.OriginalStatus == "http_error" &&
		e.OriginalBlockedReason == "http_error" && e.HTTPStatus >= 400 && e.HTTPStatus <= 599 &&
		validSHA256(e.ResponseEvidenceSHA256) && validText(e.Operator, 128) && validText(e.Reason, 2048)
}

func validText(value string, limit int) bool {
	return strings.TrimSpace(value) == value && value != "" && len(value) <= limit && !strings.ContainsAny(value, "\x00\r\n")
}

func validSHA256(value string) bool {
	if len(value) != 64 || value != strings.ToLower(value) {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func digestSHA256(data []byte) string {
	value := sha256.Sum256(data)
	return hex.EncodeToString(value[:])
}

func requestSHA256(request Request) string {
	request.Reconciliation = nil
	request.UnresolvedReconciliation = nil
	data, err := json.Marshal(request)
	if err != nil {
		return ""
	}
	return digestSHA256(data)
}

func requestUnresolved(request Request) bool {
	switch request.Status {
	case "http_error":
		return !validEmbeddingHTTPErrorReconciliation(request)
	case "in_flight", "outcome_unknown", "usage_unknown", "limit_violation", "cancel_reconciliation_required":
		// A row an operator settled is resolved; a row whose settlement is
		// missing, incomplete or tampered with is NOT: the window stays
		// frozen rather than opening on a proof nobody can check.
		if request.UnresolvedReconciliation != nil {
			return !validUnresolvedCallReconciliation(request)
		}
		return true
	default:
		if request.UnresolvedReconciliation != nil {
			return !validUnresolvedCallReconciliation(request)
		}
		return request.Reconciliation != nil && !validEmbeddingHTTPErrorReconciliation(request)
	}
}

func consistentRecordedCosts(ledger Ledger) bool {
	for _, request := range ledger.Requests {
		cny, usd, err := actualAmounts(request.Model, request.InputTokens, request.OutputTokens)
		if err != nil || request.InputTokens > request.InputUpperBound || request.OutputTokens > request.OutputLimit ||
			cny != request.ActualNanoCNY || usd != request.ActualNanoUSD {
			return false
		}
	}
	return true
}

func decodeLedger(data []byte) (Ledger, error) {
	if !uniqueJSONKeys(data) {
		return Ledger{}, ErrUnavailable
	}
	var ledger Ledger
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&ledger); err != nil || ledger.SchemaVersion != ledgerVersion {
		return Ledger{}, ErrUnavailable
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return Ledger{}, ErrUnavailable
	}
	for _, request := range ledger.Requests {
		if request.Reconciliation != nil && (!ProfileSupportsEmbeddingHTTPErrorReconciliation(ledger.Profile) || !validEmbeddingHTTPErrorReconciliation(request)) {
			return Ledger{}, ErrUnavailable
		}
		if request.UnresolvedReconciliation != nil && (!ProfileSupportsUnresolvedCallReconciliation(ledger.Profile) || !validUnresolvedCallReconciliation(request)) {
			return Ledger{}, ErrUnavailable
		}
	}
	return ledger, nil
}

// Require canonical ASCII lowercase schema keys before typed decoding. The
// encoding/json field matcher is case-insensitive (including Unicode folds),
// so literal duplicate detection alone would silently discard alias history.
// Noncanonical historical keys are rejected, never normalized or rewritten.
func uniqueJSONKeys(data []byte) bool {
	decoder := json.NewDecoder(bytes.NewReader(data))
	var visit func() bool
	visit = func() bool {
		token, err := decoder.Token()
		if err != nil {
			return false
		}
		delim, compound := token.(json.Delim)
		if !compound {
			return true
		}
		switch delim {
		case '{':
			keys := map[string]bool{}
			for decoder.More() {
				keyToken, err := decoder.Token()
				if err != nil {
					return false
				}
				key, ok := keyToken.(string)
				if !ok || !canonicalLedgerJSONKey(key) || keys[key] {
					return false
				}
				keys[key] = true
				if !visit() {
					return false
				}
			}
		case '[':
			for decoder.More() {
				if !visit() {
					return false
				}
			}
		default:
			return false
		}
		end, err := decoder.Token()
		return err == nil && ((delim == '{' && end == json.Delim('}')) || (delim == '[' && end == json.Delim(']')))
	}
	if !visit() {
		return false
	}
	_, err := decoder.Token()
	return err == io.EOF
}

func canonicalLedgerJSONKey(key string) bool {
	if len(key) == 0 || key[0] < 'a' || key[0] > 'z' {
		return false
	}
	for index := 1; index < len(key); index++ {
		ch := key[index]
		if (ch < 'a' || ch > 'z') && (ch < '0' || ch > '9') && ch != '_' {
			return false
		}
	}
	return true
}
