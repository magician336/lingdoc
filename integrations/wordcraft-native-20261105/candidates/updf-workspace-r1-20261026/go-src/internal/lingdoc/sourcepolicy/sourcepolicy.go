// Package sourcepolicy defines the shared source-review seam used by LingDoc
// consumers.  The workspace, candidate-adoption and delivery adapters may
// translate a verdict into their own result shape, but the review itself is
// performed through this one domain port.
package sourcepolicy

import (
	"context"

	"github.com/Tencent/WeKnora/internal/evidence"
	"github.com/Tencent/WeKnora/internal/types"
)

// SourcePolicy is the compatibility seam used by consumers that only need an
// all-or-nothing answer for a set of source IDs.  Implementations must derive
// the answer from the shared Reviewer verdict instead of maintaining another
// source authorization rule.
type SourcePolicy interface {
	Validate(context.Context, string, string, []string) error
}

// Reviewer is the domain seam for source revalidation.  The caller decides
// how to present or aggregate the per-source verdicts; it does not get to
// reimplement source authorization, currentness, or coordinate checks.
type Reviewer interface {
	Review(context.Context, string, evidence.Actor, []string) ([]Verdict, error)
}

// Verdict is one source's answer from the shared source reviewer.  Chunk and
// DisplayTitle are included because read and delivery adapters need the
// already-reviewed projection to present context and frozen input without
// querying or revalidating the source a second time.
type Verdict struct {
	SourceID     string
	Source       evidence.Source
	Chunk        types.Chunk
	DisplayTitle string
	Usable       bool
	AssetDeny    evidence.DenyReason
	Status       evidence.SourceStatus
	Detail       string
}
