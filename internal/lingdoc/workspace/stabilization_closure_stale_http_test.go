package workspace

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// AC-G3-12 (#255): every task page says "done", but one of the target objects
// was later replaced by a new version. The stabilization aggregator re-reads
// the conditions and finds the version mismatch.
//
// The required outcome is a DOMAIN fact, not a request error: the revision
// stays BLOCKED with a `closure_stale` blocker that names the object and the
// versions, and the obligation is re-raised against the current version.
//
// What must NOT change: a request that points at a revision which is no
// longer the project's current revision is still a request error and still
// answers 409 version_conflict. The two situations used to share one
// `ErrVersionConflict`, which made the first one unreachable.

const closureStalePath = "/api/projects/%s/change-sets/%s/stabilize"

// closureStaleEngine registers the real impact routes over the delivery guard
// composition, so the verdict is observed over HTTP instead of through a
// domain call.
func closureStaleEngine(t *testing.T, f deliveryGuardFixture) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	RegisterImpactRoutes(engine.Group("/api"), f.projects)
	return engine
}

// stabilizeOverHTTP asks the server for the stabilization verdict.
func stabilizeOverHTTP(t *testing.T, f deliveryGuardFixture, engine *gin.Engine, changeID, key string) *httptest.ResponseRecorder {
	t.Helper()
	path := fmt.Sprintf(closureStalePath, f.project.ID, changeID)
	request := httptest.NewRequest(http.MethodPost, path, strings.NewReader("{}"))
	request = request.WithContext(f.requestContext(f.actor.UserID, f.actor.Role, "closure-stale-"+key))
	request.Header.Set("Idempotency-Key", key)
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	return response
}

// impactTasksOverHTTP reads the visible impact tasks through the published
// route, so a test sees what a client sees.
func impactTasksOverHTTP(t *testing.T, f deliveryGuardFixture, engine *gin.Engine) []core.ImpactTask {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, "/api/projects/"+f.project.ID+"/impact-tasks", nil)
	request = request.WithContext(f.requestContext(f.actor.UserID, f.actor.Role, "closure-stale-tasks"))
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	require.Equal(t, http.StatusOK, response.Code, "body=%s", response.Body.String())
	var payload struct {
		Data []core.ImpactTask `json:"data"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &payload))
	return payload.Data
}

// replaceChapterVersion writes a new version of one chapter, which is what
// replaces the version a closure evidence names.
func replaceChapterVersion(t *testing.T, f deliveryGuardFixture, chapterID string) (string, string) {
	t.Helper()
	chapters, err := f.projects.ListChapters(f.ctx, f.actor, f.project.ID)
	require.NoError(t, err)
	p, err := f.projects.GetProject(f.ctx, f.actor, f.project.ID)
	require.NoError(t, err)
	for _, chapter := range chapters {
		if chapter.ID != chapterID || chapter.CurrentVersionID == nil {
			continue
		}
		previous := *chapter.CurrentVersionID
		raw, _, _, err := f.projects.SaveChapter(f.ctx, f.actor, f.project.ID, chapterID, "closure-stale-replace-"+previous, core.SaveChapterInput{
			ExpectedChapterVersionID: chapter.CurrentVersionID, ExpectedSpecRevision: p.SpecRevision,
			BodyMarkdown: "稳定化 合成：目标对象被新版本替换。", SourceIDs: []string{},
		})
		require.NoError(t, err, "replacing chapter %s", chapterID)
		var saved core.Chapter
		require.NoError(t, json.Unmarshal(raw, &saved))
		require.NotNil(t, saved.CurrentVersionID)
		require.NotEqual(t, previous, *saved.CurrentVersionID, "the chapter version was not replaced")
		return previous, *saved.CurrentVersionID
	}
	t.Fatalf("chapter %s has no current version", chapterID)
	return "", ""
}

// The replaced closure is a domain fact: the revision stays BLOCKED, the
// blocker names the object and the versions, and the obligation is re-raised
// against the CURRENT version instead of being lost.
func TestStabilizeReportsClosureStaleAndRepostsTaskWhenTargetVersionReplaced(t *testing.T) {
	f := newDeliveryGuardFixture(t, false)
	engine := closureStaleEngine(t, f)

	change := applyChangeForGate(t, f, "closure-stale")
	// The Owner discharges every obligation, so each closure evidence names the
	// chapter version that is current right now.
	repairGateObligations(t, f, change.ID)

	closed := tasksForChangeSet(t, f, change.ID)
	require.Equal(t, "DONE", closed.Status, "fixture precondition: the obligation is closed")
	require.NotEmpty(t, closed.ClosureEvidence)
	evidence := closed.ClosureEvidence[0]

	// A later edit replaces the very version the closure evidence names.
	replacedVersion, currentVersion := replaceChapterVersion(t, f, evidence.ID)
	require.Equal(t, evidence.Version, replacedVersion, "fixture precondition: the closure names the replaced version")

	response := stabilizeOverHTTP(t, f, engine, change.ID, "closure-stale-stabilize")
	require.Equal(t, http.StatusOK, response.Code, "a replaced closure is a domain fact, not a request error: body=%s", response.Body.String())
	var payload struct {
		Data core.StabilizationResult `json:"data"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &payload))
	result := payload.Data
	require.Equal(t, "BLOCKED", result.Status, "a replaced closure must not stabilize: %+v", result.Blockers)

	var stale *core.ImpactBlocker
	for i := range result.Blockers {
		if result.Blockers[i].Code == "closure_stale" {
			stale = &result.Blockers[i]
		}
	}
	require.NotNil(t, stale, "the verdict must name the stale closure: %+v", result.Blockers)
	require.Contains(t, stale.Reason, evidence.ID, "the blocker must name the object: %+v", stale)
	require.Contains(t, stale.Reason, replacedVersion, "the blocker must name the replaced version: %+v", stale)
	require.Contains(t, stale.Reason, currentVersion, "the blocker must name the current version: %+v", stale)

	// The obligation is re-raised against the current version of the same
	// object. The task identity is (change set, target object, revision,
	// action) - the version is not part of it - so the re-raised obligation is
	// the SAME task, live again, and the old closure evidence is not carried
	// over.
	tasks := impactTasksOverHTTP(t, f, engine)
	var repost *core.ImpactTask
	for i := range tasks {
		if tasks[i].ChangeSetID == change.ID && tasks[i].Target.Kind == evidence.Kind && tasks[i].Target.ID == evidence.ID {
			if repost != nil {
				require.Fail(t, "the re-raised obligation must not be a second task", "%+v", tasks)
			}
			repost = &tasks[i]
		}
	}
	require.NotNil(t, repost, "the replaced obligation must be re-raised: %+v", tasks)
	require.Equal(t, closed.ID, repost.ID, "the re-raised obligation is the same task: %+v", repost)
	require.Equal(t, "OPEN", repost.Status, "the re-raised obligation is live again: %+v", repost)
	require.Equal(t, currentVersion, repost.Target.Version, "the re-raised task must target the current version: %+v", repost)
	require.Empty(t, repost.ClosureEvidence, "the old closure evidence must not be carried over: %+v", repost)
	require.Empty(t, repost.HumanDecision, "the old human decision must not be carried over: %+v", repost)
}

