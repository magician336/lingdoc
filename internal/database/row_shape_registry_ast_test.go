package database

// The AST side of the row-shape guard (Spec #707, ticket #709): the shape
// checklist may not be circumvented by simply not registering a row
// structure. Every type in the LingDoc domain's non-test files that declares
// a `TableName() string` method returning a constant string literal must
// appear in guardedRowShapes (or in the explicit exclusions), and every
// checklist entry must correspond to a type that really exists — a dangling
// entry is red too, so the checklist cannot silently outlive the types it
// was written for.
//
// The scan is syntax only (go/parser, like the operationcore single-owner
// test): it walks internal/lingdoc, finds TableName methods, and reads the
// returned string literal. Two deliberate strictness choices:
//
//   - the return must be a string literal. A constant identifier or a
//     computed string cannot be pinned by a syntax scan, so it is a scan
//     problem (red) rather than a silent hole; every current row structure
//     returns a literal;
//   - the scan failing to find anything is itself a failure — if the domain
//     moves, this test must fail loudly instead of guarding an empty set.
//
// The cross-check (rowShapeRegistryProblems) is a pure function so its own
// red fixtures can prove each rule without a fabricated domain: unregistered
// discovery, dangling registration, duplicate registration, and an exclusion
// missing any of its three hard requirements (reason, unlock condition,
// follow-up issue) all fail.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// lingdocDomainModulePath is the import path prefix of the scanned domain;
// the checklist's PackagePath entries are spelled in full form.
const lingdocDomainModulePath = "github.com/Tencent/WeKnora/internal/lingdoc"

// discoveredRowShape is one row structure the AST scan found: the package
// directory relative to internal/lingdoc, the file the TableName method lives
// in, the receiver type's name, and the constant table name it returns.
type discoveredRowShape struct {
	RelPackage string
	File       string
	TypeName   string
	TableName  string
}

// scanTableNameRowShapes walks one Go package tree (normally
// internal/lingdoc) and returns every type whose TableName() returns a
// constant string literal, plus scan problems. A scan problem is red: it
// means the scan cannot pin a TableName down (non-literal return, unparseable
// file), never something to skip quietly.
func scanTableNameRowShapes(t *testing.T, domainDir string) ([]discoveredRowShape, []string) {
	t.Helper()
	if _, err := os.Stat(domainDir); err != nil {
		return nil, []string{"找不到 LingDoc 领域目录 " + domainDir + "：" + err.Error()}
	}

	fset := token.NewFileSet()
	var discoveries []discoveredRowShape
	var problems []string
	checked := 0

	err := filepath.WalkDir(domainDir, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		source, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		checked++
		relPackage, relErr := filepath.Rel(domainDir, filepath.Dir(path))
		if relErr != nil {
			return relErr
		}
		relPackage = filepath.ToSlash(relPackage)
		file := filepath.Base(path)

		parsed, err := parser.ParseFile(fset, path, source, parser.SkipObjectResolution)
		if err != nil {
			problems = append(problems, file+": 无法解析（"+err.Error()+"）——守护无法钉扎其中的 TableName")
			return nil
		}
		for _, decl := range parsed.Decls {
			funcDecl, ok := decl.(*ast.FuncDecl)
			if !ok || funcDecl.Name.Name != "TableName" || funcDecl.Recv == nil || len(funcDecl.Recv.List) != 1 {
				continue
			}
			typeName, ok := receiverTypeName(funcDecl.Recv.List[0].Type)
			if !ok {
				problems = append(problems, file+":"+fset.Position(funcDecl.Pos()).String()+
					"：TableName 的接收者类型无法静态判定，守护拒绝放行")
				continue
			}
			table, problem := constantStringReturn(funcDecl.Body)
			if problem != "" {
				problems = append(problems, file+":"+fset.Position(funcDecl.Pos()).String()+
					"："+typeName+"."+problem)
				continue
			}
			discoveries = append(discoveries, discoveredRowShape{
				RelPackage: relPackage,
				File:       file,
				TypeName:   typeName,
				TableName:  table,
			})
		}
		return nil
	})
	if err != nil {
		return nil, []string{"扫描 LingDoc 领域失败：" + err.Error()}
	}
	if checked == 0 {
		return nil, []string{"没有扫描到任何 Go 文件，扫描范围可能已经失效"}
	}
	return discoveries, problems
}

