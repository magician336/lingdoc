package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/acceptancebudget"
)

func TestOperatorCLIRequiresExplicitApplyBeforeReadingOrCreatingLedger(t *testing.T) {
	ledger := filepath.Join(t.TempDir(), "must-not-exist.json")
	var output bytes.Buffer
	if code := run([]string{"-ledger", ledger}, &output, &output); code != 2 {
		t.Fatalf("exit=%d", code)
	}
	if _, err := os.Stat(ledger); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("CLI without apply touched ledger")
	}
}

func TestOperatorCLISyntheticFullReserveAndDuplicate(t *testing.T) {
	ledger := filepath.Join(t.TempDir(), "synthetic.calls.json")
	b := acceptancebudget.New(acceptancebudget.CostOnlyProfile, ledger)
	r, err := b.Reserve(context.Background(), acceptancebudget.QwenEmbedding, 100, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Unknown("http_error"); !errors.Is(err, acceptancebudget.ErrCallsSpent) {
		t.Fatal(err)
	}
	before, err := os.ReadFile(ledger)
	if err != nil {
		t.Fatal(err)
	}
	sha := sha256.Sum256(before)
	artifact := sha256.Sum256([]byte("synthetic HTTP403 artifact"))
	e := acceptancebudget.EmbeddingHTTPErrorEvidence{LedgerSHA256: hex.EncodeToString(sha[:]), RequestNumber: 1,
		OriginalStatus: "http_error", OriginalBlockedReason: "http_error", HTTPStatus: 403,
		ResponseEvidenceSHA256: hex.EncodeToString(artifact[:]), Operator: "synthetic-CLI-operator", Reason: "Synthetic operator reviewed HTTP403; retain full reserve"}
	evidence := filepath.Join(t.TempDir(), "evidence.json")
	data, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(evidence, data, 0o600); err != nil {
		t.Fatal(err)
	}
	args := []string{"-ledger", ledger, "-expected-ledger-sha256", e.LedgerSHA256, "-request", "1", "-reason", e.Reason, "-evidence", evidence, "-apply", "-confirm-full-reserve-charge"}
	var output bytes.Buffer
	if code := run(args, &output, &output); code != 0 {
		t.Fatalf("CLI exit=%d: %s", code, output.String())
	}
	after, err := os.ReadFile(ledger)
	if err != nil {
		t.Fatal(err)
	}
	var readback acceptancebudget.Ledger
	if err := json.Unmarshal(after, &readback); err != nil {
		t.Fatal(err)
	}
	if readback.Blocked || readback.Requests[0].Status != "http_error" || readback.ActualNanoCNY != 50_000 || readback.ReservedNanoCNY != 50_000 ||
		readback.Requests[0].Reconciliation == nil || readback.Requests[0].Reconciliation.ProviderTokenUsage != "unknown" {
		t.Fatal("CLI altered failure/money/unknown token evidence")
	}
	if code := run(args, &output, &output); code != 1 {
		t.Fatal("duplicate CLI apply accepted")
	}
	final, err := os.ReadFile(ledger)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, final) {
		t.Fatal("duplicate changed ledger bytes")
	}
}

