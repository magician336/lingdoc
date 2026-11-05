package delivery

import (
	"github.com/Tencent/WeKnora/internal/lingdoc/access"
	"maps"
)

// AccessCurrentnessInput is the single translation from a frozen delivery
// input into the shared policy contract. Provenance survives this boundary;
// its digest is an identity and never substitutes for a live permission check.
func AccessCurrentnessInput(input DeliveryInput) access.CurrentnessInput {
	result := access.CurrentnessInput{
		Kind: access.DeliveryInput, ProjectID: input.ProjectID,
		ProjectVersion: int64(input.ProjectVersion), SpecRevision: int64(input.SpecRevision),
		TemplateID: input.Template.ID, TemplateVersion: input.Template.Version,
		RulesetHash: FrozenRuleSetHash(input), ContextRevision: FrozenContextRevision(input),
		Spec: maps.Clone(input.Spec), TemplateCopyID: input.TemplateCopyID,
		TemplateCopyVersion: input.TemplateCopyVersion, TemplateCopyContentHash: input.TemplateCopyContentHash,
	}
	for _, asset := range input.AssetVersions {
		result.AssetVersions = append(result.AssetVersions, access.AssetVersion{AssetID: asset.AssetID, Revision: asset.Revision})
	}
	for _, c := range input.Chapters {
		result.Chapters = append(result.Chapters, access.ChapterVersion{ChapterID: c.ChapterID, ChapterVersionID: c.ChapterVersionID})
	}
	for _, s := range input.Sources {
		result.Sources = append(result.Sources, access.Source{ID: s.ID, ProjectID: s.ProjectID, AssetID: s.AssetID, AssetRevision: s.AssetRevision, Locator: s.Locator, QuotedTextHash: s.QuotedTextHash})
	}
	if p := input.SpecProvenance; p != nil {
		result.SpecProvenance = &access.ProvenanceSnapshot{ProjectID: p.ProjectID, SpecRevision: p.SpecRevision, ContextRevision: p.ContextRevision, TemplateCopyVersion: p.TemplateCopyVersion, Digest: p.Digest}
		for _, f := range p.Fields {
			result.SpecProvenance.Fields = append(result.SpecProvenance.Fields, access.ProvenanceField{FieldKey: f.FieldKey, ValueHash: f.ValueHash, PinID: f.PinID})
		}
	}
	return result
}

// FrozenContextRevision reads only captured data. Legacy snapshots may carry
// the context in their provenance; never substitute today's project revision.
func FrozenContextRevision(input DeliveryInput) int64 {
	if input.ContextRevision != 0 {
		return input.ContextRevision
	}
	if input.SpecProvenance != nil {
		return input.SpecProvenance.ContextRevision
	}
	return 0
}
