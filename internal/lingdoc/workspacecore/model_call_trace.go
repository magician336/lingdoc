package workspacecore

import "time"

// ProviderTraceBasis is what a reader can check about one model call when the
// provider sent no request identifier of its own.
//
// It is the alternative to a provider-side request id, and it is not a
// consolation prize: the endpoint, the model, the request parameters, the time
// window and the usage together identify one call well enough to be reconciled
// against a provider's own records. A missing request id is a fact; a missing
// basis would be a gap.
//
// The fields are the ledger's own. This type adds no vocabulary of its own --
// every value here is read from a column that already exists.
type ProviderTraceBasis struct {
	// Provider is the provider that answered.
	Provider string
	// ModelID is the model that answered.
	ModelID string
	// RunMode says whether the call reached a real provider.
	RunMode string
	// ParametersJSON is the JSON text of the parameters that were sent.
	ParametersJSON string
	// InputTokens and OutputTokens are the token counts the provider reported.
	// They are zero for a call that failed before the provider reported any.
	InputTokens  int
	OutputTokens int
	// DurationMS is how long the call took, and CreatedAt when it was recorded.
	DurationMS int64
	CreatedAt  time.Time
	// SourceKind is the kind of the product action the call belongs to, and
	// SourceID is that action's id. They are what joins this row to the product
	// side -- to the candidate, the turn, the run.
	SourceKind string
	SourceID   string
}

// ProviderTraceBasis returns the facts a reader can check instead of a
// provider request id.
//
// It is the read side of the ledger's promise: a row that says which model
// answered which product action, when, and at what usage can be reconciled
// even when the provider sent no identifier.
func (e ModelCallLedgerEntry) ProviderTraceBasis() ProviderTraceBasis {
	return ProviderTraceBasis{
		Provider:       e.Provider,
		ModelID:        e.ModelID,
		RunMode:        e.RunMode,
		ParametersJSON: e.ParametersJSON,
		InputTokens:    e.InputTokens,
		OutputTokens:   e.OutputTokens,
		DurationMS:     e.DurationMS,
		CreatedAt:      e.CreatedAt,
		SourceKind:     e.SourceKind,
		SourceID:       e.SourceID,
	}
}

// Complete reports whether the basis carries every fact a reader needs to
// reconcile this call.
//
// The four that must be there are the provider, the model, the product action
// and the time window: without them a row cannot even be attributed to a
// call. The usage may legitimately be zero -- a call that failed before the
// provider reported anything has no usage, and "no usage" is a fact, not a
// gap -- so it is reported but not required.
func (b ProviderTraceBasis) Complete() bool {
	return b.Provider != "" && b.ModelID != "" && b.SourceKind != "" &&
		b.SourceID != "" && !b.CreatedAt.IsZero()
}
