package workspacecore

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type operationSQLSourcePolicy struct {
	db        *sql.DB
	denied    bool
	afterRead func() error
	calls     int
}

func (p *operationSQLSourcePolicy) Validate(ctx context.Context, projectID, actorID string, sourceIDs []string) error {
	p.calls++
	var value int
	if err := p.db.QueryRowContext(ctx, "SELECT 1").Scan(&value); err != nil {
		return err
	}
	if p.denied {
		return ErrSourceUnavailable
	}
	if p.afterRead != nil {
		fn := p.afterRead
		p.afterRead = nil
		return fn()
	}
	return nil
}

func operationSourceFixture(t *testing.T) (*Service, Actor, string, string, *operationSQLSourcePolicy) {
	t.Helper()
	s := testStore(t, t.TempDir()+"/sources.db")
	actor := Actor{TenantID: 81, UserID: "owner"}
	seedTenantMember(t, s, actor)
	ctx := context.Background()
	raw, _, _, err := s.CreateProject(ctx, actor, "create-sources", CreateProjectInput{Name: "sources", TemplateID: "template-demo"})
	require.NoError(t, err)
	project := asProject(t, raw)
	_, _, _, err = s.SaveSpec(ctx, actor, project.ID, "spec-sources", SaveSpecInput{ExpectedSpecRevision: 0, Fields: map[string]string{"research_subject": "test", "research_goal": "test"}})
	require.NoError(t, err)
	_, _, _, err = s.ActivateProject(ctx, actor, project.ID, "activate-sources", ActivateProjectInput{ExpectedSpecRevision: 1, ExpectedProjectVersion: 2, ReviewedProjectVersion: 2})
	require.NoError(t, err)
	chapters, err := s.ListChapters(ctx, actor, project.ID)
	require.NoError(t, err)
	require.NotEmpty(t, chapters)
	db, err := s.repository.(*GORMRepository).db.DB()
	require.NoError(t, err)
	db.SetMaxOpenConns(1)
	policy := &operationSQLSourcePolicy{db: db}
	s.sources = policy
	return s, actor, project.ID, chapters[0].ID, policy
}

func TestOperationSourcePolicyUsesSingleConnectionOutsideWriteAndReplay(t *testing.T) {
	s, actor, projectID, chapterID, policy := operationSourceFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	input := SaveWorkingCopyInput{ExpectedSpecRevision: 1, ExpectedWorkingCopyRevision: 1, BodyMarkdown: "draft [[source:one]]", SourceIDs: []string{"one"}}
	first, _, replayed, err := s.SaveWorkingCopy(ctx, actor, projectID, chapterID, "save-sources", input)
	require.NoError(t, err)
	require.False(t, replayed)
	again, _, replayed, err := s.SaveWorkingCopy(ctx, actor, projectID, chapterID, "save-sources", input)
	require.NoError(t, err)
	require.True(t, replayed)
	require.Equal(t, first, again)
	policy.denied = true
	_, _, _, err = s.SaveWorkingCopy(ctx, actor, projectID, chapterID, "save-sources", input)
	require.ErrorIs(t, err, ErrSourceUnavailable)
	policy.denied = false
	commit := CommitWorkingCopyInput{ExpectedSpecRevision: 1, ExpectedWorkingCopyRevision: 2}
	_, _, replayed, err = s.CommitWorkingCopy(ctx, actor, projectID, chapterID, "commit-sources", commit)
	require.NoError(t, err)
	require.False(t, replayed)
	_, _, replayed, err = s.CommitWorkingCopy(ctx, actor, projectID, chapterID, "commit-sources", commit)
	require.NoError(t, err)
	require.True(t, replayed)
	policy.denied = true
	_, _, _, err = s.CommitWorkingCopy(ctx, actor, projectID, chapterID, "commit-sources", commit)
	require.ErrorIs(t, err, ErrSourceUnavailable)
	require.Equal(t, 6, policy.calls)
}

