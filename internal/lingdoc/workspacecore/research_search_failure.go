package workspacecore

import "strings"

// ResearchSearchError reports which retrieval path of a research round failed
// and why.
//
// A round can retrieve from two places: the project's own material index and an
// external search provider.  Before this type existed both failures collapsed
// into the single code "provider_failure", so the receipt could not tell "the
// channel is not configured" from "the URL was refused" from "the provider
// itself failed" -- and the server log carried nothing at all (#335).
//
// Partial says the round still produced results from the other path.  A
// partial failure does not fail the round; it is recorded on the round so the
// degraded path stays visible.
type ResearchSearchError struct {
	Material string
	Provider string
	Partial  bool
}

func (e *ResearchSearchError) Error() string {
	parts := make([]string, 0, 2)
	if e.Material != "" {
		parts = append(parts, "material: "+e.Material)
	}
	if e.Provider != "" {
		parts = append(parts, "provider: "+e.Provider)
	}
	if len(parts) == 0 {
		return "research search failed"
	}
	return strings.Join(parts, "; ")
}

// ResearchFailureDetail is the receipt-safe rendering of an error: a bounded,
// non-empty string the round can carry without echoing an unbounded provider
// body into the contract.
func ResearchFailureDetail(err error) string {
	if err == nil {
		return ""
	}
	const limit = 512
	detail := err.Error()
	if len(detail) > limit {
		detail = detail[:limit] + "…"
	}
	return detail
}
