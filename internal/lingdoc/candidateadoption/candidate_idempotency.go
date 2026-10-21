package candidateadoption

import (
	"github.com/Tencent/WeKnora/internal/lingdoc/operationcore"
)

// adoptionRequestDigest is the payload the candidate adoption request digest
// is computed over.
//
// The payload is a dedicated struct on purpose. AcceptCandidateInput also
// carries IdempotencyKey and ActorID, and those two are lookup keys rather
// than request content: hashing them would make every historical idempotency
// row mismatch, not only the rows that carry a new field.
type adoptionRequestDigest struct {
	ProjectID                string  `json:"project_id"`
	ChapterID                string  `json:"chapter_id"`
	CandidateID              string  `json:"candidate_id"`
	ExpectedChapterVersionID *string `json:"expected_chapter_version_id"`
	ExpectedSpecRevision     int     `json:"expected_spec_revision"`
	ReplaceExisting          bool    `json:"replace_existing"`
}

// requestHash computes the digest of one candidate adoption request.
//
// The digest is computed by operationcore.RequestHash, the one owner of the
// request digest rule. Moving the implementation did not move the rule:
// operationcore.RequestHash is hex(sha256(json.Marshal(payload))), which is
// exactly what this package used to compute by hand over the same payload.
// The digest of an already issued request is therefore unchanged, and the
// idempotency rows that carry it still match.
func requestHash(in AcceptCandidateInput) string {
	digest, _ := operationcore.RequestHash(adoptionRequestDigest{
		ProjectID:                in.ProjectID,
		ChapterID:                in.ChapterID,
		CandidateID:              in.CandidateID,
		ExpectedChapterVersionID: in.ExpectedChapterVersionID,
		ExpectedSpecRevision:     in.ExpectedSpecRevision,
		ReplaceExisting:          in.ReplaceExisting,
	})
	return digest
}
