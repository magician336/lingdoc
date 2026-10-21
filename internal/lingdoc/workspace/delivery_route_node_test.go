package workspace

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/lingdoc/candidateadoption"
	"github.com/Tencent/WeKnora/internal/lingdoc/delivery"
	lingdoctemplate "github.com/Tencent/WeKnora/internal/lingdoc/template"
	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
	"gorm.io/gorm"
)

// #324：交付检查在带必需路线节点的项目上返回 403 source_access_denied。
//
// 同一个字段在这个项目上有**两份值**，各司其职：
//   - 原始规格里存的是激活闸门逼出来的占位值。溯源快照记的就是它，所以溯源校验
//     必须拿原始规格去比——拿投影那份去比，值对不上，来源访问就被拒了。
//   - 路线感知投影里存的是「路线图还没给出这个节点」的空值。求值必须拿投影那份去
//     读，否则占位值会顶替缺失的路线节点，#319 的语义就退了。
//
// 所以这条用例同时钉两件事：检查返回 200（不是 403），而路线节点缺失仍然是阻断级
// 问题。
func TestDeliveryCheckOnARequiredRouteNodeProjectReadsTheRuleIssues(t *testing.T) {
	handler, db, assetID := seedBoundSource(t)
	seedDeliveryWorkspace(t, db,
		deliveryQuestionChapter(assetID, currentAssetRevision(t, handler, assetID), deliveryBody, []string{deliverySourceID}),
		deliveryMethodChapter(),
	)
	seedRequiredRouteNodeTemplateCopy(t, db)

	router := deliveryRoutes(NewDeliveryHandler(newDeliveryReleaseService(t, handler)))
	checked := deliveryServe(router, deliveryRequest(http.MethodPost, deliveryRouteBase+"/checks", deliveryReadVersionBody, ""))
	if checked.Code != http.StatusOK {
		t.Fatalf("POST checks = %d, want 200: %s", checked.Code, checked.Body.String())
	}
	var result delivery.CheckResult
	if err := json.Unmarshal(decodeDeliveryEnvelope(t, checked).Data, &result); err != nil {
		t.Fatalf("decode check result: %v", err)
	}

	// 求值仍走路线感知投影：已声明但缺失的路线节点必须是阻断级问题。
	var route *delivery.CheckIssue
	for index := range result.Issues {
		if result.Issues[index].RuleID == lingdoctemplate.RuleRouteNodeRequired {
			route = &result.Issues[index]
			break
		}
	}
	if route == nil {
		t.Fatalf("check issues = %+v, want a blocking route node issue", result.Issues)
	}
	if route.Severity != delivery.SeverityBlocking {
		t.Fatalf("route node issue = %+v, want a blocking issue", route)
	}
	if route.TargetID != lingdoctemplate.DemoRequiredRouteNodeID {
		t.Fatalf("route node issue = %+v, want the target %s", route, lingdoctemplate.DemoRequiredRouteNodeID)
	}
}

// seedRequiredRouteNodeTemplateCopy 把项目换成演示模板 v2（声明 route-r7 为必需
// 路线节点），并在规格里放上激活闸门逼出来的占位值。
//
// 三件事必须同时成立，缺一件就测不到 #324：
//   - 模板副本声明 route-r7 为必需：路线感知投影才会把这个 id 读成「空」；
//   - 规格里带占位值：溯源快照记下的就是占位值那份；
//   - 路线图里没有这个节点：投影读到的确实是空，而不是节点名。
func seedRequiredRouteNodeTemplateCopy(t *testing.T, db *gorm.DB) {
	t.Helper()
	// 路线感知的投影只在路线图那几张表存在时才算（candidateadoption/delivery_input.go）。
	ensureImpactRouteTables(t, db)
	definition, err := lingdoctemplate.WithHashes(lingdoctemplate.DemoRouteTemplate())
	if err != nil {
		t.Fatalf("stamp the route template copy: %v", err)
	}
	raw, err := json.Marshal(definition)
	if err != nil {
		t.Fatalf("encode the route template copy: %v", err)
	}
	if err := db.Exec(`CREATE TABLE IF NOT EXISTS lingdoc_project_template_copies(
		id TEXT PRIMARY KEY,project_id TEXT NOT NULL,source_template_id TEXT NOT NULL,
		source_template_version TEXT NOT NULL,version INTEGER NOT NULL,status TEXT NOT NULL,
		content_hash TEXT NOT NULL,ruleset_hash TEXT NOT NULL,definition_json TEXT NOT NULL,
		created_by TEXT NOT NULL,created_at DATETIME NOT NULL,UNIQUE(project_id,version))`).Error; err != nil {
		t.Fatalf("create the template copy table: %v", err)
	}
	if err := db.Exec(`INSERT INTO lingdoc_project_template_copies(
		id,project_id,source_template_id,source_template_version,version,status,
		content_hash,ruleset_hash,definition_json,created_by,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
		"route-copy-project-1", "project-1", definition.ID, definition.Version, 1, core.TemplateCopyBound,
		definition.ContentHash, definition.RulesetHash, string(raw), deliveryActorID, time.Now().UTC()).Error; err != nil {
		t.Fatalf("seed the route template copy: %v", err)
	}
	// 项目指向这份副本；模板版本随副本走，副本才是模板真相的所在。
	if err := db.Exec("UPDATE lingdoc_projects SET template_copy_version = 1, template_id = ?, template_version = ? WHERE id = ?",
		definition.ID, definition.Version, "project-1").Error; err != nil {
		t.Fatalf("point the project at the route template copy: %v", err)
	}
	// 规格里带上激活闸门逼出来的占位值：它就是溯源快照要记的那份。
	spec := map[string]string{
		"research_subject":                      "公开的合成调查样本",
		"research_goal":                         "验证资料到章节的工作流程",
		lingdoctemplate.DemoRequiredRouteNodeID: "合成路线占位值（真正的值由路线图节点提供）",
	}
	rawSpec, err := json.Marshal(spec)
	if err != nil {
		t.Fatalf("encode the project spec: %v", err)
	}
	if err := db.Exec("UPDATE lingdoc_projects SET spec_json = ? WHERE id = ?", string(rawSpec), "project-1").Error; err != nil {
		t.Fatalf("write the route node placeholder into the spec: %v", err)
	}
	rebindConfirmationTemplateVersion(t, db, definition.Version)
}

// rebindConfirmationTemplateVersion 让确认记录跟上模板版本：确认钉的是模板版本，
// 模板换了版本而确认没换，冻结时读到的就只是「确认过期」，盖住本用例要看的结论。
func rebindConfirmationTemplateVersion(t *testing.T, db *gorm.DB, version string) {
	t.Helper()
	var rows []struct {
		ID          string
		DetailsJSON string
	}
	if err := db.Table("lingdoc_chapter_confirmations").Select("id, details_json").Find(&rows).Error; err != nil {
		t.Fatalf("read the chapter confirmations: %v", err)
	}
	for _, row := range rows {
		var confirmation candidateadoption.Confirmation
		if err := json.Unmarshal([]byte(row.DetailsJSON), &confirmation); err != nil {
			t.Fatalf("decode confirmation %s: %v", row.ID, err)
		}
		confirmation.TemplateVersion = version
		raw, err := json.Marshal(confirmation)
		if err != nil {
			t.Fatalf("encode confirmation %s: %v", row.ID, err)
		}
		if err := db.Exec("UPDATE lingdoc_chapter_confirmations SET details_json = ? WHERE id = ?", string(raw), row.ID).Error; err != nil {
			t.Fatalf("rebind confirmation %s: %v", row.ID, err)
		}
	}
}
