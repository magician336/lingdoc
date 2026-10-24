package workspace

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/acceptancebudget"
)

// The two ledgers, one answer.
//
// The shared budget ledger and the round ledger both account for one and the
// same provider call. When they read their criteria from two different
// places, they can write down two different conclusions about one and the
// same answer -- and the ledger that did not see the violation keeps
// spending.
//
// The tests below are offline. They never reach a provider: they reserve,
// answer and read the ledger files back.

// paritySharedLedger is the shared budget ledger as a reader of the JSON file
// sees it.
type paritySharedLedger struct {
	Blocked       bool   `json:"blocked"`
	BlockedReason string `json:"blocked_reason"`
	Requests      []struct {
		Number          int    `json:"number"`
		Status          string `json:"status"`
		InputUpperBound int    `json:"input_upper_bound_tokens"`
		OutputLimit     int    `json:"output_limit_tokens"`
		InputTokens     int    `json:"input_tokens"`
		OutputTokens    int    `json:"output_tokens"`
	} `json:"requests"`
}

// parityVerdict is what one ledger says about one answer.
type parityVerdict struct {
	Status  string
	Blocked bool
}

// parityReadShared reads a shared budget ledger's conclusion.
func parityReadShared(t *testing.T, path string) parityVerdict {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read the shared budget ledger %s: %v", path, err)
	}
	var ledger paritySharedLedger
	if err := json.Unmarshal(data, &ledger); err != nil {
		t.Fatalf("decode the shared budget ledger %s: %v", path, err)
	}
	if len(ledger.Requests) != 1 {
		t.Fatalf("the shared budget ledger holds %d requests, want exactly 1", len(ledger.Requests))
	}
	return parityVerdict{Status: ledger.Requests[0].Status, Blocked: ledger.Blocked}
}

// parityReadRound reads the round ledger's conclusion.
func parityReadRound(t *testing.T, path string) parityVerdict {
	t.Helper()
	ledger, err := readAcceptanceLedger(path)
	if err != nil {
		t.Fatalf("read the round ledger %s: %v", path, err)
	}
	if len(ledger.Requests) != 1 {
		t.Fatalf("the round ledger holds %d requests, want exactly 1", len(ledger.Requests))
	}
	return parityVerdict{Status: ledger.Requests[0].Status, Blocked: ledger.Blocked}
}

// paritySharedOwn drives ONE answer through a shared budget ledger that is
// driven ON ITS OWN, and returns what it concluded.
//
// This is the only way to see the shared budget ledger's OWN criteria. The
// shared budget ledger of a real window is written by the round ledger's
// reservation, and the round ledger hands its own status to it: the file of
// a real window can therefore say "violation" wherever the round ledger says
// "violation", and the shared ledger can add a violation but never remove
// one. A guardian that reads the window's file alone cannot see the shared
// ledger's own criteria at all, and a broken criterion there would go
// unnoticed.
//
// The reservation and the answer are the same ones the window uses, so the
// two ledgers are asked the same question about the same answer.
func paritySharedOwn(t *testing.T, profile string, inputBound, inputTokens, outputTokens int) parityVerdict {
	t.Helper()
	_, outputLimit, _ := acceptancebudget.ProfileTokenLimits(profile)
	path := filepath.Join(t.TempDir(), "calls.json")
	budget := acceptancebudget.New(profile, path)
	reservation, err := budget.Reserve(context.Background(), acceptancebudget.ProfileApprovedModel(profile),
		inputBound, outputLimit)
	if err != nil {
		t.Fatalf("reserve in the shared budget ledger: %v", err)
	}
	// The error is not the assertion: the verdict is what the file says.
	_ = reservation.CompleteUsage("response_received", acceptancebudget.Usage{
		InputTokens:  inputTokens,
		OutputTokens: outputTokens,
		TotalTokens:  inputTokens + outputTokens,
		Reported:     true,
	}, "stop", false)
	return parityReadShared(t, path)
}

// parityAnswer drives ONE answer through the round ledger of a real window.
//
// The window writes both files: the round ledger, and the shared budget
// ledger beside it.
func parityAnswer(t *testing.T, inputBound, inputTokens, outputTokens int) (parityVerdict, parityVerdict) {
	t.Helper()
	budget := qwenV2Budget(t)
	reservation, err := budget.reserve(context.Background(), generationAcceptanceModel, inputBound)
	if err != nil {
		t.Fatalf("reserve: %v", err)
	}
	// The error is not the assertion: the verdict is what the two ledger
	// files say afterwards.
	_ = reservation.complete("response_received", inputTokens, outputTokens, "stop", false)
	round := parityReadRound(t, budget.ledgerPath)
	shared := parityReadShared(t, budget.ledgerPath+".calls.json")
	return round, shared
}

