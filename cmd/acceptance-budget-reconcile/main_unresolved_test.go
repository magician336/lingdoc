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

// The operator command can settle a call whose OUTCOME could not be
// confirmed, and it refuses everything else without touching the ledger.

func unresolvedCLIEvidence(t *testing.T, ledger string, requestNumber int, status, blockedReason string) (acceptancebudget.UnresolvedCallEvidence, string) {
	t.Helper()
	before, err := os.ReadFile(ledger)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := sha256.Sum256(before)
	artifact := sha256.Sum256([]byte("synthetic operator attestation artifact"))
	e := acceptancebudget.UnresolvedCallEvidence{
		LedgerSHA256: hex.EncodeToString(snapshot[:]), RequestNumber: requestNumber,
		OriginalStatus: status, OriginalBlockedReason: blockedReason,
		EvidenceSHA256: hex.EncodeToString(artifact[:]),
		Operator:       "synthetic-CLI-operator",
		Reason:         "Synthetic operator attestation reviewed; retain the full reserve",
	}
	return e, filepath.Join(t.TempDir(), "evidence.json")
}

// writeUnresolvedCLIEvidence writes the evidence file the command reads.
func writeUnresolvedCLIEvidence(t *testing.T, path string, e acceptancebudget.UnresolvedCallEvidence) {
	t.Helper()
	data, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

// unresolvedCLIWindow builds a controlled embedding window whose last call
// ended in an outcome nobody can confirm.
func unresolvedCLIWindow(t *testing.T) string {
	t.Helper()
	ledger := filepath.Join(t.TempDir(), "synthetic.calls.json")
	b := acceptancebudget.New(acceptancebudget.QwenEmbeddingProfile, ledger)
	r, err := b.Reserve(context.Background(), acceptancebudget.QwenEmbedding, 100, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Unknown("outcome_unknown"); !errors.Is(err, acceptancebudget.ErrCallsSpent) {
		t.Fatal(err)
	}
	return ledger
}

func TestOperatorCLISettlesAnUnconfirmableCall(t *testing.T) {
	ledger := unresolvedCLIWindow(t)
	e, evidencePath := unresolvedCLIEvidence(t, ledger, 1, "outcome_unknown", "outcome_unknown")
	writeUnresolvedCLIEvidence(t, evidencePath, e)
	args := []string{"-mode", "unresolved-call", "-ledger", ledger,
		"-profile", acceptancebudget.QwenEmbeddingProfile,
		"-expected-ledger-sha256", e.LedgerSHA256, "-request", "1", "-reason", e.Reason,
		"-evidence", evidencePath, "-apply", "-confirm-full-reserve-charge"}
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
	// The failed call is preserved verbatim, the provider's usage stays
	// unknown, and the window is open again.
	row := readback.Requests[0]
	if readback.Blocked || row.Status != "outcome_unknown" || row.UsageStatus != "unavailable" ||
		row.ActualNanoCNY != 50_000 || row.ReservedNanoCNY != 50_000 {
		t.Fatalf("CLI altered the failure/money evidence: %+v", readback)
	}
	if row.UnresolvedReconciliation == nil ||
		row.UnresolvedReconciliation.ProviderTokenUsage != "unknown" ||
		row.UnresolvedReconciliation.ChargedNanoCNY != 50_000 {
		t.Fatalf("CLI did not record the conservative settlement: %+v", row.UnresolvedReconciliation)
	}
	if row.Reconciliation != nil {
		t.Fatal("the CLI wrote the HTTP error annotation on an unresolved call")
	}
	// A second apply is refused and changes nothing.
	if code := run(args, &output, &output); code != 1 {
		t.Fatalf("duplicate CLI apply exit=%d, want a refusal", code)
	}
	final, err := os.ReadFile(ledger)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, final) {
		t.Fatal("the refused duplicate changed ledger bytes")
	}
}

func TestOperatorCLIRefusesTheUnresolvedModeForAProfileWithoutTheCapability(t *testing.T) {
	ledger := filepath.Join(t.TempDir(), "must-not-change.json")
	var output bytes.Buffer
	if code := run([]string{"-mode", "unresolved-call", "-ledger", ledger,
		"-profile", "a-profile-nobody-declared",
		"-expected-ledger-sha256", strings.Repeat("0", 64), "-request", "1", "-reason", "r",
		"-evidence", filepath.Join(t.TempDir(), "absent.json"),
		"-apply", "-confirm-full-reserve-charge"}, &output, &output); code != 2 {
		t.Fatalf("exit=%d, want the profile refused up front", code)
	}
	if _, err := os.Stat(ledger); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("the refused profile touched the ledger")
	}
}

func TestOperatorCLIRefusesTheUnresolvedModeWithAWrongProfile(t *testing.T) {
	ledger := unresolvedCLIWindow(t)
	e, evidencePath := unresolvedCLIEvidence(t, ledger, 1, "outcome_unknown", "outcome_unknown")
	writeUnresolvedCLIEvidence(t, evidencePath, e)
	before, err := os.ReadFile(ledger)
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if code := run([]string{"-mode", "unresolved-call", "-ledger", ledger,
		"-profile", acceptancebudget.CostOnlyProfile,
		"-expected-ledger-sha256", e.LedgerSHA256, "-request", "1", "-reason", e.Reason,
		"-evidence", evidencePath, "-apply", "-confirm-full-reserve-charge"}, &output, &output); code != 1 {
		t.Fatalf("CLI exit=%d: the wrong profile was applied to this ledger", code)
	}
	after, err := os.ReadFile(ledger)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("the refused settlement changed ledger bytes")
	}
}

func TestOperatorCLIUnresolvedModeNeedsAnExplicitApply(t *testing.T) {
	ledger := filepath.Join(t.TempDir(), "must-not-exist.json")
	var output bytes.Buffer
	if code := run([]string{"-mode", "unresolved-call", "-ledger", ledger}, &output, &output); code != 2 {
		t.Fatalf("exit=%d", code)
	}
	if _, err := os.Stat(ledger); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("CLI without apply touched ledger")
	}
}
