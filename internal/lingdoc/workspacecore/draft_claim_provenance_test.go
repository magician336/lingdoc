package workspacecore

import (
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"testing"
)

func TestDraftClaimProvenanceBecomesPermanentlyStaleOnEdit(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+uuid.NewString()+"?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&workingCopyRow{}, &DraftClaimProvenance{}))
	original := "前😀论断 [[source:s]]后"
	require.NoError(t, db.Create(&workingCopyRow{ProjectID: "p", ChapterID: "c", SpecRevision: 1, WorkingCopyRevision: 1, BodyMarkdown: original, SourceIDsJSON: `["s"]`, CitationUsagesJSON: `[]`, ReviewItemsJSON: `[]`}).Error)
	require.NoError(t, RecordDraftClaimProvenance(db, "p", "c", "generation", "candidate", "run", 1, 3, original, "论断 [[source:s]]", original))
	tx := gormTransaction{db: db}
	before, err := tx.WorkingCopy("p", "c")
	require.NoError(t, err)
	require.Len(t, before.ClaimProvenance, 1)
	require.Equal(t, "needs_revalidation", before.ClaimProvenance[0].Status)
	next := before
	next.WorkingCopyRevision++
	next.BodyMarkdown = original + "文字"
	require.NoError(t, tx.SaveWorkingCopy(before, next))
	after, err := tx.WorkingCopy("p", "c")
	require.NoError(t, err)
	require.Equal(t, "stale", after.ClaimProvenance[0].Status)
	reverted := after
	reverted.WorkingCopyRevision++
	reverted.BodyMarkdown = original
	require.NoError(t, tx.SaveWorkingCopy(after, reverted))
	latest, err := tx.WorkingCopy("p", "c")
	require.NoError(t, err)
	require.Equal(t, "stale", latest.ClaimProvenance[0].Status, "reverting text must not silently revive evidence")
	require.Error(t, RecordDraftClaimProvenance(db, "p", "c", "generation", "other", "run", 3, 2, original, "论断 [[source:s]]", original), "offset may not bisect UTF-16 surrogate pair")
}
