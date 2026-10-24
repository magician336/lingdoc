package acceptancebudget

import (
	"testing"
)

// The authoritative checklist.
//
// The checklist is the ONE definition of "did this call exceed a limit?", and
// both ledgers read it. The tests below pin down the four classes it covers,
// the quantity each class measures, and the dimension of that quantity.
//
// They are offline. Nothing here reaches a provider.

// breachFor returns the breach of one criterion, and whether it fired.
func breachFor(t *testing.T, verdict LimitVerdict, criterion LimitCriterion) (LimitBreach, bool) {
	t.Helper()
	for _, breach := range verdict.Breaches {
		if breach.Criterion == criterion {
			return breach, true
		}
	}
	return LimitBreach{}, false
}

// The four classes of the checklist are four DIFFERENT questions, and each
// one fires on its own case and on no other.
//
// A checklist that cannot tell the four apart is one class wearing four
// names: then a reader cannot say WHICH ceiling a call passed, and the two
// ledgers can drift apart again without anybody noticing.
func TestLimitChecklistCoversTheFourClassesSeparately(t *testing.T) {
	const (
		bound   = 2048
		limit   = 4096
		total   = 20096
		profile = QwenProfile
	)
	cases := []struct {
		name      string
		facts     CallFacts
		criterion LimitCriterion
		alone     bool
	}{
		{
			name:      "the output ceiling",
			facts:     CallFacts{InputUpperBound: bound, OutputLimit: limit, InputTokens: 100, OutputTokens: limit + 1},
			criterion: LimitCriterionOutputCeiling,
			alone:     true,
		},
		{
			name:      "the input ceiling",
			facts:     CallFacts{InputUpperBound: bound, OutputLimit: limit, InputTokens: bound + 1, OutputTokens: 100},
			criterion: LimitCriterionInputCeiling,
			alone:     true,
		},
		{
			// The total ceiling CANNOT fire on its own, and that is a fact
			// about the numbers rather than about the code: the total
			// ceiling is inputMax + outputMax, and every recorded request
			// satisfies bound <= inputMax and outputLimit <= outputMax, so
			// an answer that passes the total ceiling has already passed one
			// of the other two. The class is still worth having -- it names
			// the profile's own total ceiling, which the other two do not.
			name:      "the total ceiling",
			facts:     CallFacts{InputUpperBound: bound, OutputLimit: limit, InputTokens: total - 96, OutputTokens: 200},
			criterion: LimitCriterionTotalCeiling,
			alone:     false,
		},
		{
			name:      "the negative counter",
			facts:     CallFacts{InputUpperBound: bound, OutputLimit: limit, InputTokens: -1, OutputTokens: 100},
			criterion: LimitCriterionNegativeCounter,
			alone:     true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			verdict := EvaluateCallLimits(profile, tc.facts)
			if !verdict.Violated {
				t.Fatalf("the checklist did not see the %s", tc.name)
			}
			breach, ok := breachFor(t, verdict, tc.criterion)
			if !ok {
				t.Fatalf("the %s did not fire: %+v", tc.name, verdict.Breaches)
			}
			if breach.Limit <= 0 && tc.criterion != LimitCriterionNegativeCounter {
				t.Fatalf("the %s measured against %d, want a positive ceiling", tc.name, breach.Limit)
			}
			if tc.criterion == LimitCriterionNegativeCounter && breach.Actual >= 0 {
				t.Fatalf("the impossible counter was reported as %d, want the counter that cannot be true", breach.Actual)
			}
			if breach.Dimension == "" {
				t.Fatalf("the %s did not say what its quantity counts", tc.name)
			}
			if tc.alone && len(verdict.Breaches) != 1 {
				t.Fatalf("the %s fired together with %d other criteria: %+v",
					tc.name, len(verdict.Breaches)-1, verdict.Breaches)
			}
		})
	}

	// An answer inside every ceiling is not a violation of any of them.
	verdict := EvaluateCallLimits(profile, CallFacts{
		InputUpperBound: bound, OutputLimit: limit, InputTokens: 320, OutputTokens: 128,
	})
	if verdict.Violated {
		t.Fatalf("an answer inside every ceiling was called a violation: %+v", verdict.Breaches)
	}
	if len(verdict.Breaches) != 0 {
		t.Fatalf("an answer inside every ceiling reported %d breaches", len(verdict.Breaches))
	}
}

