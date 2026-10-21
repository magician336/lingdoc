package candidateadoption

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/lingdoc/operationcore"
)

// legacyAdoptionRequestDigest is the payload shape the adoption digest had
// before it carried a version prefix: the six request fields and nothing
// else. It is written out here because the promise it carries is about the
// past -- a request issued under that rule must still hash to the same
// value after the implementation moved.
type legacyAdoptionRequestDigest struct {
	ProjectID                string  `json:"project_id"`
	ChapterID                string  `json:"chapter_id"`
	CandidateID              string  `json:"candidate_id"`
	ExpectedChapterVersionID *string `json:"expected_chapter_version_id"`
	ExpectedSpecRevision     int     `json:"expected_spec_revision"`
	ReplaceExisting          bool    `json:"replace_existing"`
}

// digestBothWays hashes one payload the old way and the new way.
//
// The old way is the two-step spelling -- marshal into a variable, digest the
// variable -- and it is exactly the spelling the adoption digest used to own.
// The new way is operationcore.RequestHash, the one owner of the rule.
func digestBothWays(t *testing.T, payload any) (legacy string, owner string) {
	t.Helper()
	encoded, err := json.Marshal(payload)
	require.NoError(t, err)
	sum := sha256.Sum256(encoded)
	legacy = hex.EncodeToString(sum[:])
	owner, err = operationcore.RequestHash(payload)
	require.NoError(t, err)
	return legacy, owner
}

// TestRequestDigestKeepsItsValueWhenTheImplementationMoves pins the promise
// the digest version relies on: moving the computation to its single owner
// does not change the digest of an already issued request, so the idempotency
// rows that carry the old digest still match.
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

	// The payload of a request issued before the version prefix existed.
	legacyPayload := legacyAdoptionRequestDigest{
		ProjectID:                input.ProjectID,
		ChapterID:                input.ChapterID,
		CandidateID:              input.CandidateID,
		ExpectedChapterVersionID: input.ExpectedChapterVersionID,
		ExpectedSpecRevision:     input.ExpectedSpecRevision,
		ReplaceExisting:          input.ReplaceExisting,
	}
	legacyDigest, ownerDigest := digestBothWays(t, legacyPayload)
	require.Equal(t, legacyDigest, ownerDigest,
		"换实现之后，同一请求的摘要值必须与旧写法逐字相同")

	// The payload of a request issued under the current rule.
	currentPayload := adoptionRequestDigest{
		DigestVersion:            adoptionDigestVersion,
		ProjectID:                input.ProjectID,
		ChapterID:                input.ChapterID,
		CandidateID:              input.CandidateID,
		ExpectedChapterVersionID: input.ExpectedChapterVersionID,
		ExpectedSpecRevision:     input.ExpectedSpecRevision,
		ReplaceExisting:          input.ReplaceExisting,
	}
	legacyDigest, ownerDigest = digestBothWays(t, currentPayload)
	require.Equal(t, legacyDigest, ownerDigest,
		"换实现之后，当前规则下的摘要值必须与旧写法逐字相同")
}

// TestRequestDigestCarriesTheVersionPrefix pins the version prefix itself.
//
// The prefix has to be the first field of the payload and it has to change
// the digest value: a version that does not change anything cannot tell two
// rules apart.
func TestRequestDigestCarriesTheVersionPrefix(t *testing.T) {
	input := AcceptCandidateInput{
		ProjectID: "project-1", ChapterID: "chapter-1", CandidateID: "candidate-1",
		ExpectedSpecRevision: 7,
	}
	encoded, err := json.Marshal(adoptionRequestDigest{
		DigestVersion:        adoptionDigestVersion,
		ProjectID:            input.ProjectID,
		ChapterID:            input.ChapterID,
		CandidateID:          input.CandidateID,
		ExpectedSpecRevision: input.ExpectedSpecRevision,
	})
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(string(encoded), `{"digest_version":2,`),
		"版本前缀必须是摘要结构的第一个字段，实际编码为 %s", string(encoded))

	legacyDigest, _ := digestBothWays(t, legacyAdoptionRequestDigest{
		ProjectID:            input.ProjectID,
		ChapterID:            input.ChapterID,
		CandidateID:          input.CandidateID,
		ExpectedSpecRevision: input.ExpectedSpecRevision,
	})
	require.NotEqual(t, legacyDigest, requestHash(input),
		"加了版本前缀之后，同一请求的摘要值必须与旧规则不同")
}

