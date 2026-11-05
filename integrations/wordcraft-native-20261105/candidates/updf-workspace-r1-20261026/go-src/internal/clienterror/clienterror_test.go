package clienterror

import (
	"errors"
	"fmt"
	"testing"
)

func TestNewCarriesTheFormAndTheInternalText(t *testing.T) {
	form := Form{Status: 400, Code: "invalid_request", Message: "请求字段不符合约定。"}
	err := New(form, "invalid_request")
	if err.Error() != "invalid_request" {
		t.Fatalf("Error() = %q, want the internal text", err.Error())
	}
	got, ok := FormOf(err)
	if !ok {
		t.Fatal("FormOf did not find the presentation")
	}
	if got != form {
		t.Fatalf("FormOf = %+v, want %+v", got, form)
	}
}

func TestFormOfFindsThePresentationThroughAWrapper(t *testing.T) {
	form := Form{Status: 409, Code: "version_conflict", Message: "内容已变化，请先读取当前版本。"}
	wrapped := fmt.Errorf("apply the change set: %w", New(form, "version_conflict"))
	got, ok := FormOf(wrapped)
	if !ok || got != form {
		t.Fatalf("FormOf = %+v, %v, want %+v", got, ok, form)
	}
}

// The outer error decides what the caller sees, even when the cause also
// carries a presentation: the outer one is the one that describes the
// situation the caller is in.
func TestTheOuterErrorDecidesThePresentation(t *testing.T) {
	inner := Form{Status: 422, Code: "invalid_state", Message: "当前状态不允许该操作。"}
	outer := Form{Status: 409, Code: "context_unstable", Message: "正式变更尚未完成稳定化。"}
	err := Wrap(outer, "context_unstable", New(inner, "invalid_state"))
	got, ok := FormOf(err)
	if !ok || got != outer {
		t.Fatalf("FormOf = %+v, %v, want the outer form %+v", got, ok, outer)
	}
}

func TestWrapKeepsTheCauseReachable(t *testing.T) {
	cause := errors.New("the underlying reason")
	err := Wrap(Form{Status: 403, Code: "forbidden"}, "forbidden: the underlying reason", cause)
	if !errors.Is(err, cause) {
		t.Fatal("the cause is no longer reachable")
	}
	if err.Error() != "forbidden: the underlying reason" {
		t.Fatalf("Error() = %q", err.Error())
	}
}

func TestFormOfReportsNothingForAnUndeclaredError(t *testing.T) {
	if _, ok := FormOf(errors.New("plain")); ok {
		t.Fatal("a plain error must not report a presentation")
	}
	if _, ok := FormOf(nil); ok {
		t.Fatal("nil must not report a presentation")
	}
}
