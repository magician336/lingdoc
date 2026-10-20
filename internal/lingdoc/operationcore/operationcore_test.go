package operationcore

import (
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

// The human decision gate knows which operations need it.
func TestRequiresHumanDecisionNamesTheOperationsThatNeedIt(t *testing.T) {
	for _, op := range []string{"applyChangeSet", "reverseChangeSet", "activateProject", "chapterReview"} {
		if !RequiresHumanDecision(op) {
			t.Fatalf("operation %q needs the human decision gate", op)
		}
	}
	for _, op := range []string{"saveSpec", "saveChapter", "createProject", ""} {
		if RequiresHumanDecision(op) {
			t.Fatalf("operation %q must not need the human decision gate", op)
		}
	}
}

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
	for _, op := range []string{"saveSpec", "createProject", "saveTemplateCopy", "bindTemplateCopyRuleSet", "reverseChangeSet"} {
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