// The two ledgers give the SAME conclusion about one and the same answer.
//
// This is the guardian of the whole ticket. It is written as a table so that
// every class of the authoritative checklist is exercised on both sides: an
// answer inside every ceiling, an answer over the request's own input bound,
// an answer over the request's output limit, an answer over the profile's
// total ceiling and an impossible counter.
//
// Three conclusions are compared for every answer:
//
//   - the round ledger's own conclusion,
//   - the shared budget ledger's OWN conclusion, read from a ledger that was
//     driven on its own, and
//   - the shared budget ledger of the window, which inherits the round
//     ledger's status.
//
// Before the checklist existed, the input row made the two ledgers disagree:
// the shared budget ledger measured the reported input tokens against the
// request's own input bound, and the round ledger measured them against the
// profile's input ceiling. An answer between the two was a violation in one
// file and a normal response in the other.
func TestBothLedgersGiveTheSameConclusionAboutTheSameAnswer(t *testing.T) {
	profile := acceptancebudget.QwenProfileV2
	cases := []struct {
		name         string
		inputBound   int
		inputTokens  int
		outputTokens int
	}{
		{
			name:         "an answer inside every ceiling",
			inputBound:   2048,
			inputTokens:  320,
			outputTokens: 128,
		},
		{
			name:         "an answer over the request's own input bound",
			inputBound:   2048,
			inputTokens:  2049,
			outputTokens: 128,
		},
		{
			name:         "an answer over the request's output limit",
			inputBound:   2048,
			inputTokens:  320,
			outputTokens: acceptancebudget.QwenOutputMax + 1,
		},
		{
			name:         "an answer over the profile's total ceiling",
			inputBound:   2048,
			inputTokens:  acceptancebudget.QwenInputMax,
			outputTokens: acceptancebudget.QwenOutputMax + 1,
		},
		{
			name:         "an impossible counter",
			inputBound:   2048,
			inputTokens:  -1,
			outputTokens: 128,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			round, sharedInWindow := parityAnswer(t, tc.inputBound, tc.inputTokens, tc.outputTokens)
			sharedOwn := paritySharedOwn(t, profile, tc.inputBound, tc.inputTokens, tc.outputTokens)

			if round.Status != sharedOwn.Status {
				t.Fatalf("the two ledgers disagree about one answer: the round ledger says %q, the shared budget ledger says %q",
					round.Status, sharedOwn.Status)
			}
			if round.Blocked != sharedOwn.Blocked {
				t.Fatalf("the two ledgers disagree about blocking one answer: the round ledger says %v, the shared budget ledger says %v",
					round.Blocked, sharedOwn.Blocked)
			}
			if round.Status != sharedInWindow.Status || round.Blocked != sharedInWindow.Blocked {
				t.Fatalf("the window's shared budget ledger says %q (blocked %v), the round ledger says %q (blocked %v)",
					sharedInWindow.Status, sharedInWindow.Blocked, round.Status, round.Blocked)
			}
		})
	}
}

// The band between the request's own bound and the profile's ceiling is a
// violation in BOTH ledgers.
//
// The band is the exact interval the ticket is about: an answer whose input
// token count is above the request's own input bound but at or below the
// profile's input ceiling. The shared budget ledger called it a violation
// and the round ledger called it a normal response.
func TestTheBandBetweenTheRequestBoundAndTheProfileCeilingIsAViolationInBothLedgers(t *testing.T) {
	profile := acceptancebudget.QwenProfileV2
	inputMax, _, _ := acceptancebudget.ProfileTokenLimits(profile)
	const bound = 2048
	if bound >= inputMax {
		t.Fatalf("the fixture needs a bound under the profile's ceiling: %d >= %d", bound, inputMax)
	}
	round, sharedInWindow := parityAnswer(t, bound, bound+1, 128)
	if round.Status != "limit_violation" {
		t.Fatalf("the round ledger recorded %q, want limit_violation", round.Status)
	}
	if sharedInWindow.Status != "limit_violation" {
		t.Fatalf("the window's shared budget ledger recorded %q, want limit_violation", sharedInWindow.Status)
	}
	sharedOwn := paritySharedOwn(t, profile, bound, bound+1, 128)
	if sharedOwn.Status != "limit_violation" {
		t.Fatalf("the shared budget ledger recorded %q, want limit_violation", sharedOwn.Status)
	}
	if !round.Blocked || !sharedOwn.Blocked {
		t.Fatalf("a limit violation must block both ledgers: round %v, shared %v", round.Blocked, sharedOwn.Blocked)
	}
}

