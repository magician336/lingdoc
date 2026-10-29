package operationcore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"testing"
)

func TestValidKeyAcceptsTheBoundsAndRejectsTheRest(t *testing.T) {
	cases := []struct {
		key  string
		want bool
	}{
		{"12345678", true},                 // exactly the minimum
		{"1234567", false},                 // one too short
		{"", false},                        //
		{string(make([]byte, 128)), true},  // exactly the maximum
		{string(make([]byte, 129)), false}, // one too long
	}
	for _, tc := range cases {
		if got := ValidKey(tc.key); got != tc.want {
			t.Fatalf("ValidKey(len=%d) = %v, want %v", len(tc.key), got, tc.want)
		}
	}
}

// The digest must be the one the pipeline used before: sha256 over the JSON
// encoding, in hex.
func TestRequestHashMatchesTheDigestThePipelineUsed(t *testing.T) {
	body := map[string]any{"chapter_id": "c1", "body": "正文"}
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(encoded)
	want := hex.EncodeToString(sum[:])

	got, err := RequestHash(body)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("RequestHash = %q, want %q", got, want)
	}
}

// A body that cannot be encoded is a failure, not a silent digest.
func TestRequestHashReportsAnUnencodableBody(t *testing.T) {
	if _, err := RequestHash(make(chan int)); err == nil {
		t.Fatal("an unencodable body produced a digest without an error")
	}
}

// A value hash must stay stable even when the value cannot be encoded: the
// pipeline hashes values it does not control.
func TestValueHashIsStableForAnUnencodableValue(t *testing.T) {
	first := ValueHash(make(chan int))
	second := ValueHash(make(chan int))
	if first == "" || first != second {
		t.Fatalf("ValueHash is not stable for an unencodable value: %q vs %q", first, second)
	}
	sum := sha256.Sum256(nil)
	if first != hex.EncodeToString(sum[:]) {
		t.Fatalf("ValueHash = %q, want the digest of the empty encoding", first)
	}
}

// The human decision gate is a mechanism the pipeline applies; which
// operations need it belongs to the domain that owns them.
//
// The gate has an explicit "not declared" state, and it is the zero value. A
// caller that forgot to declare is refused at run time rather than let
// through, because a check that was never declared must not be assumed
// absent.
func TestHumanGateTreatsAnUndeclaredGateAsRefused(t *testing.T) {
	if HumanGateUndeclared != 0 {
		t.Fatal("the undeclared gate must be the zero value, so a caller that forgot to declare is recognised")
	}
	if HumanGateRequired == HumanGateNotRequired {
		t.Fatal("the two declared values must differ")
	}
	if HumanGateRequired == HumanGateUndeclared || HumanGateNotRequired == HumanGateUndeclared {
		t.Fatal("a declared gate must not read as undeclared")
	}
}

// A caller that does not say whether the operation needs the gate is refused.
//
// The refusal is the safe direction: a check that was never declared must not
// be assumed absent, and a caller that forgot must not be let through.
func TestRunRefusesAnUndeclaredGate(t *testing.T) {
	refused := false
	_, err := Run(context.Background(), Request[string, string, string]{
		Identity: Identity[string]{Actor: "a", Operation: "op", Target: "t", Key: "12345678"},
		Errors: Errors{
			InvalidRequest:        errInvalidRequest,
			IdempotencyConflict:   errInvalidRequest,
			VersionConflict:       errInvalidRequest,
			InvalidState:          errInvalidRequest,
			HumanDecisionRequired: errInvalidRequest,
		},
		ValidActor: func(string) bool { return true },
		Transaction: func(context.Context, Options, func(string) error) error {
			return nil
		},
		Ledger: func(string) (Ledger[string], error) { return nil, nil },
		Write: func(context.Context, string, string) (any, string, int, error) {
			return nil, "", 0, nil
		},
		Project: func(context.Context, string, string, json.RawMessage) (json.RawMessage, error) {
			return nil, nil
		},
	})
	if !errors.Is(err, errInvalidRequest) {
		t.Fatalf("an undeclared gate = %v, want the invalid request error", err)
	}
	refused = true
	if !refused {
		t.Fatal("the operation was not refused")
	}
}

var errInvalidRequest = errors.New("invalid request")

// A replay payload that could not be decoded is a replay attempt, not an
// ordinary failure, and the marker must survive wrapping.
func TestReplayPayloadDecodeErrorIsRecognisedThroughWrapping(t *testing.T) {
	cause := errors.New("payload is not a working copy")
	if IsReplayPayloadDecodeError(cause) {
		t.Fatal("an ordinary error was reported as a replay payload decode failure")
	}
	marked := MarkReplayPayloadDecodeError(cause)
	if !IsReplayPayloadDecodeError(marked) {
		t.Fatal("the marked error was not recognised")
	}
	if !errors.Is(marked, cause) {
		t.Fatal("the marked error lost the cause it carries")
	}
	if IsReplayPayloadDecodeError(errors.New("outer: " + marked.Error())) {
		// The message alone does not carry the marker, and must not: only the
		// wrapped value does. This asserts the marker is not a string match.
		t.Fatal("the marker was recognised from the message alone")
	}
	if MarkReplayPayloadDecodeError(nil) != nil {
		t.Fatal("marking a nil error produced a value")
	}
}

// The operations whose write transaction may retry on lock contention have one
// owner.
func TestRetryLocksNamesTheRetryingOperations(t *testing.T) {
	for _, op := range []string{"saveSpec", "createProject", "changeTemplate", "saveTemplateCopy", "bindTemplateCopyRuleSet", "applyLegacyRuleOverrides", "createTemplateFieldArchive", "removeArchivedTemplateField", "reverseChangeSet"} {
		if !RetryLocks(op) {
			t.Fatalf("operation %q retries on lock contention", op)
		}
	}
	for _, op := range []string{"saveChapter", "commitWorkingCopy", ""} {
		if RetryLocks(op) {
			t.Fatalf("operation %q must not retry on lock contention", op)
		}
	}
}
