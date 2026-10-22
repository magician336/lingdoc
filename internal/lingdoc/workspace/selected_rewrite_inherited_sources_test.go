package workspace

import (
	"context"
	"path/filepath"
	"testing"
	"unicode/utf16"

	"github.com/Tencent/WeKnora/internal/evidence"
	"github.com/Tencent/WeKnora/internal/lingdoc/candidateadoption"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type inheritedRewriteWorkspace struct {
	ApplicationService
	copy WorkingCopy
}

func (s *inheritedRewriteWorkspace) Authorize(context.Context, Actor, string, string) error {
	return nil
}
func (s *inheritedRewriteWorkspace) GetProject(context.Context, Actor, string) (Project, error) {
	return Project{ID: "project", Status: "active", SpecRevision: 1}, nil
}
func (s *inheritedRewriteWorkspace) GetWorkingCopy(context.Context, Actor, string, string) (WorkingCopy, error) {
	return s.copy, nil
}

type inheritedRewriteSources struct {
	SourceApplicationService
	denied bool
	calls  []string
}

func (s *inheritedRewriteSources) GetSource(_ context.Context, _ Actor, projectID, id string) (evidence.Source, error) {
	s.calls = append(s.calls, id)
	if s.denied || id != "existing" {
		return evidence.Source{}, ErrRewriteSourceDenied
	}
	return evidence.Source{ID: id, ProjectID: projectID, Status: evidence.SourceAvailable, AssetID: "asset", AssetRevision: 1, Locator: "page/1", QuotedTextHash: "hash", QuotedText: "supported fact"}, nil
}

type inheritedRewriteModel struct {
	calls    int
	prompt   SelectedRewritePrompt
	invented bool
}

func (*inheritedRewriteModel) RunMode() string { return "mock" }
func (m *inheritedRewriteModel) RewriteSelected(_ context.Context, _ Actor, p SelectedRewritePrompt) (SelectedRewriteOutput, error) {
	m.calls++
	m.prompt = p
	id := "existing"
	if m.invented {
		id = "invented"
	}
	return SelectedRewriteOutput{ReplacementMarkdown: "revised [[source:" + id + "]]", SourceIDs: []string{id}, RunMode: "mock"}, nil
}
func inheritedRewriteFixture(t *testing.T) (*SelectedRewriteService, *inheritedRewriteWorkspace, *inheritedRewriteSources, *inheritedRewriteModel, SelectedRewriteRequest) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "rewrite.db")), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	selected := "original [[source:existing]]"
	workspace := &inheritedRewriteWorkspace{copy: WorkingCopy{ProjectID: "project", ChapterID: "chapter", SpecRevision: 1, WorkingCopyRevision: 1, BodyMarkdown: selected + " context [[source:outside]]", SourceIDs: []string{"existing", "outside"}}}
	sources := &inheritedRewriteSources{}
	model := &inheritedRewriteModel{}
	service := NewSelectedRewriteService(db, workspace, sources, model)
	require.NoError(t, service.store.AutoMigrate(context.Background()))
	input := SelectedRewriteRequest{ExpectedSpecRevision: 1, ExpectedWorkingCopyRevision: 1, Selection: RewriteSelection{EndUTF16: len(utf16.Encode([]rune(selected))), SelectedText: selected}, Instruction: "polish", SourceIDs: []string{}}
	return service, workspace, sources, model, input
}
func TestSelectedRewriteInheritsOnlyVerifiedSelectionSourcesAndReplays(t *testing.T) {
	s, _, sources, model, input := inheritedRewriteFixture(t)
	actor := Actor{TenantID: 1, UserID: "owner"}
	candidate, err := s.Create(context.Background(), actor, "project", "chapter", "inherit-key", input)
	require.NoError(t, err)
	require.Equal(t, candidateadoption.ValidityFresh, candidate.Validity)
	require.Equal(t, []string{"existing"}, candidate.SourceIDs)
	require.Len(t, candidate.AuthorizedSources, 1)
	require.Len(t, model.prompt.Sources, 1)
	require.Equal(t, "existing", model.prompt.Sources[0].ID)
	require.NotContains(t, sources.calls, "outside")
	input.SourceIDs = []string{"existing"}
	replay, err := s.Create(context.Background(), actor, "project", "chapter", "inherit-key", input)
	require.NoError(t, err)
	require.Equal(t, candidate.CandidateID, replay.CandidateID)
	require.Equal(t, 1, model.calls)
	sources.denied = true
	_, err = s.Create(context.Background(), actor, "project", "chapter", "inherit-key", input)
	require.ErrorIs(t, err, ErrRewriteSourceDenied)
	require.Equal(t, 1, model.calls)
}
func TestSelectedRewriteInheritedSourceDeniedBeforeModel(t *testing.T) {
	for _, scenario := range []string{"revoked", "forged_selection", "undeclared_original", "forged_explicit_source"} {
		t.Run(scenario, func(t *testing.T) {
			s, workspace, sources, model, input := inheritedRewriteFixture(t)
			switch scenario {
			case "revoked":
				sources.denied = true
			case "forged_selection":
				input.Selection.SelectedText = "forged [[source:existing]]"
			case "undeclared_original":
				workspace.copy.SourceIDs = []string{"outside"}
			case "forged_explicit_source":
				input.SourceIDs = []string{"invented"}
			}
			_, err := s.Create(context.Background(), Actor{TenantID: 1, UserID: "owner"}, "project", "chapter", "denied-key", input)
			require.Error(t, err)
			require.Zero(t, model.calls)
			var rows int64
			require.NoError(t, s.store.db.Model(&selectedRewriteRow{}).Count(&rows).Error)
			require.Zero(t, rows)
		})
	}
}
func TestSelectedRewriteInheritanceDoesNotAuthorizeModelInventedSources(t *testing.T) {
	s, _, sources, model, input := inheritedRewriteFixture(t)
	model.invented = true
	candidate, err := s.Create(context.Background(), Actor{TenantID: 1, UserID: "owner"}, "project", "chapter", "invented-key", input)
	require.NoError(t, err)
	require.Empty(t, candidate.Validity)
	require.Equal(t, "source_access_denied", candidate.ErrorCode)
	require.Empty(t, candidate.ReplacementMarkdown)
	require.NotContains(t, sources.calls, "invented")
}
