// Package clienterror lets a domain error state its own client presentation.
//
// A business failure knows two things about itself: what went wrong inside the
// domain, and what the caller should be told. The second half used to live in
// the transport's error tables, which meant every new business error meant
// editing a table in another package, and a table that was missed turned a
// 400 into a 500.
//
// An error declared with this package carries its presentation with it, so the
// transport only has to render it.
//
// The package is a leaf: it depends on nothing but the standard library, so
// both the host-level packages and the LingDoc packages can use it.
package clienterror

import "errors"

// Form is the outward shape of a business failure.
//
// Status is the HTTP status code, Code the stable machine-readable error code
// and Message the human-readable text. Retryable tells the caller whether
// repeating the same request could succeed. RetryAfter is the number of
// seconds the caller should wait before repeating it, when the failure has a
// known wait (0 means it does not).
//
// Details is optional: a failure that can name its reason carries it there,
// and a failure that has nothing extra to say leaves it empty. It is rendered
// only when it is present, so the responses of the failures that never carry
// one keep exactly the shape they had.
type Form struct {
	Status     int
	Code       string
	Message    string
	Retryable  bool
	RetryAfter int
	Details    any
}

// Presenter is implemented by errors that know their own client presentation.
type Presenter interface {
	error
	ClientForm() Form
}

// Error is a domain error that carries its client presentation.
//
// The internal text and the client message are different things: the internal
// text is what the domain and the audit trail read, the client message is what
// the caller sees. Keeping them apart means a caller-facing wording change
// never rewrites what the domain recorded.
type Error struct {
	form  Form
	text  string
	cause error
}

// New declares an error that carries its client presentation.
//
// text is the internal text; it stays in the domain's own language and is
// what the error's Error method reports.
func New(form Form, text string) *Error {
	return &Error{form: form, text: text}
}

// Wrap declares an error that carries its client presentation and wraps a
// cause.
//
// The cause stays reachable through errors.Is and errors.As, so the reason the
// failure happened is not lost when the failure is reported outward.
func Wrap(form Form, text string, cause error) *Error {
	return &Error{form: form, text: text, cause: cause}
}

// Error returns the internal text.
func (e *Error) Error() string { return e.text }

// Unwrap returns the wrapped cause, if any.
func (e *Error) Unwrap() error { return e.cause }

// ClientForm returns the outward shape of the error.
func (e *Error) ClientForm() Form { return e.form }

// FormOf reports the client presentation of err, if err or anything it wraps
// knows one.
//
// It walks the error chain the same way errors.Is does, so a presentation
// declared on a sentinel is found even when the sentinel was wrapped with
// additional context.
func FormOf(err error) (Form, bool) {
	var presenter Presenter
	if errors.As(err, &presenter) {
		return presenter.ClientForm(), true
	}
	return Form{}, false
}
