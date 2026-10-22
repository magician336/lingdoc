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

// 这组断言守的是「运行模式的词表只有一个拥有者」。
//
// 运行模式有三档取值：real / mock / real_api_fake_model。它们的**词表**归
// candidateadoption 所有——那是候选形态与失效判据的同一个拥有者。词表一旦有
// 第二个拥有者，两处就会各说各话：一条管道报 "real"、另一条报
// "real_api_fake_model"，谁也说不清这次到底跑的是哪一种。
//
// 判据按**标识符**判，不按裸字符串判：
//
//   - "real" / "mock" 这两个词在别处有别的含义（模型名、开关名、测试夹具名），
//     按字符串判会满仓误报。所以只判那三个**词表成员的名字**。
//   - 拥有者包内部随便用；包外只允许**带限定符**的引用（candidateadoption.X），
//     不允许裸名——裸名说明这个包自己抄了一份词表。
//
// 守护判的是**知识归属**：谁拥有这个名字，谁就拥有那次取值。

// runModeVocabularyOwner 是运行模式词表的拥有者包。
const runModeVocabularyOwner = "candidateadoption"

// runModeVocabulary 是运行模式的三个词表成员。
//
// 只列这三个标识符：它们是词表成员的**名字**，不是取值。取值是 "real" /
// "mock" / "real_api_fake_model"。
var runModeVocabulary = map[string]string{
	"RunModeReal":             "真模型",
	"RunModeMock":             "没有 provider",
	"RunModeRealAPIFakeModel": "真 API 假模型",
}

// TestRunModeVocabularyHasExactlyOneOwner 断言运行模式的词表只有一个拥有者。
//
// 包外出现裸名 ⇒ 那个包自己抄了一份词表 ⇒ 红。包外出现带限定符的引用
// （candidateadoption.RunModeReal）⇒ 正常。
func TestRunModeVocabularyHasExactlyOneOwner(t *testing.T) {
	root := filepath.Clean(aiPathRepoRoot)
	base := filepath.Join(root, "internal", "lingdoc")
	var violations []string
	ownerDefinitions := map[string]bool{}
	fset := token.NewFileSet()

	err := filepath.WalkDir(base, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		if !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			return nil
		}
		source, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		file, parseErr := parser.ParseFile(fset, path, source, parser.SkipObjectResolution)
		if parseErr != nil {
			return parseErr
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		rel = filepath.ToSlash(rel)
		pkg := file.Name.Name
		for _, violation := range runModeVocabularyViolations(fset, file, pkg, rel) {
			violations = append(violations, violation)
		}
		if pkg == runModeVocabularyOwner {
			for name := range runModeVocabulary {
				if definesRunModeConstant(file, name) {
					ownerDefinitions[name] = true
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("扫描 internal/lingdoc 失败：%v", err)
	}
	for _, violation := range violations {
		t.Error(violation)
	}
	// 拥有者必须真的定义了这三个成员：否则守护是空转的（改名即失效）。
	for name := range runModeVocabulary {
		if !ownerDefinitions[name] {
			t.Errorf("运行模式词表的拥有者 %s 没有定义 %s，守护已经空转", runModeVocabularyOwner, name)
		}
	}
}

// runModeVocabularyViolations 列出这条文件对运行模式词表的越界使用。
func runModeVocabularyViolations(fset *token.FileSet, file *ast.File, pkg, rel string) []string {
	// 带限定符的引用是合法的：candidateadoption.RunModeReal。限定符的名字按
	// 这条文件**实际导入**的名字算——别名也是带限定符的引用。
	owner := ownerQualifier(file)
	qualified := map[*ast.Ident]bool{}
	ast.Inspect(file, func(node ast.Node) bool {
		selector, ok := node.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		sel := selector.Sel
		if _, guarded := runModeVocabulary[sel.Name]; !guarded {
			return true
		}
		if owner != "" {
			if qualifier, ok := selector.X.(*ast.Ident); ok && qualifier.Name == owner {
				qualified[sel] = true
			}
		}
		return true
	})

	var violations []string
	ast.Inspect(file, func(node ast.Node) bool {
		ident, ok := node.(*ast.Ident)
		if !ok {
			return true
		}
		meaning, guarded := runModeVocabulary[ident.Name]
		if !guarded {
			return true
		}
		if pkg == runModeVocabularyOwner || qualified[ident] {
			return true
		}
		violations = append(violations, rel+":"+strconv.Itoa(fset.Position(ident.Pos()).Line)+
			" 裸用了运行模式词表成员 "+ident.Name+"（"+meaning+
			"）；词表归 "+runModeVocabularyOwner+" 所有，别处只能带限定符引用")
		return true
	})
	return violations
}

// ownerQualifier 说这条文件用什么名字导入拥有者包。
//
// 别名也算：别名仍然是「通过拥有者包引用词表」，不是抄了一份。没有导入就是空
// 值——此时任何裸名使用都是越界。
func ownerQualifier(file *ast.File) string {
	for _, spec := range file.Imports {
		path, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			continue
		}
		if !strings.HasSuffix(path, "/internal/lingdoc/"+runModeVocabularyOwner) {
			continue
		}
		if spec.Name != nil {
			return spec.Name.Name
		}
		return runModeVocabularyOwner
	}
	return ""
}

// definesRunModeConstant 说这条文件是否定义了这个词表成员。
func definesRunModeConstant(file *ast.File, name string) bool {
	found := false
	ast.Inspect(file, func(node ast.Node) bool {
		decl, ok := node.(*ast.GenDecl)
		if !ok || decl.Tok != token.CONST {
			return true
		}
		for _, spec := range decl.Specs {
			value, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for _, ident := range value.Names {
				if ident.Name == name {
					found = true
				}
			}
		}
		return true
	})
	return found
}