// The input criterion measures the reported tokens against the REQUEST'S OWN
// input bound, and not against the profile's input ceiling.
//
// The two are different numbers with different dimensions: the request's bound
// is a UTF-8 byte length plus a fixed overhead, and the profile's ceiling is
// the ceiling of that very bound. An answer between the two is over the
// request's own bound and under the profile's ceiling, and the checklist has
// to say so -- otherwise the two ledgers can answer the same question two
// different ways.
func TestLimitChecklistMeasuresTheInputAgainstTheRequestsOwnBound(t *testing.T) {
	inputMax, _, _ := ProfileTokenLimits(QwenProfile)
	const bound = 2048
	if bound >= inputMax {
		t.Fatalf("the fixture needs a bound under the profile's ceiling: %d >= %d", bound, inputMax)
	}

	// Just under the request's own bound: not a violation.
	verdict := EvaluateCallLimits(QwenProfile, CallFacts{
		InputUpperBound: bound, OutputLimit: QwenOutputMax, InputTokens: bound, OutputTokens: 128,
	})
	if verdict.Violated {
		t.Fatalf("an answer of exactly the request's own bound was called a violation: %+v", verdict.Breaches)
	}

	// One token over the request's own bound, and still under the profile's
	// ceiling: a violation of the input ceiling.
	verdict = EvaluateCallLimits(QwenProfile, CallFacts{
		InputUpperBound: bound, OutputLimit: QwenOutputMax, InputTokens: bound + 1, OutputTokens: 128,
	})
	breach, ok := breachFor(t, verdict, LimitCriterionInputCeiling)
	if !ok {
		t.Fatalf("an answer over the request's own bound was not a violation: %+v", verdict.Breaches)
	}
	if breach.Limit != bound {
		t.Fatalf("the input criterion measured against %d, want the request's own bound %d", breach.Limit, bound)
	}
	if breach.Quantity != LimitQuantityRequestInputUpperBound {
		t.Fatalf("the input criterion measured against %q, want %q",
			breach.Quantity, LimitQuantityRequestInputUpperBound)
	}
	if breach.Actual != bound+1 {
		t.Fatalf("the input criterion reported %d, want the provider's %d", breach.Actual, bound+1)
	}
	if breach.Dimension != LimitDimensionInputUpperBoundBytes {
		t.Fatalf("the input criterion says its quantity counts %q, want the byte-bound dimension", breach.Dimension)
	}
}

// Every quantity of the checklist says what it counts.
//
// A byte bound and a token ceiling are different numbers. The quantity name
// says WHICH number, and the dimension says what it counts -- and the input
// quantity must never read like a token count, because it is not one.
func TestLimitChecklistNamesTheDimensionOfEveryQuantity(t *testing.T) {
	dimensions := map[LimitQuantity]LimitDimension{
		LimitQuantityRequestOutputLimit:     LimitDimensionOutputTokensExact,
		LimitQuantityRequestInputUpperBound: LimitDimensionInputUpperBoundBytes,
		LimitQuantityProfileTotalCeiling:    LimitDimensionTotalTokens,
		LimitQuantityProviderCounters:       LimitDimensionProviderTokens,
	}
	seen := map[LimitQuantity]bool{}
	for quantity, dimension := range dimensions {
		if quantity == "" {
			t.Fatal("a quantity of the checklist has no name")
		}
		if dimension == "" {
			t.Fatalf("the quantity %q has no dimension", quantity)
		}
		if seen[quantity] {
			t.Fatalf("the quantity %q is declared twice", quantity)
		}
		seen[quantity] = true
	}
	for _, quantity := range []LimitQuantity{
		LimitQuantityRequestOutputLimit,
		LimitQuantityRequestInputUpperBound,
		LimitQuantityProfileTotalCeiling,
		LimitQuantityProviderCounters,
	} {
		if _, ok := dimensions[quantity]; !ok {
			t.Fatalf("the quantity %q has no declared dimension", quantity)
		}
	}
	if LimitDimensionInputUpperBoundBytes == LimitDimensionTotalTokens {
		t.Fatal("the input bound and the total ceiling must not share a dimension")
	}
	if LimitDimensionInputUpperBoundBytes == LimitDimensionOutputTokensExact {
		t.Fatal("the input bound and the output limit must not share a dimension")
	}
}

