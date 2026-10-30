package workspacecore

// AcademicEvidenceSnapshot binds immutable original bytes and parser-aligned
// source text to the source and academic decisions reviewed for one card.
// The references carry hashes and sizes only; neither carries document bytes.
type AcademicEvidenceSnapshot struct {
	SourceID      string                        `json:"source_id"`
	KnowledgeID   string                        `json:"knowledge_id"`
	AssetID       string                        `json:"asset_id"`
	AssetRevision int                           `json:"asset_revision"`
	Locator       string                        `json:"locator"`
	Page          int                           `json:"page,omitempty"`
	RawRef        AcademicOriginalSnapshotRef   `json:"raw_ref"`
	TextRef       AcademicOriginalSnapshotRef   `json:"text_ref"`
	AnchorRefs    []VersionRef                  `json:"anchor_refs"`
	Associations  []AcademicSnapshotAssociation `json:"associations"`
}

type AcademicSnapshotAssociation struct {
	AssociationRef        VersionRef `json:"association_ref"`
	CandidateRef          VersionRef `json:"candidate_ref"`
	DOI                   string     `json:"doi"`
	CatalogVersion        string     `json:"catalog_version"`
	PolicySnapshotVersion string     `json:"policy_snapshot_version"`
}