// receiverTypeName resolves the receiver's type name for the plain and
// pointer spellings; anything else (generic receivers) is refused loudly.
func receiverTypeName(expr ast.Expr) (string, bool) {
	switch typed := expr.(type) {
	case *ast.Ident:
		return typed.Name, true
	case *ast.StarExpr:
		if ident, ok := typed.X.(*ast.Ident); ok {
			return ident.Name, true
		}
	}
	return "", false
}

// constantStringReturn extracts the single constant string a TableName body
// returns. Every return must be the same string literal; a computed or
// multi-valued return cannot be pinned by a syntax scan and is refused.
func constantStringReturn(body *ast.BlockStmt) (string, string) {
	if body == nil {
		return "", "TableName 没有函数体"
	}
	table := ""
	returns := 0
	ast.Inspect(body, func(node ast.Node) bool {
		result, ok := node.(*ast.ReturnStmt)
		if !ok {
			return true
		}
		returns++
		if len(result.Results) != 1 {
			return true
		}
		literal, ok := result.Results[0].(*ast.BasicLit)
		if !ok || literal.Kind != token.STRING {
			return true
		}
		value, err := strconv.Unquote(literal.Value)
		if err != nil {
			return true
		}
		if returns == 1 {
			table = value
			return true
		}
		if table != value {
			table = "" // conflicting constants: not one pinnable table name
		}
		return true
	})
	if returns == 0 {
		return "", "TableName 没有返回值"
	}
	if table == "" {
		return "", "TableName 未返回唯一的字符串字面量常量（守护要求字面量以便钉扎表名）"
	}
	return table, ""
}

// rowShapeRegistryProblems is the single cross-check between what the AST
// scan found and what the checklist registers. It is deliberately pure so
// each rule's red fixture can be exercised without a fabricated domain:
//
//   - a discovery that is neither registered nor explicitly excluded is red
//     (the "wrote a row structure and skipped the guard" hole);
//   - a registration that matches no discovery is red (dangling entry);
//   - a duplicate registration is red (checklist noise);
//   - an exclusion must match a real discovery and carry all three hard
//     requirements (reason, unlock condition, follow-up issue), and a
//     discovery may not be both registered and excluded.
func rowShapeRegistryProblems(
	discoveries []discoveredRowShape,
	registrations []rowShapeRegistration,
	exclusions []rowShapeExclusion,
) []string {
	var problems []string

	registered := func(discovery discoveredRowShape) bool {
		for _, reg := range registrations {
			if reg.PackagePath == lingdocDomainModulePath+"/"+discovery.RelPackage &&
				reg.TypeName == discovery.TypeName &&
				reg.TableName == discovery.TableName {
				return true
			}
		}
		return false
	}
	excluded := func(discovery discoveredRowShape) *rowShapeExclusion {
		for i := range exclusions {
			e := &exclusions[i]
			if e.RelPackage == discovery.RelPackage &&
				e.TypeName == discovery.TypeName &&
				e.TableName == discovery.TableName {
				return e
			}
		}
		return nil
	}

	for _, discovery := range discoveries {
		exclusion := excluded(discovery)
		switch {
		case registered(discovery) && exclusion != nil:
			problems = append(problems, discovery.relPath()+
				"：行结构既登记又被排除，两者只能取其一")
		case registered(discovery):
			// fine: the checklist covers it
		case exclusion != nil:
			// hard requirements checked below
		default:
			problems = append(problems, discovery.relPath()+
				"：行结构 "+discovery.TypeName+"（表 "+discovery.TableName+"）已定义 TableName 却未登记进 guardedRowShapes——"+
				"请登记（或按三硬要求显式排除），不许静默绕过形状守护")
		}
	}

	seen := map[string]bool{}
	for _, reg := range registrations {
		key := reg.PackagePath + "|" + reg.TypeName + "|" + reg.TableName
		if seen[key] {
			problems = append(problems, "登记项 "+reg.PackagePath+"."+reg.TypeName+"（表 "+reg.TableName+"）重复登记")
		}
		seen[key] = true

		// Out-of-domain entries (hostRowShapeDoors) sit outside this scan's
		// patrol area by design — the domain scan neither finds nor demands
		// them. They are reconciled against their own package by
		// TestHostRowShapeRegistrationsAreReal below, under the same
		// loud-failure standard.
		if _, isHost := hostRowShapeDoors[reg.PackagePath]; isHost {
			continue
		}

		found := false
		for _, discovery := range discoveries {
			if lingdocDomainModulePath+"/"+discovery.RelPackage == reg.PackagePath &&
				discovery.TypeName == reg.TypeName &&
				discovery.TableName == reg.TableName {
				found = true
				break
			}
		}
		if !found {
			problems = append(problems, "登记项 "+reg.PackagePath+"."+reg.TypeName+
				"（表 "+reg.TableName+"）在 AST 扫描中不存在（悬空登记）——类型已删除或表名已改，请同步清单")
		}
	}

	for i := range exclusions {
		e := &exclusions[i]
		found := false
		for _, discovery := range discoveries {
			if discovery.RelPackage == e.RelPackage &&
				discovery.TypeName == e.TypeName &&
				discovery.TableName == e.TableName {
				found = true
				break
			}
		}
		if !found {
			problems = append(problems, "排除项 "+e.RelPackage+"."+e.TypeName+
				"（表 "+e.TableName+"）在 AST 扫描中不存在（悬空排除）——请删除")
		}
		if strings.TrimSpace(e.Reason) == "" {
			problems = append(problems, "排除项 "+e.RelPackage+"."+e.TypeName+" 缺少逐条理由")
		}
		if strings.TrimSpace(e.UnlockCondition) == "" {
			problems = append(problems, "排除项 "+e.RelPackage+"."+e.TypeName+" 缺少写明的解除条件")
		}
		if e.FollowUpIssue <= 0 {
			problems = append(problems, "排除项 "+e.RelPackage+"."+e.TypeName+" 缺少跟进票号（#NNN）")
		}
	}

	return problems
}

