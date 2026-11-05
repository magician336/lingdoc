package workspace

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"testing"

	"github.com/Tencent/WeKnora/internal/lingdoc/delivery"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func readDOCXFixture(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile("../../../../kimi-web/public/templates/nsfc2026-application.docx")
	require.NoError(t, err)
	return data
}

func replaceDOCXDocument(t *testing.T, data []byte, replace func(string) string) []byte {
	t.Helper()
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	require.NoError(t, err)
	var out bytes.Buffer
	writer := zip.NewWriter(&out)
	for _, file := range reader.File {
		stream, err := file.Open()
		require.NoError(t, err)
		content, err := io.ReadAll(stream)
		require.NoError(t, err)
		require.NoError(t, stream.Close())
		if file.Name == "word/document.xml" {
			content = []byte(replace(string(content)))
		}
		header := file.FileHeader
		header.Method = zip.Deflate
		target, err := writer.CreateHeader(&header)
		require.NoError(t, err)
		_, err = target.Write(content)
		require.NoError(t, err)
	}
	require.NoError(t, writer.Close())
	return out.Bytes()
}

func TestDOCXProjectionFromAuthoritativeBytes(t *testing.T) {
	data := readDOCXFixture(t)
	projection, err := parseDOCXProjection(data)
	require.NoError(t, err)
	require.Len(t, projection.Sections, 6)
	require.Equal(t, "topic-summary", projection.Sections[0].SectionID)
	require.Equal(t, "references", projection.Sections[5].SectionID)
	for _, section := range projection.Sections {
		require.Empty(t, section.Paragraphs[0])
		require.NotEmpty(t, section.SectionDeliveryHash)
	}
	changed := replaceDOCXDocument(t, data, func(document string) string {
		return bytes.NewBufferString(document).String()
	})
	// Insert after the first guidance run without changing any other section.
	changed = replaceDOCXDocument(t, data, func(document string) string {
		marker := "字以内）。"
		index := bytes.Index([]byte(document), []byte(marker))
		require.Greater(t, index, 0)
		insertAt := index + len(marker)
		return document[:insertAt] + "合成课题正文" + document[insertAt:]
	})
	next, err := parseDOCXProjection(changed)
	require.NoError(t, err)
	require.Contains(t, next.Sections[0].Body, "合成课题正文")
	require.Equal(t, projection.ManifestHash, next.ManifestHash)
	require.NotEqual(t, projection.Sections[0].SectionDeliveryHash, next.Sections[0].SectionDeliveryHash)
	for i := 1; i < 6; i++ {
		require.Equal(t, projection.Sections[i].SectionDeliveryHash, next.Sections[i].SectionDeliveryHash)
	}
	broken := replaceDOCXDocument(t, data, func(document string) string {
		return string(bytes.Replace([]byte(document), []byte("参考文献"), []byte("已删除"), 1))
	})
	_, err = parseDOCXProjection(broken)
	require.ErrorContains(t, err, "anchor_missing")
}

func seedDOCXChapterMappings(t *testing.T, f candidateOneConnFixture) {
	t.Helper()
	adopted, err := f.adoption.AcceptCandidate(f.ctx, f.input)
	require.NoError(t, err)
	require.NoError(t, f.db.Exec("UPDATE lingdoc_chapters SET section_id='topic-summary' WHERE id='chapter-2'").Error)
	for i, spec := range docxSectionSpecs[1:] {
		chapterID := fmt.Sprintf("chapter-docx-%d", i+2)
		versionID := "version-" + chapterID
		require.NoError(t, f.db.Exec("INSERT INTO lingdoc_chapters (id,project_id,section_id,title,current_version_id) SELECT ?,project_id,?,title,? FROM lingdoc_chapters WHERE id='chapter-2'", chapterID, spec.ID, versionID).Error)
		require.NoError(t, f.db.Exec(`INSERT INTO lingdoc_chapter_versions (id,project_id,chapter_id,parent_version_id,candidate_id,body_markdown,source_ids_json,citation_usages_json,review_items_json,spec_revision,confirmation_valid,created_by,created_at) SELECT ?,project_id,?,parent_version_id,candidate_id,body_markdown,source_ids_json,citation_usages_json,review_items_json,spec_revision,confirmation_valid,created_by,created_at FROM lingdoc_chapter_versions WHERE id=?`, versionID, chapterID, adopted.Chapter.CurrentVersionID).Error)
	}
}