// TestIdempotencyKeyFromAnOlderDigestVersionExpiresOnEveryPath pins the
// three-state idempotency answer.
//
// A key issued under an older digest rule is not a conflict: the request
// never changed, the key did. The answer is "this key expired", and every
// path that looks at the key has to give that same answer -- otherwise the
// same key gets two different verdicts depending on which path ran.
func TestIdempotencyKeyFromAnOlderDigestVersionExpiresOnEveryPath(t *testing.T) {
	store := newCandidateAdoptionStore(t)
	ctx := context.Background()
	candidate := Candidate{
		ID: "candidate-1", ProjectID: "project-1", ChapterID: "chapter-1", RunID: "run-1",
		BodyMarkdown: "候选正文 [[source:source-1]]", SourceIDs: []string{"source-1"},
		Basis:    Basis{SpecRevision: 1, TemplateID: "template-demo", TemplateVersion: "1"},
		Validity: "fresh",
	}
	seedCandidateAdoptionWorkspace(t, store,
		Chapter{ID: "chapter-1", ProjectID: "project-1", SectionID: "question", Title: "问题"},
		candidate, 1)

	input := AcceptCandidateInput{
		ProjectID: "project-1", ChapterID: "chapter-1", CandidateID: "candidate-1", ActorID: "user-1",
		IdempotencyKey: "candidate-adoption-key-0001", ExpectedSpecRevision: 1,
	}
	// A row written before the digest rule carried a version: its digest is
	// the one the old rule produced.
	legacyDigest, _ := digestBothWays(t, legacyAdoptionRequestDigest{
		ProjectID:            input.ProjectID,
		ChapterID:            input.ChapterID,
		CandidateID:          input.CandidateID,
		ExpectedSpecRevision: input.ExpectedSpecRevision,
	})
	require.NoError(t, store.DB().Create(&adoptionIdempotencyRow{
		ID: uuid.NewString(), ProjectID: input.ProjectID, ChapterID: input.ChapterID,
		ActorID: input.ActorID, Key: input.IdempotencyKey, RequestHash: legacyDigest,
		DigestVersion: 1, ChapterVersionID: "version-1", ResponseJSON: "{}",
	}).Error)

	_, err := store.ReplayCandidateAcceptance(ctx, input)
	require.ErrorIs(t, err, ErrIdempotencyKeyExpired, "回放路径必须回答键过期")

	_, err = store.AcceptCandidate(ctx, input, candidate)
	require.ErrorIs(t, err, ErrIdempotencyKeyExpired, "写事务路径必须回答键过期")

	_, err = store.CandidateAcceptancePending(ctx, input)
	require.ErrorIs(t, err, ErrIdempotencyKeyExpired, "待决探测路径必须回答键过期")
}

// TestSameVersionDifferentDigestConflictsOnEveryPath pins the other half of
// the three-state answer: under the same digest version, a different digest
// is still a real conflict, on every path.
func TestSameVersionDifferentDigestConflictsOnEveryPath(t *testing.T) {
	store := newCandidateAdoptionStore(t)
	ctx := context.Background()
	candidate := Candidate{
		ID: "candidate-1", ProjectID: "project-1", ChapterID: "chapter-1", RunID: "run-1",
		BodyMarkdown: "候选正文 [[source:source-1]]", SourceIDs: []string{"source-1"},
		Basis:    Basis{SpecRevision: 1, TemplateID: "template-demo", TemplateVersion: "1"},
		Validity: "fresh",
	}
	seedCandidateAdoptionWorkspace(t, store,
		Chapter{ID: "chapter-1", ProjectID: "project-1", SectionID: "question", Title: "问题"},
		candidate, 1)

	input := AcceptCandidateInput{
		ProjectID: "project-1", ChapterID: "chapter-1", CandidateID: "candidate-1", ActorID: "user-1",
		IdempotencyKey: "candidate-adoption-key-0002", ExpectedSpecRevision: 1,
	}
	require.NoError(t, store.DB().Create(&adoptionIdempotencyRow{
		ID: uuid.NewString(), ProjectID: input.ProjectID, ChapterID: input.ChapterID,
		ActorID: input.ActorID, Key: input.IdempotencyKey, RequestHash: "another-request-digest",
		DigestVersion: adoptionDigestVersion, ChapterVersionID: "version-1", ResponseJSON: "{}",
	}).Error)

	_, err := store.ReplayCandidateAcceptance(ctx, input)
	require.ErrorIs(t, err, ErrIdempotencyConflict, "回放路径必须回答冲突")

	_, err = store.AcceptCandidate(ctx, input, candidate)
	require.ErrorIs(t, err, ErrIdempotencyConflict, "写事务路径必须回答冲突")

	_, err = store.CandidateAcceptancePending(ctx, input)
	require.ErrorIs(t, err, ErrIdempotencyConflict, "待决探测路径必须回答冲突")
}

// TestAcceptedCandidateRecordsTheDigestVersion pins the version column: the
// row a successful adoption writes says which digest rule produced its
// digest, so a later build can tell whether it can still compare it.
func TestAcceptedCandidateRecordsTheDigestVersion(t *testing.T) {
	store := newCandidateAdoptionStore(t)
	ctx := context.Background()
	candidate := Candidate{
		ID: "candidate-1", ProjectID: "project-1", ChapterID: "chapter-1", RunID: "run-1",
		BodyMarkdown: "候选正文 [[source:source-1]]", SourceIDs: []string{"source-1"},
		Basis:    Basis{SpecRevision: 1, TemplateID: "template-demo", TemplateVersion: "1"},
		Validity: "fresh",
	}
	seedCandidateAdoptionWorkspace(t, store,
		Chapter{ID: "chapter-1", ProjectID: "project-1", SectionID: "question", Title: "问题"},
		candidate, 1)

	input := AcceptCandidateInput{
		ProjectID: "project-1", ChapterID: "chapter-1", CandidateID: "candidate-1", ActorID: "user-1",
		IdempotencyKey: "candidate-adoption-key-0003", ExpectedSpecRevision: 1,
	}
	result, err := store.AcceptCandidate(ctx, input, candidate)
	require.NoError(t, err)
	require.False(t, result.Replayed)

	var row adoptionIdempotencyRow
	require.NoError(t, store.DB().
		Where("project_id = ? AND chapter_id = ? AND actor_id = ? AND idempotency_key = ?",
			input.ProjectID, input.ChapterID, input.ActorID, input.IdempotencyKey).
		First(&row).Error)
	require.Equal(t, adoptionDigestVersion, row.DigestVersion)
	require.Equal(t, requestHash(input), row.RequestHash)

	replayed, err := store.ReplayCandidateAcceptance(ctx, input)
	require.NoError(t, err)
	require.NotNil(t, replayed)
	require.True(t, replayed.Replayed)
}
