// Package sourcemarker owns the source-marker syntax that research bodies
// use to cite materials.
//
// The syntax is a literal `[[source:<id>]]` marker. This package is the only
// place that syntax is written down: the identifier character set, the
// recognition rule, and the rule that a body's marker count must equal the
// number of marker prefixes it contains.
//
// It is deliberately a leaf package with no LingDoc imports, so that every
// layer above it — the workspace domain, the delivery snapshot, the generation
// service, the candidate adoption store and the DOCX renderer — can depend on
// one definition without depending on each other.
package sourcemarker

import (
	"errors"
	"regexp"
	"strings"
)

// ErrMalformed reports that a body contains a source-marker prefix which does
// not belong to a well-formed marker, or that the number of markers disagrees
// with the number of marker prefixes in the body.
//
// The two facts are deliberately reported as one error: a caller can only
// react to "this body does not carry a usable set of source markers" either
// way, and separating them would invite callers to half-handle them.
var ErrMalformed = errors.New("sourcemarker: malformed source marker")

// markerPattern is the single definition of the source-marker syntax.
//
// The identifier character set is the one the original regular expressions
// used, and it is the only place that set is written down. Three packages
// used to hand-roll their own scanning and accepted a wider set (any
// non-empty run between "[[source:" and "]]"), so a body such as
// "[[source:a.b]]" was accepted by one path and rejected by another. The
// regular-expression set is the stricter of the two and is now the only one.
var markerPattern = regexp.MustCompile(`\[\[source:([A-Za-z0-9_-]+)\]\]`)

// prefix is the literal that opens a source marker. Its occurrence count in a
// body must equal the number of parsed markers, otherwise the body contains a
// prefix that is not a marker.
const prefix = "[[source:"

// IDs returns every source id carried by the markers in body, in the order the
// markers appear.
//
// IDs keeps duplicates: the same id may legitimately be cited more than once in
// a body, and whether that is an error is the caller's decision, not the
// parser's. Callers that need a set can build one.
//
// IDs does not validate the body. A body that contains a stray prefix yields
// the ids of the markers that did parse; use Validate when the difference
// between "parsed" and "present" matters.
func IDs(body string) []string {
	matches := markerPattern.FindAllStringSubmatch(body, -1)
	ids := make([]string, 0, len(matches))
	for _, match := range matches {
		ids = append(ids, match[1])
	}
	return ids
}

// Validate reports whether the markers in body are well formed.
//
// Every "[[source:" prefix in the body must belong to a marker, and the number
// of markers must equal the number of prefixes. A body whose marker count
// disagrees with its prefix count is malformed: it claims a citation the
// parser cannot resolve, and silently ignoring it would drop a reference.
func Validate(body string) error {
	if strings.Count(body, prefix) != len(markerPattern.FindAllStringSubmatch(body, -1)) {
		return ErrMalformed
	}
	return nil
}

// Strip returns body with every source marker removed.
//
// It is used where the markers must be hidden from a parser that would
// otherwise split on the brackets.
func Strip(body string) string {
	return markerPattern.ReplaceAllString(body, "")
}

// ReplaceAll rewrites every marker token in body through fn.
//
// The callback receives the whole token and the id it carries, and returns the
// text that replaces the token.
//
// ReplaceAll does not validate the body. Some callers have to rewrite markers
// before they can count them, so validation stays an explicit, separate step
// in Validate.
func ReplaceAll(body string, fn func(token, id string) string) string {
	return markerPattern.ReplaceAllStringFunc(body, func(token string) string {
		match := markerPattern.FindStringSubmatch(token)
		if match == nil {
			return token
		}
		return fn(token, match[1])
	})
}

// TokenID returns the id carried by a single marker token.
//
// The second result reports whether the token is a marker at all.
func TokenID(token string) (string, bool) {
	match := markerPattern.FindStringSubmatch(token)
	if match == nil {
		return "", false
	}
	return match[1], true
}