func (d discoveredRowShape) relPath() string {
	return "internal/lingdoc/" + d.RelPackage + "/" + d.File
}

// TestLingDocTableNameRowStructsAreAllRegistered is the enforcement entry:
// every TableName row structure in the real domain must be covered by the
// checklist (minus explicit exclusions), and every checklist entry must be
// real. The scan logging the count keeps the checklist's growth visible in
// the evidence without hardcoding the number here.
func TestLingDocTableNameRowStructsAreAllRegistered(t *testing.T) {
	discoveries, problems := scanTableNameRowShapes(t, filepath.Join("..", "lingdoc"))
	for _, problem := range problems {
		t.Errorf("TableName 行结构扫描问题：%s", problem)
	}
	if len(discoveries) == 0 {
		t.Fatal("没有扫描到任何 TableName 行结构——扫描范围可能已经失效")
	}
	t.Logf("AST 扫描发现 %d 个 TableName 行结构", len(discoveries))

	for _, problem := range rowShapeRegistryProblems(discoveries, guardedRowShapes, deferredRowShapeExclusions) {
		t.Error(problem)
	}
}

// TestHostRowShapeRegistrationsAreReal reconciles the out-of-domain
// registrations (#713): a host entry's package sits outside the domain scan's
// patrol area, so scanning that package directly with the same parser is what
// keeps the entry honest — the type must really exist there, answer to the
// registered table, and the package must expose no OTHER TableName row
// structure that silently stays off the checklist's radar.
func TestHostRowShapeRegistrationsAreReal(t *testing.T) {
	hostDirs := map[string]bool{}
	for _, reg := range guardedRowShapes {
		if _, isHost := hostRowShapeDoors[reg.PackagePath]; !isHost {
			continue
		}
		// The domain scan bases at ".." (= internal/), so a host package's
		// dir is its path below internal/, not below the module root.
		relDir := strings.TrimPrefix(reg.PackagePath, "github.com/Tencent/WeKnora/internal/")
		if hostDirs[relDir] {
			continue
		}
		hostDirs[relDir] = true

		discoveries, problems := scanTableNameRowShapes(t, filepath.Join("..", relDir))
		for _, problem := range problems {
			t.Errorf("宿主行结构扫描问题（%s）：%s", relDir, problem)
		}
		if len(discoveries) == 0 {
			t.Fatalf("宿主包 %s 没有扫描到任何 TableName 行结构——扫描范围可能已经失效", relDir)
		}

		seen := map[string]bool{}
		for _, reg := range guardedRowShapes {
			if reg.PackagePath != "github.com/Tencent/WeKnora/internal/"+relDir {
				continue
			}
			found := false
			for _, discovery := range discoveries {
				if discovery.TypeName == reg.TypeName && discovery.TableName == reg.TableName {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("登记项 %s.%s（表 %s）在其宿主包扫描中不存在（悬空登记）",
					reg.PackagePath, reg.TypeName, reg.TableName)
			}
			seen[reg.TypeName] = true
		}
		for _, discovery := range discoveries {
			if !seen[discovery.TypeName] {
				t.Errorf("宿主包 %s 的行结构 %s（表 %s）未登记——宿主条目与域内条目适用同一守护标准",
					relDir, discovery.TypeName, discovery.TableName)
			}
		}
	}
}

// --- Red fixtures for the scan and the cross-check --------------------------
//
// Like the shape guard's mismatch fixtures, these prove the registry guard
// actually fails instead of passing vacuously. They never touch the real
// checklist.

// TestRowShapeRegistryScanFlagsTableNameThatDoesNotReturnALiteral proves the
// scan's strictness: a TableName returning anything but a single constant
// string literal is a scan problem, not a silent hole.
func TestRowShapeRegistryScanFlagsTableNameThatDoesNotReturnALiteral(t *testing.T) {
	sources := map[string]string{
		"const_name.go": "package fixture\n\nconst table = \"t\"\n\nfunc (row) TableName() string { return table }\n",
		"computed.go":   "package fixture\n\nfunc (row) TableName() string { return \"t\" + suffix }\n",
		"two_names.go": "package fixture\n\nfunc (row) TableName() string {\n" +
			"\tif truthy() {\n\t\treturn \"a\"\n\t}\n\treturn \"b\"\n}\n\nfunc truthy() bool { return true }\n",
	}
	for name, source := range sources {
		discoveries, problems := scanTableNameRowShapesInMemory(t, name, source)
		if len(discoveries) != 0 {
			t.Errorf("%s: 不应产生登记发现，got %v", name, discoveries)
		}
		if len(problems) == 0 {
			t.Errorf("%s: 非字面量 TableName 必须成为扫描问题，got none", name)
		}
	}
}

// TestRowShapeRegistryScanAcceptsLiteralTableNames proves the scanner's
// positive path with the plain, pointer, and multi-line literal spellings the
// domain actually uses.
func TestRowShapeRegistryScanAcceptsLiteralTableNames(t *testing.T) {
	source := "package fixture\n\n" +
		"type row struct{}\n\n" +
		"func (row) TableName() string { return \"plain\" }\n\n" +
		"type ptrRow struct{}\n\n" +
		"func (*ptrRow) TableName() string { return \"pointer\" }\n\n" +
		"type multiRow struct{}\n\n" +
		"func (multiRow) TableName() string {\n\treturn \"multiline\"\n}\n"
	discoveries, problems := scanTableNameRowShapesInMemory(t, "fixture.go", source)
	if len(problems) != 0 {
		t.Fatalf("字面量 TableName 不应产生扫描问题，got %v", problems)
	}
	got := map[string]string{}
	for _, discovery := range discoveries {
		got[discovery.TypeName] = discovery.TableName
	}
	want := map[string]string{"row": "plain", "ptrRow": "pointer", "multiRow": "multiline"}
	if len(got) != len(want) {
		t.Fatalf("应发现 %d 个行结构，got %d：%v", len(want), len(got), got)
	}
	for typeName, table := range want {
		if got[typeName] != table {
			t.Errorf("%s 应映射到表 %s，got %q", typeName, table, got[typeName])
		}
	}
}

// scanTableNameRowShapesInMemory runs the real scanner over one in-memory
// package file, so the fixtures exercise the same code path as the domain
// scan.
func scanTableNameRowShapesInMemory(t *testing.T, fileName, source string) ([]discoveredRowShape, []string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, fileName), []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	return scanTableNameRowShapes(t, dir)
}