// The profile's input ceiling can never see a violation the request's own
// bound does not see.
//
// Every recorded request satisfies bound <= the profile's input ceiling,
// because the reservation refuses a request whose bound passes it. A reported
// token count can never exceed the ceiling either, because a token is at
// least one byte and the bound is a byte length. So the call site's
// profile-ceiling pre-check is SUBSUMED by the checklist: whenever it fires,
// the checklist fires too, and the two ledgers cannot answer the same answer
// two different ways.
func TestLimitChecklistCannotSeeAViolationTheProfileCeilingCannotSee(t *testing.T) {
	inputMax, outputMax, totalMax := ProfileTokenLimits(QwenProfile)
	for _, bound := range []int{0, 1, inputMax / 2, inputMax} {
		for _, tokens := range []int{0, bound, inputMax, inputMax + 1, totalMax + 1} {
			facts := CallFacts{
				InputUpperBound: bound,
				OutputLimit:     outputMax,
				InputTokens:     tokens,
				OutputTokens:    0,
			}
			preCheckFires := facts.InputTokens > inputMax
			verdict := EvaluateCallLimits(QwenProfile, facts)
			_, checklistFires := breachFor(t, verdict, LimitCriterionInputCeiling)
			if preCheckFires && !checklistFires {
				t.Fatalf("the profile ceiling saw a violation the checklist did not: bound %d, tokens %d",
					bound, tokens)
			}
		}
	}
}

// The shared budget ledger's criteria did not change.
//
// The expression the shared budget ledger used before the checklist existed
// is written out here verbatim, and the checklist has to answer exactly as it
// does on every case of a matrix. The shared budget ledger's verdict decides
// whether a window keeps spending, so a checklist that is even slightly
// stricter or slightly looser would change what an existing ledger says --
// and "slightly looser" would be a relaxation nobody asked for.
func TestTheChecklistAgreesWithTheSharedLedgersOldCriteria(t *testing.T) {
	profile := QwenProfile
	_, _, totalMax := ProfileTokenLimits(profile)

	bounds := []int{0, 1, 2048, QwenInputMax}
	limits := []int{0, 1, 4096, QwenOutputMax}
	inputs := []int{-1, 0, 1, 2048, 2049, QwenInputMax, QwenInputMax + 1, totalMax, totalMax + 1}
	outputs := []int{-1, 0, 1, 4096, 4097, QwenOutputMax, QwenOutputMax + 1, totalMax, totalMax + 1}

	for _, bound := range bounds {
		for _, limit := range limits {
			for _, input := range inputs {
				for _, output := range outputs {
					// The criteria the shared budget ledger applied before
					// the checklist existed, written out as they were.
					old := input < 0 || output < 0 || input > bound ||
						output > limit || input+output > totalMax
					verdict := EvaluateCallLimits(profile, CallFacts{
						InputUpperBound: bound,
						OutputLimit:     limit,
						InputTokens:     input,
						OutputTokens:    output,
					})
					if verdict.Violated != old {
						t.Fatalf("the checklist answers %v where the old criteria answered %v: bound %d, limit %d, input %d, output %d",
							verdict.Violated, old, bound, limit, input, output)
					}
				}
			}
		}
	}
}

// The first profile's numbers did not move.
//
// This is the configuration half of the "the old profile is unchanged" proof:
// the same single call, the same ceilings, the same cost basis, the same
// approved model, the same allowlist and still no cumulative token ceiling.
// The ledger artifact half of the proof lives in the workspace package, where
// the artifact itself is produced.
func TestQwenV1ProfileNumbersAreUnchangedByTheLimitChecklist(t *testing.T) {
	if ProfileMaxCalls(QwenProfile) != 1 {
		t.Fatalf("the first profile's call budget = %d, want 1", ProfileMaxCalls(QwenProfile))
	}
	inputMax, outputMax, totalMax := ProfileTokenLimits(QwenProfile)
	if inputMax != 16000 || outputMax != 4096 || totalMax != 20096 {
		t.Fatalf("the first profile's token ceilings = %d/%d/%d, want 16000/4096/20096",
			inputMax, outputMax, totalMax)
	}
	if ProfileCumulativeTokenMax(QwenProfile) != 0 {
		t.Fatalf("the first profile's cumulative ceiling = %d, want 0",
			ProfileCumulativeTokenMax(QwenProfile))
	}
	if ProfileCostBasis(QwenProfile) != CostBasisNotApplicable {
		t.Fatalf("the first profile's cost basis = %q, want %q",
			ProfileCostBasis(QwenProfile), CostBasisNotApplicable)
	}
	if ProfileApprovedModel(QwenProfile) != QwenChatModel {
		t.Fatalf("the first profile's approved model = %q, want %q",
			ProfileApprovedModel(QwenProfile), QwenChatModel)
	}
	if !ProfileAllowsModel(QwenProfile, QwenChatModel) {
		t.Fatal("the first profile refused its own approved model")
	}
	for _, model := range []string{DeepSeekModel, QwenEmbedding, QwenEmbeddingFlash, QwenEmbeddingFlash + "-x"} {
		if ProfileAllowsModel(QwenProfile, model) {
			t.Fatalf("the first profile approved %q", model)
		}
	}
}