func TestOperationSourcePreflightRejectsChangedBasisBeforeCommit(t *testing.T) {
	for _, change := range []string{"sources", "project", "working_revision", "permission"} {
		t.Run(change, func(t *testing.T) {
			s, actor, projectID, chapterID, policy := operationSourceFixture(t)
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			_, _, _, err := s.SaveWorkingCopy(ctx, actor, projectID, chapterID, "save-sources", SaveWorkingCopyInput{ExpectedSpecRevision: 1, ExpectedWorkingCopyRevision: 1, BodyMarkdown: "draft [[source:one]]", SourceIDs: []string{"one"}})
			require.NoError(t, err)
			policy.afterRead = func() error {
				var err error
				switch change {
				case "sources":
					_, err = policy.db.ExecContext(ctx, `UPDATE lingdoc_working_copies SET source_ids_json='["two"]' WHERE project_id=? AND chapter_id=?`, projectID, chapterID)
				case "project":
					_, err = policy.db.ExecContext(ctx, "UPDATE lingdoc_projects SET project_version=project_version+1 WHERE id=?", projectID)
				case "working_revision":
					_, err = policy.db.ExecContext(ctx, "UPDATE lingdoc_working_copies SET working_copy_revision=working_copy_revision+1 WHERE project_id=? AND chapter_id=?", projectID, chapterID)
				case "permission":
					_, err = policy.db.ExecContext(ctx, "DELETE FROM lingdoc_members WHERE project_id=?", projectID)
				}
				return err
			}
			_, _, _, err = s.CommitWorkingCopy(ctx, actor, projectID, chapterID, "commit-racing", CommitWorkingCopyInput{ExpectedSpecRevision: 1, ExpectedWorkingCopyRevision: 2})
			if change == "permission" {
				require.Error(t, err)
				require.False(t, errors.Is(err, context.DeadlineExceeded))
			} else {
				require.ErrorIs(t, err, ErrVersionConflict)
			}
			var count int
			require.NoError(t, policy.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM lingdoc_chapter_versions WHERE project_id=? AND chapter_id=?", projectID, chapterID).Scan(&count))
			require.Zero(t, count)
		})
	}
}

func TestRevokedSourceReplayPreservesResultAndResumesAfterReauthorization(t *testing.T) {
	s, actor, projectID, chapterID, policy := operationSourceFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	key := "revocation-replay"
	input := SaveWorkingCopyInput{ExpectedSpecRevision: 1, ExpectedWorkingCopyRevision: 1, BodyMarkdown: "saved [[source:one]]", SourceIDs: []string{"one"}}
	first, status, replayed, err := s.SaveWorkingCopy(ctx, actor, projectID, chapterID, key, input)
	require.NoError(t, err)
	require.False(t, replayed)
	identity := OperationIdentity{Actor: actor, Operation: "saveWorkingCopy", Target: projectID + "/" + chapterID, Key: key}
	var originalOperation OperationResult
	var originalCopy WorkingCopy
	require.NoError(t, s.repository.Transaction(ctx, TransactionOptions{ReadOnly: true}, func(tx Transaction) error {
		var found bool
		originalOperation, found, err = tx.Operation(identity)
		require.True(t, found)
		if err != nil {
			return err
		}
		originalCopy, err = tx.WorkingCopy(projectID, chapterID)
		return err
	}))
	policy.denied = true
	denied, deniedStatus, deniedReplay, err := s.SaveWorkingCopy(ctx, actor, projectID, chapterID, key, input)
	require.ErrorIs(t, err, ErrSourceUnavailable)
	require.Nil(t, denied)
	require.Zero(t, deniedStatus)
	require.False(t, deniedReplay)
	require.NoError(t, s.repository.Transaction(ctx, TransactionOptions{ReadOnly: true}, func(tx Transaction) error {
		operation, found, e := tx.Operation(identity)
		require.NoError(t, e)
		require.True(t, found)
		require.Equal(t, originalOperation, operation)
		copy, e := tx.WorkingCopy(projectID, chapterID)
		require.NoError(t, e)
		require.Equal(t, originalCopy, copy)
		return nil
	}))
	policy.denied = false
	again, againStatus, replayed, err := s.SaveWorkingCopy(ctx, actor, projectID, chapterID, key, input)
	require.NoError(t, err)
	require.True(t, replayed)
	require.Equal(t, status, againStatus)
	require.Equal(t, first, again)
	current, err := s.GetWorkingCopy(ctx, actor, projectID, chapterID)
	require.NoError(t, err)
	require.Equal(t, originalCopy, current)
}