// The call site's profile-ceiling pre-check can never disagree with the
// ledgers.
//
// The call site measures the reported input tokens against the profile's
// input ceiling BEFORE it writes the answer down. The ledgers measure them
// against the request's own input bound. The two can only ever agree,
// because the reservation refuses a request whose bound passes the profile's
// ceiling: a reported token count over the ceiling is therefore also over the
// request's own bound.
//
// So when the pre-check fires, the ledgers fire too -- and the answer is
// never written down as a normal response by one file and as a violation by
// the other.
func TestTheProfileCeilingPreCheckCanNeverDisagreeWithTheLedgers(t *testing.T) {
	profile := acceptancebudget.QwenProfileV2
	inputMax, outputMax, _ := acceptancebudget.ProfileTokenLimits(profile)
	if outputMax <= 0 {
		t.Fatal("the fixture needs a positive output ceiling")
	}
	// The biggest bound the reservation accepts, and an answer one token over
	// the profile's ceiling.
	round, sharedInWindow := parityAnswer(t, inputMax, inputMax+1, 16)
	if round.Status != "limit_violation" {
		t.Fatalf("the round ledger recorded %q, want limit_violation", round.Status)
	}
	if sharedInWindow.Status != "limit_violation" {
		t.Fatalf("the window's shared budget ledger recorded %q, want limit_violation", sharedInWindow.Status)
	}
	sharedOwn := paritySharedOwn(t, profile, inputMax, inputMax+1, 16)
	if sharedOwn.Status != "limit_violation" {
		t.Fatalf("the shared budget ledger recorded %q, want limit_violation", sharedOwn.Status)
	}
	if !round.Blocked || !sharedOwn.Blocked {
		t.Fatalf("a limit violation must block both ledgers: round %v, shared %v", round.Blocked, sharedOwn.Blocked)
	}
}

// The workspace's own total ceiling and the checklist's total ceiling are the
// same number.
//
// The workspace reads the profile's ceilings through its own helpers, and the
// checklist reads them from the profile table. They are one number, and this
// test says so before the two can drift apart.
func TestTheWorkspaceAndTheChecklistReadTheSameTotalCeiling(t *testing.T) {
	for _, profile := range []string{
		acceptancebudget.Profile,
		acceptancebudget.CostOnlyProfile,
		acceptancebudget.QwenProfile,
		acceptancebudget.QwenProfileV2,
	} {
		_, _, totalMax := acceptancebudget.ProfileTokenLimits(profile)
		if acceptanceTotalMax(profile) != totalMax {
			t.Fatalf("the workspace reads %d as the total ceiling of %q, the checklist reads %d",
				acceptanceTotalMax(profile), profile, totalMax)
		}
	}
}

// The first profile's ledger artifact is byte for byte what it was.
//
// The second profile added fields to the ledger, and the first profile's
// ledger file must not have grown a single one of them. This is the artifact
// half of that proof: the file the first profile's window produces is hashed
// and compared with the hash of the very same scenario taken before the
// limit checklist existed.
//
// The only volatile part of the artifact is the wall-clock timestamp of the
// row, so it is masked before the hash is taken. Everything else -- every
// field name, every number, every flag -- is compared byte for byte.
func TestQwenV1LedgerArtifactIsByteIdentical(t *testing.T) {
	t.Setenv(generationAcceptanceProfileEnv, acceptancebudget.QwenProfile)
	path := filepath.Join(t.TempDir(), "round.json")
	t.Setenv(generationAcceptanceLedgerEnv, path)
	budget, err := loadGenerationAcceptanceBudget()
	if err != nil || budget == nil {
		t.Fatalf("loadGenerationAcceptanceBudget() = %v, %v; want the first profile armed", budget, err)
	}
	reservation, err := budget.reserve(context.Background(), generationAcceptanceModel, v1ArtifactInputBound)
	if err != nil {
		t.Fatalf("reserve: %v", err)
	}
	if err := reservation.complete("response_received", v1ArtifactInputTokens, v1ArtifactOutputTokens, "stop", false); err != nil {
		t.Fatalf("complete: %v", err)
	}

	if got := parityHashArtifact(t, budget.ledgerPath); got != v1RoundLedgerSHA256 {
		t.Fatalf("the first profile's round ledger artifact changed:\n got %s\nwant %s", got, v1RoundLedgerSHA256)
	}
	if got := parityHashArtifact(t, budget.ledgerPath+".calls.json"); got != v1SharedLedgerSHA256 {
		t.Fatalf("the first profile's shared budget ledger artifact changed:\n got %s\nwant %s", got, v1SharedLedgerSHA256)
	}
}

// The scenario the first profile's artifact is taken from.
//
// The numbers are literals: an artifact that is compared with itself proves
// nothing, and an artifact taken from a scenario that moves with the
// constants proves nothing either.
const (
	v1ArtifactInputBound   = 2515
	v1ArtifactInputTokens  = 320
	v1ArtifactOutputTokens = 128

	// v1RoundLedgerSHA256 and v1SharedLedgerSHA256 are the hashes of the
	// first profile's two ledger artifacts, taken from the scenario above on
	// origin/main BEFORE the limit checklist existed.
	v1RoundLedgerSHA256  = "f02bd19fff13177d6111ccbc80b2f4fc9934ce5e06e6306ce0a4bc55ab126656"
	v1SharedLedgerSHA256 = "5367bab9dfa293bd9419c01309192ad57912eb66bd40c18d51a95866553ef6a9"
)

// parityWallClock matches the one volatile value of a ledger artifact.
var parityWallClock = regexp.MustCompile(`"created_at": "[^"]*"`)

// parityHashArtifact hashes one ledger artifact with its wall-clock
// timestamp masked.
func parityHashArtifact(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read the ledger artifact %s: %v", path, err)
	}
	masked := parityWallClock.ReplaceAll(data, []byte(`"created_at": "<masked>"`))
	sum := sha256.Sum256(masked)
	return hex.EncodeToString(sum[:])
}