// TestRowShapeRegistryFlagsAnUnregisteredRowStructure proves rule one: a
// discovery outside the checklist (and outside the exclusions) is red.
func TestRowShapeRegistryFlagsAnUnregisteredRowStructure(t *testing.T) {
	discoveries := []discoveredRowShape{{RelPackage: "fixture", File: "fixture.go", TypeName: "rogueRow", TableName: "lingdoc_rogue"}}
	problems := rowShapeRegistryProblems(discoveries, nil, nil)
	if len(problems) != 1 || !strings.Contains(problems[0], "rogueRow") || !strings.Contains(problems[0], "未登记") {
		t.Errorf("未登记行结构必须红并点名类型；got %v", problems)
	}
}

// TestRowShapeRegistryFlagsADanglingRegistration proves rule two: a checklist
// entry that matches no discovery is red, so the list cannot outlive the
// types it pins.
func TestRowShapeRegistryFlagsADanglingRegistration(t *testing.T) {
	registrations := []rowShapeRegistration{{
		PackagePath: lingdocDomainModulePath + ".ghost",
		TypeName:    "ghostRow",
		TableName:   "lingdoc_ghost",
	}}
	problems := rowShapeRegistryProblems(nil, registrations, nil)
	if len(problems) != 1 || !strings.Contains(problems[0], "悬空登记") {
		t.Errorf("悬空登记必须红；got %v", problems)
	}
}

