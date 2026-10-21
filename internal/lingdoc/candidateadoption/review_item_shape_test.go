package candidateadoption_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/lingdoc/candidateadoption"
	"github.com/Tencent/WeKnora/internal/lingdoc/workspace"
)

// 这条守护守的是「候选形态唯一」（开发计划 A39 / I-9）。
//
// 候选层只有一种待核事项类型：candidateadoption.ReviewItem。工作副本侧保留
// 自己的类型（workspacecore.ReviewItem）——它本来就该在那儿——转换点在
// workspace 包内。
//
// 判据是**字段形状**，不是类型名，也不是「出现在哪几个包里」：
//
//   - 按类型名判会误报 docx.ReviewItem（处置与理由）与 template.ReviewItemInput
//     （版本与决定）——它们与待核事项无关。
//   - 按「只允许两个包」判会误报 delivery.ReviewItem——它是同形状的第三份
//     副本，那一笔债由具名例外登记着。
//
// 出现第四份同形状的类型（无论它叫什么名字）⇒ 守护红。

const (
	// reviewItemRepoRoot 是仓库根目录相对本包（internal/lingdoc/candidateadoption）的路径。
	reviewItemRepoRoot = "../../.."
	// candidateLayerPackage 是候选层的包目录：待核事项类型的拥有者。
	candidateLayerPackage = "internal/lingdoc/candidateadoption"
	// workingCopyPackage 是工作副本层的包目录：它保留自己的待核事项类型。
	workingCopyPackage = "internal/lingdoc/workspacecore"
)

// reviewItemShapeFields 是「候选层待核事项」这一形状的字段。
//
// 四个字段同时在场就是这一形状。判据来自开发计划 §3.4 的候选模型表。
var reviewItemShapeFields = []string{"ID", "Statement", "OriginCandidateID", "Anchor"}

// knownReviewItemShapeExceptions 列出仍然带着同形状副本的文件。
//
// 例外必须具名：写明是哪一处重复、为什么现在允许它、以及什么时候该收拢。
// 静默忽略等于没有守护——下一个人会以为候选层只有一份形状。
//
// 这一笔债是 delivery 包的待核事项快照：internal/lingdoc/delivery/ 不属 PF-2
// 的路径集，收拢候选层形状属后续项，不在本面范围。例外按文件登记，与
// writingaction 包的来源词表守护同一写法：一条具名例外对应一处已知重复；
// 债还了就把这一条删掉，守护会自己发现。
var knownReviewItemShapeExceptions = map[string]string{
	"internal/lingdoc/delivery/snapshot.go": "交付快照里的待核事项与候选层同形状；internal/lingdoc/delivery/ 不属 PF-2 的路径集，收拢候选层形状属后续项",
}

// candidateShapes 是「候选形态」的结构体。
//
// 它们的待核事项字段的元素类型必须是候选层的唯一类型：工作副本侧的类型
// 不得出现在候选形态里。
var candidateShapes = map[string]map[string]bool{
	candidateLayerPackage: {"Candidate": true, "Chapter": true},
	"internal/lingdoc/workspace": {"SelectedRewriteCandidate": true},
}

