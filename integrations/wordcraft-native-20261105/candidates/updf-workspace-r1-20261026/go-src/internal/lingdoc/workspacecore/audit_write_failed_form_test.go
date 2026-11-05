package workspacecore

import (
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/clienterror"
)

// The audit write failure states its own outward form.
//
// The operation failed because its audit row could not be written. That is a
// storage failure, not a business decision: the request was valid and
// nothing changed. The caller must be able to tell that apart from a
// business rejection, and the code that says so has to be the one the
// transport renders.
//
// The presentation is declared with clienterror for exactly that reason. It
// used to be declared on a private interface (ClientStatus/ClientCode/
// ClientMessage) that the transport stopped reading, which left the error
// with a form nobody would render: the failure reached the caller as the
// generic "internal_error" 500, and the code that named the cause was lost.
func TestAuditWriteFailedDeclaresItsClientForm(t *testing.T) {
	cause := errors.New("audit row write failed")

	form, ok := clienterror.FormOf(auditWriteFailed(cause))
	if !ok {
		t.Fatal("审计写入失败没有声明对外形态")
	}
	if form.Status != 500 {
		t.Fatalf("status = %d, want 500", form.Status)
	}
	if form.Code != "audit_write_failed" {
		t.Fatalf("code = %q, want %q", form.Code, "audit_write_failed")
	}
	if form.Message != "审计记录写入失败，本次操作未生效，请稍后重试。" {
		t.Fatalf("message = %q", form.Message)
	}
	if form.Retryable {
		t.Fatal("retryable = true, want false")
	}
}

// The cause stays reachable: the domain still has to be able to ask what
// actually went wrong, and the audit trail records the same thing.
func TestAuditWriteFailedKeepsItsCause(t *testing.T) {
	cause := errors.New("audit row write failed")

	err := auditWriteFailed(cause)

	if !errors.Is(err, cause) {
		t.Fatal("包装后的错误丢了它包裹的原因")
	}
	if got := err.Error(); got != "audit_write_failed: audit row write failed" {
		t.Fatalf("error text = %q", got)
	}
}
