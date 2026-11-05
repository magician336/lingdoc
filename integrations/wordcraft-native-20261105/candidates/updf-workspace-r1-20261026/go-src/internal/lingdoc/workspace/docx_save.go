package workspace

import (
	"context"

	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
	"gorm.io/gorm"
)

// SaveDOCXVersion authorizes every mapped chapter inside the same transaction
// as the immutable DOCX/version/projection write. No browser-supplied body,
// manifest or rule proof crosses this application boundary. Optional evidence
// refs are claims checked against current server-owned card/source records.
func (r *SourceRuntime) SaveDOCXVersion(ctx context.Context, actor Actor, projectID, key string, expectedDiskVersion int64, data []byte, citations ...[]DOCXCitationRef) (DOCXSavedVersion, bool, error) {
	var runtime *SourceRuntime
	var projects *core.Service
	store := NewDOCXVersionStore(r.db, func(tx *gorm.DB, chapterID string) error {
		if runtime == nil {
			runtime = r.CloneWithDatabase(tx)
			projects = core.NewServiceWithSources(core.NewGORMRepository(tx), r.templates, runtime.WorkspaceSourcePolicy())
			runtime.ConnectProjects(projects)
			runtime.EnableFullWorkflowPolicy()
			projects.ConfigureG2(core.G2Runtime{Sources: NewG2ResearchAdapter(tx, projects, runtime, r.kbShares, nil, nil)})
		}
		return projects.Authorize(ctx, actor, projectID, "write:"+chapterID)
	})
	store.validateCitations = func(tx *gorm.DB, chapterID string, ids []string, refs []DOCXCitationRef) error {
		return validateDOCXCitations(ctx, tx, runtime, projects, actor, projectID, chapterID, ids, refs)
	}
	return store.Save(ctx, projectID, actor.UserID, key, expectedDiskVersion, data, citations...)
}