// TestCandidateReviewItemsHaveOneShape 断言仓内不存在第四份候选层待核事项形状。
func TestCandidateReviewItemsHaveOneShape(t *testing.T) {
	root := filepath.Clean(reviewItemRepoRoot)
	domain := filepath.Join(root, "internal", "lingdoc")
	if _, err := os.Stat(domain); err != nil {
		t.Fatalf("找不到 LingDoc 领域目录：%v", err)
	}

	fset := token.NewFileSet()
	var violations []string
	exceptionUse := map[string]int{}
	checked := 0

	err := filepath.Walk(domain, func(path string, entry os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		source, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		checked++

		file, err := parser.ParseFile(fset, path, source, parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		for _, decl := range file.Decls {
			generic, ok := decl.(*ast.GenDecl)
			if !ok || generic.Tok != token.TYPE {
				continue
			}
			for _, spec := range generic.Specs {
				typeSpec, ok := spec.(*ast.TypeSpec)
				if !ok {
					continue
				}
				structType, ok := typeSpec.Type.(*ast.StructType)
				if !ok || !hasReviewItemShape(structType) {
					continue
				}
				dir := packageDirOf(rel)
				switch dir {
				case candidateLayerPackage, workingCopyPackage:
					// 拥有者与工作副本侧：允许。
					continue
				}
				if _, known := knownReviewItemShapeExceptions[rel]; known {
					exceptionUse[rel]++
					continue
				}
				violations = append(violations, rel+":"+line(fset, typeSpec.Pos())+
					" 声明了 "+typeSpec.Name.Name+"：它与候选层待核事项同形状"+
					"（候选层待核事项只有 candidateadoption.ReviewItem 一种类型）")
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("扫描 LingDoc 领域失败：%v", err)
	}
	if checked == 0 {
		t.Fatal("没有扫描到任何文件，断言可能已经失效")
	}
	// 例外不得静默失效：一条不再覆盖任何站点的例外说明债已经还了，
	// 应当从名单里删掉，否则它会变成一个藏住新违规的口子。
	for file := range knownReviewItemShapeExceptions {
		if exceptionUse[file] == 0 {
			t.Errorf("例外 %s 已经不再覆盖任何同形状类型：债还了，请把这一条从名单里删掉", file)
		}
	}
	for _, violation := range violations {
		t.Error(violation)
	}
}

// TestCandidateShapesCarryTheCandidateLayerReviewItem 断言候选形态结构体的
// 待核事项字段用的是候选层的唯一类型。
//
// 工作副本侧的类型不得出现在候选形态里：它只作为工作副本侧类型存在。
func TestCandidateShapesCarryTheCandidateLayerReviewItem(t *testing.T) {
	root := filepath.Clean(reviewItemRepoRoot)
	domain := filepath.Join(root, "internal", "lingdoc")
	fset := token.NewFileSet()
	var violations []string
	checked := 0

	err := filepath.Walk(domain, func(path string, entry os.FileInfo, err error) error {
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
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		dir := packageDirOf(rel)
		shapes, ok := candidateShapes[dir]
		if !ok {
			return nil
		}

		file, err := parser.ParseFile(fset, path, source, parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		for _, decl := range file.Decls {
			generic, ok := decl.(*ast.GenDecl)
			if !ok || generic.Tok != token.TYPE {
				continue
			}
			for _, spec := range generic.Specs {
				typeSpec, ok := spec.(*ast.TypeSpec)
				if !ok || !shapes[typeSpec.Name.Name] {
					continue
				}
				structType, ok := typeSpec.Type.(*ast.StructType)
				if !ok {
					continue
				}
				checked++
				field, found := reviewItemField(structType)
				if !found {
					violations = append(violations, rel+":"+line(fset, typeSpec.Pos())+
						" 的 "+typeSpec.Name.Name+" 没有待核事项字段（候选形态必须带待核事项）")
					continue
				}
				owner := reviewItemOwner(file, dir, field)
				if owner != candidateLayerPackage {
					violations = append(violations, rel+":"+line(fset, field.Pos())+
						" 的 "+typeSpec.Name.Name+" 的待核事项类型来自 "+owner+
						"（候选形态的待核事项必须是 candidateadoption.ReviewItem）")
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("扫描 LingDoc 领域失败：%v", err)
	}
	if checked == 0 {
		t.Fatal("没有扫描到任何候选形态结构体，断言可能已经失效")
	}
	for _, violation := range violations {
		t.Error(violation)
	}
}

// TestSelectedRewriteCandidateCarriesTheCandidateLayerReviewItem 断言改写候选
// 的待核事项字段的元素类型是候选层的唯一类型。
//
// 这里用反射而不是编译期赋值：编译期断言在类型写错时会让整个包的测试二进制
// 编译不过，那就没有下文了——守护得自己把话说出来。
func TestSelectedRewriteCandidateCarriesTheCandidateLayerReviewItem(t *testing.T) {
	expected := reflect.TypeOf(candidateadoption.ReviewItem{})
	for _, shape := range []any{workspace.SelectedRewriteCandidate{}, candidateadoption.Candidate{}} {
		field, ok := reflect.TypeOf(shape).FieldByName("ReviewItems")
		if !ok {
			t.Fatalf("%T 没有待核事项字段", shape)
		}
		if field.Type.Elem() != expected {
			t.Errorf("%T 的待核事项元素类型 = %v，期望 %v（候选形态的待核事项必须是 candidateadoption.ReviewItem）",
				shape, field.Type.Elem(), expected)
		}
	}
}

// hasReviewItemShape 判断一个结构体是不是候选层待核事项的形状。
func hasReviewItemShape(structType *ast.StructType) bool {
	present := map[string]bool{}
	for _, field := range structType.Fields.List {
		for _, name := range field.Names {
			present[name.Name] = true
		}
	}
	for _, name := range reviewItemShapeFields {
		if !present[name] {
			return false
		}
	}
	return true
}

// reviewItemField 找出结构体里承载待核事项的字段。
func reviewItemField(structType *ast.StructType) (*ast.Field, bool) {
	for _, field := range structType.Fields.List {
		array, ok := field.Type.(*ast.ArrayType)
		if !ok {
			continue
		}
		if !isReviewItemType(array.Elt) {
			continue
		}
		return field, true
	}
	return nil, false
}

// isReviewItemType 判断一个类型表达式是不是某个待核事项类型。
func isReviewItemType(expr ast.Expr) bool {
	switch typed := expr.(type) {
	case *ast.Ident:
		return typed.Name == "ReviewItem"
	case *ast.SelectorExpr:
		return typed.Sel.Name == "ReviewItem"
	}
	return false
}

// reviewItemOwner 判断一个待核事项字段的元素类型来自哪个包。
func reviewItemOwner(file *ast.File, dir string, field *ast.Field) string {
	array, ok := field.Type.(*ast.ArrayType)
	if !ok {
		return "未知"
	}
	switch typed := array.Elt.(type) {
	case *ast.Ident:
		// 同包里的待核事项类型。
		return dir
	case *ast.SelectorExpr:
		ident, ok := typed.X.(*ast.Ident)
		if !ok {
			return "未知"
		}
		for _, spec := range file.Imports {
			path, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				continue
			}
			alias := path
			if index := strings.LastIndex(path, "/"); index >= 0 {
				alias = path[index+1:]
			}
			if spec.Name != nil {
				alias = spec.Name.Name
			}
			if alias == ident.Name {
				return lingdocPackageOf(path)
			}
		}
		return ident.Name
	}
	return "未知"
}

// packageDirOf 取一个仓内路径所属的包目录。
func packageDirOf(rel string) string {
	index := strings.LastIndex(rel, "/")
	if index < 0 {
		return ""
	}
	return rel[:index]
}

// lingdocPackageOf 把一个导入路径折成仓内的包目录。
func lingdocPackageOf(importPath string) string {
	index := strings.Index(importPath, "internal/lingdoc/")
	if index < 0 {
		return importPath
	}
	return importPath[index:]
}

// line 返回一个位置所在的行号。
func line(fset *token.FileSet, pos token.Pos) string {
	return strconv.Itoa(fset.Position(pos).Line)
}
