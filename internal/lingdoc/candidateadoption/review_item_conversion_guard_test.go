package candidateadoption_test

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

// 这条断言核对 A39 的另一半：工作副本侧的同形类型只经**一个**转换点进出。
//
// 候选层的待核事项只有 candidateadoption.ReviewItem 一种类型；工作副本侧保留
// 自己的类型（workspacecore.ReviewItem）。两者字段逐字相同，所以 JSON 格式
// 不变，改的是 Go 类型——而类型转换必须只有一个地方做。
//
// 两条判据：
//
//  1. 采纳链路（candidateadoption 包）不得引用工作副本侧的待核事项类型：
//     采纳端带着候选层的类型一路走，转换留给工作副本侧。
//  2. 全域里把候选层类型转成工作副本侧类型的函数**只有一个**，且它在
//     workspace 包内（开发计划 §3.4 的转换点归属）。
//
// 同形状的第三份副本（delivery 包的快照）由 review_item_shape_test.go 的具名
// 例外登记着；这条断言管的是**转换点**，不是形状。

const (
	// reviewItemConversionRepoRoot 是仓库根目录相对本包（internal/lingdoc/candidateadoption）的路径。
	reviewItemConversionRepoRoot = "../../.."
	// workingCopyReviewItemPackage 是工作副本侧待核事项类型的拥有者包目录。
	workingCopyReviewItemPackage = "internal/lingdoc/workspacecore"
	// adoptionPackagePath 是采纳端的包目录。
	adoptionPackagePath = "internal/lingdoc/candidateadoption"
)

// TestTheAdoptionChainNeverCarriesTheWorkingCopyReviewItem 断言采纳链路不碰
// 工作副本侧的待核事项类型。
func TestTheAdoptionChainNeverCarriesTheWorkingCopyReviewItem(t *testing.T) {
	root := filepath.Clean(reviewItemConversionRepoRoot)
	adoption := filepath.Join(root, filepath.FromSlash(adoptionPackagePath))
	fset := token.NewFileSet()
	var violations []string
	checked := 0

	entries, err := os.ReadDir(adoption)
	if err != nil {
		t.Fatalf("读取采纳端目录失败：%v", err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		path := filepath.Join(adoption, entry.Name())
		source, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("读取 %s 失败：%v", entry.Name(), err)
		}
		file, err := parser.ParseFile(fset, path, source, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("解析 %s 失败：%v", entry.Name(), err)
		}
		checked++
		alias := importAliasOf(file, workingCopyReviewItemPackage)
		if alias == "" {
			continue
		}
		ast.Inspect(file, func(node ast.Node) bool {
			selector, ok := node.(*ast.SelectorExpr)
			if !ok || selector.Sel.Name != "ReviewItem" {
				return true
			}
			ident, ok := selector.X.(*ast.Ident)
			if !ok || ident.Name != alias {
				return true
			}
			violations = append(violations, entry.Name()+":"+strconv.Itoa(fset.Position(selector.Pos()).Line)+
				" 引用了工作副本侧的待核事项类型（采纳链路带着候选层的类型走）")
			return true
		})
	}
	if checked == 0 {
		t.Fatal("没有扫描到任何采纳端文件，断言可能已经失效")
	}
	for _, violation := range violations {
		t.Error(violation)
	}
}

// TestReviewItemConversionHasExactlyOnePoint 断言转换点只有一个。
func TestReviewItemConversionHasExactlyOnePoint(t *testing.T) {
	root := filepath.Clean(reviewItemConversionRepoRoot)
	domain := filepath.Join(root, "internal", "lingdoc")
	fset := token.NewFileSet()
	type conversion struct {
		file string
		line int
	}
	var conversions []conversion
	checked := 0

	err := filepath.Walk(domain, func(path string, entry os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		source, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		file, err := parser.ParseFile(fset, path, source, parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		checked++
		alias := importAliasOf(file, workingCopyReviewItemPackage)
		if alias == "" {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		for _, decl := range file.Decls {
			function, ok := decl.(*ast.FuncDecl)
			if !ok || function.Type.Results == nil {
				continue
			}
			for _, result := range function.Type.Results.List {
				array, ok := result.Type.(*ast.ArrayType)
				if !ok {
					continue
				}
				selector, ok := array.Elt.(*ast.SelectorExpr)
				if !ok || selector.Sel.Name != "ReviewItem" {
					continue
				}
				ident, ok := selector.X.(*ast.Ident)
				if !ok || ident.Name != alias {
					continue
				}
				conversions = append(conversions, conversion{file: rel, line: fset.Position(function.Pos()).Line})
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("扫描 LingDoc 领域失败：%v", err)
	}
	if checked == 0 {
		t.Fatal("没有扫描到任何领域文件，断言可能已经失效")
	}
	if len(conversions) != 1 {
		for _, found := range conversions {
			t.Errorf("%s:%d 是一个候选层→工作副本侧的待核事项转换点", found.file, found.line)
		}
		t.Fatalf("候选层→工作副本侧的待核事项转换点有 %d 个，期望恰好一个", len(conversions))
	}
	if !strings.HasPrefix(conversions[0].file, "internal/lingdoc/workspace/") {
		t.Errorf("转换点在 %s，期望在 workspace 包内（转换点归属开发计划 §3.4）", conversions[0].file)
	}
}

// importAliasOf 取一个文件里某个仓内包目录的导入别名。
func importAliasOf(file *ast.File, packageDir string) string {
	for _, spec := range file.Imports {
		importPath, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			continue
		}
		if !strings.HasSuffix(importPath, packageDir) {
			continue
		}
		if spec.Name != nil {
			return spec.Name.Name
		}
		return packageDir[strings.LastIndex(packageDir, "/")+1:]
	}
	return ""
}