// Repeating the stabilization must not raise a second task for the same
// object at the same revision: the re-raised obligation is identified by
// (change set, target object, revision, action), and a repeated call must
// find the very same one.
func TestStabilizeDoesNotRepostTheSameTaskTwice(t *testing.T) {
	f := newDeliveryGuardFixture(t, false)
	engine := closureStaleEngine(t, f)

	change := applyChangeForGate(t, f, "closure-stale-repeat")
	repairGateObligations(t, f, change.ID)
	closed := tasksForChangeSet(t, f, change.ID)
	replaceChapterVersion(t, f, closed.ClosureEvidence[0].ID)

	first := stabilizeOverHTTP(t, f, engine, change.ID, "closure-stale-repeat-1")
	require.Equal(t, http.StatusOK, first.Code, "body=%s", first.Body.String())
	second := stabilizeOverHTTP(t, f, engine, change.ID, "closure-stale-repeat-2")
	require.Equal(t, http.StatusOK, second.Code, "body=%s", second.Body.String())

	raised := 0
	for _, task := range impactTasksOverHTTP(t, f, engine) {
		if task.ChangeSetID == change.ID && task.Target.ID == closed.Target.ID {
			raised++
		}
	}
	require.Equal(t, 1, raised, "a repeated stabilization must not raise a second task")
}

// The other half of the split: a request that points at a revision which is
// no longer the project's current revision is a request error and keeps
// answering 409 version_conflict. It must NOT be reported as closure_stale.
func TestStabilizeStillRejectsAChangeSetFromAnotherRevision(t *testing.T) {
	f := newDeliveryGuardFixture(t, false)
	engine := closureStaleEngine(t, f)

	first := applyChangeWithSubject(t, f, "closure-stale-rev", "稳定化 合成 第一次修改")
	// A second applied ChangeSet advances the context revision, so the first
	// ChangeSet now points at a revision that is no longer current.
	applyChangeWithSubject(t, f, "closure-stale-rev-next", "稳定化 合成 第二次修改")

	response := stabilizeOverHTTP(t, f, engine, first.ID, "closure-stale-rev-stabilize")
	require.Equal(t, http.StatusConflict, response.Code, "a stale target revision is still a request error: body=%s", response.Body.String())
	require.Contains(t, response.Body.String(), "version_conflict")
	require.NotContains(t, response.Body.String(), "closure_stale")
}

// applyChangeWithSubject applies a real ChangeSet that really changes the
// research subject, so a second one can follow the first.
func applyChangeWithSubject(t *testing.T, f deliveryGuardFixture, key, subject string) core.ChangeSet {
	t.Helper()
	p, err := f.projects.GetProject(f.ctx, f.actor, f.project.ID)
	require.NoError(t, err)
	raw, _, _, err := f.projects.CreateChangeSet(f.ctx, f.actor, p.ID, key+"-create", core.CreateChangeSetInput{
		ExpectedContextRevision: p.CurrentContextRevision,
		Fields:                  map[string]core.ChangeFieldInput{"research_subject": {OldValue: p.Spec["research_subject"], NewValue: subject}},
		AffectedChapterIDs:      []string{f.chapters[0].ID},
		Reason:                  "稳定化 合成正式变更",
	})
	require.NoError(t, err)
	var change core.ChangeSet
	require.NoError(t, json.Unmarshal(raw, &change))
	_, _, _, err = f.projects.ApplyChangeSet(f.ctx, f.actor, p.ID, change.ID, key+"-apply")
	require.NoError(t, err)
	return change
}
