package candidateadoption

import (
	"github.com/Tencent/WeKnora/internal/lingdoc/operationcore"
)

// adoptionDigestVersion is the digest rule version this build writes.
//
// The version is bumped whenever the digest payload changes shape. A row
// written under an older rule carries a digest this build cannot interpret,
// so the version is compared before the digest is: see
// compareAdoptionDigest.
//
// Version 1 is the rule that had no version prefix at all: the six request
// fields, hashed by hand. Version 2 is the same six fields with the version
// prefix in front.
const adoptionDigestVersion = 2

// adoptionRequestDigest is the payload the candidate adoption request digest
// is computed over.
//
// The payload is a dedicated struct on purpose. AcceptCandidateInput also
// carries IdempotencyKey and ActorID, and those two are lookup keys rather
// than request content: hashing them would make every historical idempotency
// row mismatch, not only the rows that carry a new field.
type adoptionRequestDigest struct {
	// DigestVersion is the first field on purpose. Go's json.Marshal writes
	// struct fields in declaration order, so the version prefix always sits
	// in the same place and a digest says which rule produced it without a
	// side table.
	DigestVersion            int     `json:"digest_version"`
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
// The digest of an already issued request is therefore unchanged by the
// move, and the idempotency rows that carry it still match.
func requestHash(in AcceptCandidateInput) string {
	digest, _ := operationcore.RequestHash(adoptionRequestDigest{
		DigestVersion:            adoptionDigestVersion,
		ProjectID:                in.ProjectID,
		ChapterID:                in.ChapterID,
		CandidateID:              in.CandidateID,
		ExpectedChapterVersionID: in.ExpectedChapterVersionID,
		ExpectedSpecRevision:     in.ExpectedSpecRevision,
		ReplaceExisting:          in.ReplaceExisting,
	})
	return digest
}

// compareAdoptionDigest decides what an existing idempotency row means for
// this request.
//
// The version is compared first, and that order is the whole point. A row
// written under an older digest rule carries a digest this build cannot
// interpret, so the answer is "this key belongs to an older rule" -- the
// caller retries with a new key -- and not "this key carries a different
// request", which would send the caller looking for a request that never
// changed. The two situations need two different recovery steps, so they get
// two different answers.
//
// Every place that compares an adoption digest goes through this one
// function: the replay path, the write transaction and the pending probe
// cannot disagree about the same key.
func compareAdoptionDigest(row adoptionIdempotencyRow, digest string) error {
	if row.DigestVersion != adoptionDigestVersion {
		return ErrIdempotencyKeyExpired
	}
	if row.RequestHash != digest {
		return ErrIdempotencyConflict
	}
	return nil
}
