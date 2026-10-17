package workspace

import (
	"context"

	"github.com/Tencent/WeKnora/internal/evidence"
	"github.com/Tencent/WeKnora/internal/lingdoc/sourcepolicy"
)

// legacySourceReviewer keeps the pre-integration SourceService constructor
// usable for isolated callers. It is deliberately an adapter at the old
// evidence boundary; production wiring injects SourceRuntime.sourceRevalidator
// and therefore never constructs an independent source decision.
type legacyEvidenceReviewer struct {
	policy evidence.SourcePolicy
	source evidence.Source
}

func legacySourceReviewer(gateway evidence.AssetGateway, origins evidence.OriginReader, bindings evidence.RevisionReader, source evidence.Source) sourcepolicy.Reviewer {
	return legacyEvidenceReviewer{policy: evidence.NewSourcePolicy(gateway, origins, bindings), source: source}
}

func (r legacyEvidenceReviewer) Review(ctx context.Context, projectID string, actor evidence.Actor, sourceIDs []string) ([]sourcepolicy.Verdict, error) {
	if r.policy == nil || len(sourceIDs) != 1 || sourceIDs[0] != r.source.ID {
		return nil, ErrSourceUnavailable
	}
	checked, err := r.policy.Validate(ctx, projectID, actor, []evidence.Source{r.source})
	if err != nil {
		return nil, err
	}
	if checked == nil {
		return nil, ErrSourceUnavailable
	}
	verdict := sourcepolicy.Verdict{SourceID: r.source.ID, Source: r.source}
	if len(checked.Usable) > 0 {
		verdict.Usable = true
		verdict.Source = checked.Usable[0]
		return []sourcepolicy.Verdict{verdict}, nil
	}
	if len(checked.Unusable) == 0 {
		return nil, ErrSourceUnavailable
	}
	unusable := checked.Unusable[0]
	verdict.AssetDeny = unusable.AssetDeny
	verdict.Status = unusable.Status
	verdict.Detail = unusable.Detail
	return []sourcepolicy.Verdict{verdict}, nil
}
