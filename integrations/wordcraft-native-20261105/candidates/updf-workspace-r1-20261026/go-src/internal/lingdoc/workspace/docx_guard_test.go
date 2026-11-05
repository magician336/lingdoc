package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	adoption "github.com/Tencent/WeKnora/internal/lingdoc/candidateadoption"
	tpl "github.com/Tencent/WeKnora/internal/lingdoc/template"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestDOCXGuardUsesPersistedLedgerBindingAndAtomicWriter(t *testing.T) {
	f := newCandidateOneConnFixture(t)
	adopted, err := f.adoption.AcceptCandidate(f.ctx, f.input)
	require.NoError(t, err)
	definition, err := f.runtime.templates.Get("template-demo", "1")
	require.NoError(t, err)
	set := tpl.RuleSet{ID: "docx-guard-rules", Version: "1", State: tpl.RuleSetDraft}
	for _, name := range []string{"nonempty_chapter", "source_available", "review_items_decided"} {
		set.Bindings = append(set.Bindings, tpl.RuleBinding{RuleID: "docx-" + name, DefinitionID: "computed/" + name, DefinitionVersion: "1", Origin: "builtin", Severity: "blocking", TriggerScenes: []string{"preflight", "write_check"}, Enabled: true, Owner: "lingdoc"})
	}
	published, err := tpl.PublishRuleSet(set)
	require.NoError(t, err)
	definition.Rules = nil
	definition.RuleSetSnapshot = &published
	definition.RuleSetRef = &tpl.RuleSetReference{ID: published.ID, Version: published.Version, Hash: published.RulesetHash}
	// Test one mapped chapter; full-template coverage is a separate gate.
	definition.Sections = definition.Sections[1:2]
	definition, err = tpl.WithHashes(definition)
	require.NoError(t, err)
	raw, err := json.Marshal(definition)
	require.NoError(t, err)
	require.NoError(t, f.db.Exec("UPDATE lingdoc_project_template_copies SET definition_json=?,content_hash=?,ruleset_hash=? WHERE id='actual-copy'", string(raw), definition.ContentHash, definition.RulesetHash).Error)
	require.NoError(t, f.db.Exec("ALTER TABLE lingdoc_project_template_copies ADD COLUMN ruleset_ref_json TEXT NOT NULL DEFAULT 'null'").Error)
	refJSON, err := json.Marshal(definition.RuleSetRef)
	require.NoError(t, err)
	require.NoError(t, f.db.Exec("UPDATE lingdoc_project_template_copies SET ruleset_ref_json=? WHERE id='actual-copy'", string(refJSON)).Error)
	var row struct{ ID, BodyMarkdown, SectionID string }
	require.NoError(t, f.db.Table("lingdoc_chapter_versions v").Select("v.id,v.body_markdown,c.section_id").Joins("JOIN lingdoc_chapters c ON c.id=v.chapter_id").Where("v.id=?", adopted.Chapter.CurrentVersionID).Scan(&row).Error)
	in := DOCXGuardInput{ProjectID: "project-1", Capability: "confirm:chapter-2", Scene: "preflight", ExpectedTemplateCopyVersion: 1, ExpectedRuleSetHash: definition.RulesetHash, Chapters: []DOCXGuardChapter{{SectionID: row.SectionID, ChapterID: "chapter-2", ChapterVersionID: row.ID, Body: row.BodyMarkdown}}}
	require.NoError(t, f.db.Exec("CREATE TABLE docx_guard_writes(id INTEGER PRIMARY KEY,proof TEXT)").Error)
	called := 0
	writer := func(tx *gorm.DB, proof DOCXGuardProof) error {
		called++
		require.Equal(t, []string{"source-1"}, proof.Sources)
		return tx.Exec("INSERT INTO docx_guard_writes(proof) VALUES(?)", proof.Binding.EffectiveHash).Error
	}
	require.NoError(t, f.runtime.GuardDOCX(f.ctx, f.actor, in, writer))
	require.Equal(t, 1, called)
	t.Run("ledger not erased by projection", func(t *testing.T) {
		bad := in
		bad.Chapters = append([]DOCXGuardChapter{}, in.Chapters...)
		bad.Chapters[0].Body = "text with removed marker"
		require.ErrorContains(t, f.runtime.GuardDOCX(f.ctx, f.actor, bad, writer), "PROJECTION_VERSION_MISMATCH")
		require.Equal(t, 1, called)
	})
	t.Run("stale bound hash", func(t *testing.T) {
		bad := in
		bad.ExpectedRuleSetHash = "wrong"
		require.Error(t, f.runtime.GuardDOCX(f.ctx, f.actor, bad, writer))
		require.Equal(t, 1, called)
	})
	t.Run("writer rollback", func(t *testing.T) {
		err := f.runtime.GuardDOCX(f.ctx, f.actor, in, func(tx *gorm.DB, _ DOCXGuardProof) error {
			require.NoError(t, tx.Exec("INSERT INTO docx_guard_writes(proof) VALUES('rolled-back')").Error)
			return errors.New("injected write failure")
		})
		require.Error(t, err)
		var n int64
		require.NoError(t, f.db.Table("docx_guard_writes").Count(&n).Error)
		require.EqualValues(t, 1, n)
	})
	t.Run("persisted empty body fails actual rules", func(t *testing.T) {
		require.NoError(t, f.db.Exec("UPDATE lingdoc_chapter_versions SET body_markdown='' WHERE id=?", row.ID).Error)
		defer f.db.Exec("UPDATE lingdoc_chapter_versions SET body_markdown=? WHERE id=?", row.BodyMarkdown, row.ID)
		bad := in
		bad.Chapters = append([]DOCXGuardChapter{}, in.Chapters...)
		bad.Chapters[0].Body = ""
		require.ErrorContains(t, f.runtime.GuardDOCX(f.ctx, f.actor, bad, writer), "DOCX_RULE_BLOCKED")
		require.Equal(t, 1, called)
	})
	t.Run("live membership removal blocks writer", func(t *testing.T) {
		require.NoError(t, f.db.Exec("UPDATE tenant_members SET status='removed' WHERE user_id=?", f.actor.UserID).Error)
		defer f.db.Exec("UPDATE tenant_members SET status='active' WHERE user_id=?", f.actor.UserID)
		require.Error(t, f.runtime.GuardDOCX(f.ctx, f.actor, in, writer))
		require.Equal(t, 1, called)
	})
	t.Run("actual confirmation and idempotent retry", func(t *testing.T) {
		guarded := in
		guarded.Scene = "write_check"
		input := adoption.ConfirmChapterInput{ProjectID: in.ProjectID, ChapterID: "chapter-2", ActorID: f.actor.UserID, IdempotencyKey: "docx-confirm-real-service", ExpectedChapterVersionID: row.ID, ExpectedSpecRevision: f.input.ExpectedSpecRevision}
		first, replayed, err := f.runtime.ConfirmDOCXChapter(f.ctx, f.actor, guarded, input)
		require.NoError(t, err)
		require.False(t, replayed)
		require.NotEmpty(t, first.ID)
		second, replayed, err := f.runtime.ConfirmDOCXChapter(f.ctx, f.actor, guarded, input)
		require.NoError(t, err)
		require.True(t, replayed)
		require.Equal(t, first.ID, second.ID)
		var n int64
		require.NoError(t, f.db.Table("lingdoc_chapter_confirmations").Where("id=?", first.ID).Count(&n).Error)
		require.EqualValues(t, 1, n)
		t.Run("replays after version advances but still reauthorizes", func(t *testing.T) {
			require.NoError(t, f.db.Exec("UPDATE lingdoc_projects SET spec_revision=spec_revision+1 WHERE id='project-1'").Error)
			defer f.db.Exec("UPDATE lingdoc_projects SET spec_revision=spec_revision-1 WHERE id='project-1'")
			third, replayed, err := f.runtime.ConfirmDOCXChapter(f.ctx, f.actor, guarded, input)
			require.NoError(t, err)
			require.True(t, replayed)
			require.Equal(t, first.ID, third.ID)
			require.NoError(t, f.db.Exec("UPDATE tenant_members SET status='removed' WHERE user_id=?", f.actor.UserID).Error)
			defer f.db.Exec("UPDATE tenant_members SET status='active' WHERE user_id=?", f.actor.UserID)
			_, _, err = f.runtime.ConfirmDOCXChapter(f.ctx, f.actor, guarded, input)
			require.Error(t, err)
		})
	})
	t.Run("revoked source", func(t *testing.T) {
		require.NoError(t, f.db.Exec("UPDATE chunks SET content_revision=1 WHERE id='source-1'").Error)
		require.Error(t, f.runtime.GuardDOCX(f.ctx, f.actor, in, writer))
		require.Equal(t, 1, called)
	})
	t.Run("identity mismatch", func(t *testing.T) {
		ctx := types.WithCaller(context.Background(), types.Caller{TenantID: 7, UserID: "outsider", Role: types.TenantRoleOwner})
		require.ErrorContains(t, f.runtime.GuardDOCX(ctx, f.actor, in, writer), "IDENTITY_MISMATCH")
	})
}