// TestOperatorCLIRecoversTheControlledEmbeddingWindow pins the operator path
// for the controlled embedding profile: the profile that owns the ledger has
// to be named, and the CLI must apply the recovery to THAT ledger.
func TestOperatorCLIRecoversTheControlledEmbeddingWindow(t *testing.T) {
	ledger := filepath.Join(t.TempDir(), "synthetic.calls.json")
	b := acceptancebudget.New(acceptancebudget.QwenEmbeddingProfile, ledger)
	r, err := b.Reserve(context.Background(), acceptancebudget.QwenEmbedding, 100, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Unknown("http_error"); !errors.Is(err, acceptancebudget.ErrCallsSpent) {
		t.Fatal(err)
	}
	before, err := os.ReadFile(ledger)
	if err != nil {
		t.Fatal(err)
	}
	sha := sha256.Sum256(before)
	artifact := sha256.Sum256([]byte("synthetic HTTP403 artifact"))
	e := acceptancebudget.EmbeddingHTTPErrorEvidence{LedgerSHA256: hex.EncodeToString(sha[:]), RequestNumber: 1,
		OriginalStatus: "http_error", OriginalBlockedReason: "http_error", HTTPStatus: 403,
		ResponseEvidenceSHA256: hex.EncodeToString(artifact[:]), Operator: "synthetic-CLI-operator", Reason: "Synthetic operator reviewed HTTP403; retain full reserve"}
	evidence := filepath.Join(t.TempDir(), "evidence.json")
	data, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(evidence, data, 0o600); err != nil {
		t.Fatal(err)
	}
	args := []string{"-ledger", ledger, "-profile", acceptancebudget.QwenEmbeddingProfile,
		"-expected-ledger-sha256", e.LedgerSHA256, "-request", "1", "-reason", e.Reason,
		"-evidence", evidence, "-apply", "-confirm-full-reserve-charge"}
	var output bytes.Buffer
	if code := run(args, &output, &output); code != 0 {
		t.Fatalf("CLI exit=%d: %s", code, output.String())
	}
	after, err := os.ReadFile(ledger)
	if err != nil {
		t.Fatal(err)
	}
	var readback acceptancebudget.Ledger
	if err := json.Unmarshal(after, &readback); err != nil {
		t.Fatal(err)
	}
	if readback.Blocked || readback.Requests[0].Status != "http_error" ||
		readback.ActualNanoCNY != 50_000 || readback.ReservedNanoCNY != 50_000 ||
		readback.Requests[0].Reconciliation == nil ||
		readback.Requests[0].Reconciliation.ProviderTokenUsage != "unknown" {
		t.Fatal("CLI altered failure/money/unknown token evidence")
	}
	if code := run(args, &output, &output); code != 1 {
		t.Fatal("duplicate CLI apply accepted")
	}
	final, err := os.ReadFile(ledger)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, final) {
		t.Fatal("duplicate changed ledger bytes")
	}
}

// TestOperatorCLIRefusesTheWrongProfileForALedger pins that the CLI cannot
// reconcile a ledger with a profile that does not own it: the wrong profile
// would apply another profile's rules to this ledger.
func TestOperatorCLIRefusesTheWrongProfileForALedger(t *testing.T) {
	ledger := filepath.Join(t.TempDir(), "synthetic.calls.json")
	b := acceptancebudget.New(acceptancebudget.QwenEmbeddingProfile, ledger)
	r, err := b.Reserve(context.Background(), acceptancebudget.QwenEmbedding, 100, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Unknown("http_error"); !errors.Is(err, acceptancebudget.ErrCallsSpent) {
		t.Fatal(err)
	}
	before, err := os.ReadFile(ledger)
	if err != nil {
		t.Fatal(err)
	}
	sha := sha256.Sum256(before)
	artifact := sha256.Sum256([]byte("synthetic HTTP403 artifact"))
	e := acceptancebudget.EmbeddingHTTPErrorEvidence{LedgerSHA256: hex.EncodeToString(sha[:]), RequestNumber: 1,
		OriginalStatus: "http_error", OriginalBlockedReason: "http_error", HTTPStatus: 403,
		ResponseEvidenceSHA256: hex.EncodeToString(artifact[:]), Operator: "synthetic-CLI-operator", Reason: "Synthetic operator reviewed HTTP403; retain full reserve"}
	evidence := filepath.Join(t.TempDir(), "evidence.json")
	data, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(evidence, data, 0o600); err != nil {
		t.Fatal(err)
	}
	args := []string{"-ledger", ledger, "-profile", acceptancebudget.CostOnlyProfile,
		"-expected-ledger-sha256", e.LedgerSHA256, "-request", "1", "-reason", e.Reason,
		"-evidence", evidence, "-apply", "-confirm-full-reserve-charge"}
	var output bytes.Buffer
	if code := run(args, &output, &output); code != 1 {
		t.Fatalf("CLI exit=%d: the wrong profile was applied to this ledger", code)
	}
	after, err := os.ReadFile(ledger)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("the refused reconciliation changed ledger bytes")
	}
}

// TestOperatorCLIRefusesAProfileWithoutTheCapability pins that a profile
// without the recovery capability is refused BEFORE any ledger is touched.
func TestOperatorCLIRefusesAProfileWithoutTheCapability(t *testing.T) {
	ledger := filepath.Join(t.TempDir(), "must-not-change.json")
	var output bytes.Buffer
	if code := run([]string{"-ledger", ledger, "-profile", acceptancebudget.QwenProfile,
		"-expected-ledger-sha256", strings.Repeat("0", 64), "-request", "1", "-reason", "r",
		"-evidence", filepath.Join(t.TempDir(), "absent.json"),
		"-apply", "-confirm-full-reserve-charge"}, &output, &output); code != 2 {
		t.Fatalf("exit=%d, want the profile refused up front", code)
	}
	if _, err := os.Stat(ledger); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("the refused profile touched the ledger")
	}
}
