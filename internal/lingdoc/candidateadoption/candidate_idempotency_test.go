package candidateadoption

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestRequestDigestKeepsItsValueWhenTheImplementationMoves pins the promise
// the digest version relies on: moving the computation to its single owner
// does not change the digest of an already issued request, so the idempotency
// rows that carry the old digest still match.
//
// The old way is written out here on purpose. It is the two-step spelling --
// marshal into a variable, digest the variable -- and it is exactly the
// spelling this package used to own.
func TestRequestDigestKeepsItsValueWhenTheImplementationMoves(t *testing.T) {
	chapterVersionID := "chapter-version-1"
	input := AcceptCandidateInput{
		ProjectID:                "project-1",
		ChapterID:                "chapter-1",
		CandidateID:              "candidate-1",
		ExpectedChapterVersionID: &chapterVersionID,
		ExpectedSpecRevision:     7,
		ReplaceExisting:          true,
	}
	payload := adoptionRequestDigest{
		ProjectID:                input.ProjectID,
		ChapterID:                input.ChapterID,
		CandidateID:              input.CandidateID,
		ExpectedChapterVersionID: input.ExpectedChapterVersionID,
		ExpectedSpecRevision:     input.ExpectedSpecRevision,
		ReplaceExisting:          input.ReplaceExisting,
	}

	encoded, err := json.Marshal(payload)
	require.NoError(t, err)
	sum := sha256.Sum256(encoded)
	legacyDigest := hex.EncodeToString(sum[:])

	require.Equal(t, legacyDigest, requestHash(input),
		"换实现之后，同一请求的摘要值必须与旧写法逐字相同")
}