// TestRowShapeRegistryFlagsADuplicateRegistration proves checklist noise is
// red: two entries for the same (package, type, table) add nothing and hide
// the diff.
func TestRowShapeRegistryFlagsADuplicateRegistration(t *testing.T) {
	discoveries := []discoveredRowShape{{RelPackage: "fixture", File: "fixture.go", TypeName: "row", TableName: "t"}}
	registrations := []rowShapeRegistration{
		{PackagePath: lingdocDomainModulePath + "/fixture", TypeName: "row", TableName: "t"},
		{PackagePath: lingdocDomainModulePath + "/fixture", TypeName: "row", TableName: "t"},
	}
	problems := rowShapeRegistryProblems(discoveries, registrations, nil)
	if len(problems) != 1 || !strings.Contains(problems[0], "重复登记") {
		t.Errorf("重复登记必须红；got %v", problems)
	}
}

// TestRowShapeRegistryFlagsAnExclusionMissingItsHardRequirements proves the
// DEFERRED_MANUAL ratchet on the exclusion mechanism: every exclusion needs a
// reason, a written unlock condition, and a follow-up issue, and a dangling
// exclusion is red. A compliant exclusion silences exactly its own discovery.
func TestRowShapeRegistryFlagsAnExclusionMissingItsHardRequirements(t *testing.T) {
	discoveries := []discoveredRowShape{{RelPackage: "fixture", File: "fixture.go", TypeName: "deferredRow", TableName: "t"}}
	compliant := rowShapeExclusion{
		RelPackage: "fixture", TypeName: "deferredRow", TableName: "t",
		Reason: "裁定为迁移侧修复但超出本票规模", UnlockCondition: "跟进票完成双方言成对迁移", FollowUpIssue: 9001,
	}
	if problems := rowShapeRegistryProblems(discoveries, nil, []rowShapeExclusion{compliant}); len(problems) != 0 {
		t.Errorf("合规排除不应红；got %v", problems)
	}

	dangling := compliant
	dangling.RelPackage = "elsewhere"
	for _, broken := range []rowShapeExclusion{
		{RelPackage: "fixture", TypeName: "deferredRow", TableName: "t", UnlockCondition: "x", FollowUpIssue: 1},
		{RelPackage: "fixture", TypeName: "deferredRow", TableName: "t", Reason: "x", FollowUpIssue: 1},
		{RelPackage: "fixture", TypeName: "deferredRow", TableName: "t", Reason: "x", UnlockCondition: "y"},
		dangling,
	} {
		problems := rowShapeRegistryProblems(discoveries, nil, []rowShapeExclusion{broken})
		if len(problems) == 0 {
			t.Errorf("排除项 %+v 缺硬要求必须红；got none", broken)
		}
	}
}