func TestDOCXVersionSavePersistsImmutableMappingAndLedger(t *testing.T) {
	f := newCandidateOneConnFixture(t)
	seedDOCXChapterMappings(t, f)
	store := NewDOCXVersionStore(f.db, func(*gorm.DB, string) error { return nil })
	require.NoError(t, store.AutoMigrate(f.ctx))
	data := replaceDOCXDocument(t, readDOCXFixture(t), func(document string) string {
		marker := "字以内）。"
		index := bytes.Index([]byte(document), []byte(marker))
		require.Greater(t, index, 0)
		insertAt := index + len(marker)
		return document[:insertAt] + "持久化合成正文" + document[insertAt:]
	})
	saved, replayed, err := f.runtime.SaveDOCXVersion(f.ctx, f.actor, "project-1", "save-docx-v1", 0, data)
	require.NoError(t, err)
	require.False(t, replayed)
	require.EqualValues(t, 1, saved.DiskVersion)
	require.Equal(t, docxBytesHash(data), saved.DocSHA256)
	chapters, err := store.GuardChapters(f.ctx, saved.ID, []string{"topic-summary"})
	require.NoError(t, err)
	require.Len(t, chapters, 1)
	require.Contains(t, chapters[0].Body, "持久化合成正文")
	var sources string
	require.NoError(t, f.db.Table("lingdoc_chapter_versions").Select("source_ids_json").Where("id=?", chapters[0].ChapterVersionID).Scan(&sources).Error)
	require.Equal(t, `[]`, sources, "sources without a marker in the saved DOCX must not survive")
	replay, replayed, err := f.runtime.SaveDOCXVersion(f.ctx, f.actor, "project-1", "save-docx-v1", 0, data)
	require.NoError(t, err)
	require.True(t, replayed)
	require.Equal(t, saved.ID, replay.ID)
	_, _, err = f.runtime.SaveDOCXVersion(f.ctx, f.actor, "project-1", "save-docx-v1", 0, readDOCXFixture(t))
	require.ErrorContains(t, err, "IDEMPOTENCY_CONFLICT")
	_, _, err = f.runtime.SaveDOCXVersion(f.ctx, f.actor, "project-1", "save-docx-stale", 0, data)
	require.ErrorContains(t, err, "VERSION_CHANGED")
	require.NoError(t, f.db.Exec("UPDATE tenant_members SET status='removed' WHERE user_id=?", f.actor.UserID).Error)
	_, _, err = f.runtime.SaveDOCXVersion(f.ctx, f.actor, "project-1", "save-docx-v1", 0, data)
	require.Error(t, err)
	require.NoError(t, f.db.Exec("UPDATE tenant_members SET status='active' WHERE user_id=?", f.actor.UserID).Error)
	loaded, err := store.Get(f.ctx, "project-1", saved.ID)
	require.NoError(t, err)
	require.Equal(t, data, loaded.Bytes)
}

func TestDOCXFrozenRendererUsesSpecifiedImmutableVersion(t *testing.T) {
	f := newCandidateOneConnFixture(t)
	store := NewDOCXVersionStore(f.db, func(*gorm.DB, string) error { return nil })
	require.NoError(t, store.AutoMigrate(f.ctx))
	data := readDOCXFixture(t)
	projection, err := parseDOCXProjection(data)
	require.NoError(t, err)
	raw, err := json.Marshal(projection)
	require.NoError(t, err)
	row := docxSavedVersionRow{ID: "saved-version-1", ProjectID: "project-1", DiskVersion: 1, DocSHA256: docxBytesHash(data), ManifestHash: projection.ManifestHash, AdapterVersion: projection.AdapterVersion, AlgorithmVersion: projection.AlgorithmVersion, DocBytes: data, ProjectionJSON: string(raw), ActorID: "actor", IdempotencyKey: "immutable-export", RequestHash: "request"}
	require.NoError(t, f.db.Create(&row).Error)
	renderer := DOCXFrozenRenderer{Store: store}
	input := delivery.DeliveryInput{ProjectID: "project-1", DOCXSavedVersionID: row.ID, DOCXSHA256: row.DocSHA256}
	actual, err := renderer.RenderFrozen(input)
	require.NoError(t, err)
	require.Equal(t, data, actual)
	require.NoError(t, (DOCXFrozenValidator{}).ValidateFrozen(input, actual))
	require.NoError(t, f.db.Model(&docxSavedVersionRow{}).Where("id=?", row.ID).Update("doc_bytes", []byte("tampered")).Error)
	_, err = renderer.RenderFrozen(input)
	require.ErrorContains(t, err, "INTEGRITY")
}
