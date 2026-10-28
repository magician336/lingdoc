// Command acceptance-budget-reconcile performs a single explicitly authorized
// local reconciliation. It neither calls a provider nor exposes an HTTP route.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/Tencent/WeKnora/internal/models/acceptancebudget"
)

// The two local reconciliations the command can perform.
//
// They are separate modes because they settle DIFFERENT calls and mean
// different things: the HTTP error mode settles a call the provider
// refused, the unresolved mode settles a call nobody can say what happened
// to. The http_error mode stays the default, so the command's existing
// behaviour is unchanged.
const (
	modeEmbeddingHTTPError = "embedding-http-error"
	modeUnresolvedCall     = "unresolved-call"
)

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("acceptance-budget-reconcile", flag.ContinueOnError)
	flags.SetOutput(stderr)
	ledger := flags.String("ledger", "", "explicit absolute calls-ledger JSON path")
	profile := flags.String("profile", acceptancebudget.CostOnlyProfile,
		"the controlled budget profile that owns this ledger (must support the requested reconciliation)")
	expected := flags.String("expected-ledger-sha256", "", "SHA256 of the complete current ledger bytes")
	number := flags.Int("request", 0, "exact failed request number")
	reason := flags.String("reason", "", "exact operator reason present in the evidence JSON")
	evidencePath := flags.String("evidence", "", "local evidence JSON path; no credentials")
	apply := flags.Bool("apply", false, "explicitly apply this one local reconciliation")
	confirm := flags.Bool("confirm-full-reserve-charge", false, "accept full reserve cost without claiming provider usage")
	mode := flags.String("mode", modeEmbeddingHTTPError,
		"which local reconciliation to perform: "+modeEmbeddingHTTPError+" | "+modeUnresolvedCall)
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if !*apply || !*confirm || flags.NArg() != 0 || !filepath.IsAbs(*ledger) || filepath.Ext(*ledger) != ".json" || !filepath.IsAbs(*evidencePath) {
		_, _ = fmt.Fprintln(stderr, "explicit apply, full-reserve confirmation and absolute ledger/evidence paths are required")
		return 2
	}
	switch *mode {
	case modeEmbeddingHTTPError:
		return reconcileEmbeddingHTTPError(*profile, *ledger, *expected, *number, *reason, *evidencePath, *confirm, stdout, stderr)
	case modeUnresolvedCall:
		return reconcileUnresolvedCall(*profile, *ledger, *expected, *number, *reason, *evidencePath, *confirm, stdout, stderr)
	default:
		_, _ = fmt.Fprintln(stderr, "unknown reconciliation mode:", *mode)
		return 2
	}
}

// reconcileEmbeddingHTTPError settles one call the provider refused with an
// HTTP error.
func reconcileEmbeddingHTTPError(profile, ledgerPath, expected string, number int, reason, evidencePath string, confirm bool, stdout, stderr io.Writer) int {
	// The profile is named, not assumed: the ledger's owner decides which
	// rules apply to it, and a profile that cannot carry an embedding HTTP
	// error reconciliation is refused here -- before any file is read.
	if !acceptancebudget.SupportedProfile(profile) ||
		!acceptancebudget.ProfileSupportsEmbeddingHTTPErrorReconciliation(profile) {
		_, _ = fmt.Fprintln(stderr, "profile does not support embedding HTTP error reconciliation:", profile)
		return 2
	}
	data, err := os.ReadFile(evidencePath)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "cannot read operator evidence")
		return 1
	}
	var evidence acceptancebudget.EmbeddingHTTPErrorEvidence
	if err := decodeStrictJSON(data, &evidence); err != nil {
		_, _ = fmt.Fprintln(stderr, "invalid operator evidence")
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	budget := acceptancebudget.New(profile, ledgerPath)
	err = budget.ReconcileEmbeddingHTTPError(ctx, acceptancebudget.ReconcileEmbeddingHTTPErrorInput{
		ExpectedLedgerSHA256: expected, RequestNumber: number, ExpectedStatus: "http_error",
		ExpectedBlockedReason: "http_error", ExpectedReason: reason, ConfirmFullReserveCharge: confirm, Evidence: evidence,
	})
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "reconciliation refused; ledger retained:", err)
		return 1
	}
	_, _ = fmt.Fprintln(stdout, "local full-reserve reconciliation recorded on profile "+profile+
		"; original http_error and unknown provider usage retained")
	return 0
}

// reconcileUnresolvedCall settles one call whose OUTCOME could not be
// confirmed: a failed transport, a missing usage, a crash between the
// reservation and the completion, or a cancelled reservation.
//
// The status the operator settled is the one the evidence names, and the
// ledger's own row has to agree with it. The settlement keeps the failed
// request exactly as it was, charges the full reserved upper bound and
// records the provider's usage as unknown.
func reconcileUnresolvedCall(profile, ledgerPath, expected string, number int, reason, evidencePath string, confirm bool, stdout, stderr io.Writer) int {
	// The profile is named, not assumed, and the capability is checked
	// BEFORE any file is read: a profile that cannot settle an
	// unconfirmable outcome is refused here, and the ledger is not touched.
	if !acceptancebudget.SupportedProfile(profile) ||
		!acceptancebudget.ProfileSupportsUnresolvedCallReconciliation(profile) {
		_, _ = fmt.Fprintln(stderr, "profile does not support unresolved call reconciliation:", profile)
		return 2
	}
	data, err := os.ReadFile(evidencePath)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "cannot read operator evidence")
		return 1
	}
	var evidence acceptancebudget.UnresolvedCallEvidence
	if err := decodeStrictJSON(data, &evidence); err != nil {
		_, _ = fmt.Fprintln(stderr, "invalid operator evidence")
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	budget := acceptancebudget.New(profile, ledgerPath)
	err = budget.ReconcileUnresolvedCall(ctx, acceptancebudget.ReconcileUnresolvedCallInput{
		ExpectedLedgerSHA256: expected, RequestNumber: number, ExpectedStatus: evidence.OriginalStatus,
		ExpectedBlockedReason: evidence.OriginalBlockedReason, ExpectedReason: reason,
		ConfirmFullReserveCharge: confirm, Evidence: evidence,
	})
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "reconciliation refused; ledger retained:", err)
		return 1
	}
	_, _ = fmt.Fprintln(stdout, "local full-reserve reconciliation recorded on profile "+profile+
		"; the unresolved request is retained verbatim with unknown provider usage")
	return 0
}

// decodeStrictJSON decodes one JSON document and refuses anything else: an
// unknown field, a second document, or trailing content.
func decodeStrictJSON(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return err
	}
	return nil
}
