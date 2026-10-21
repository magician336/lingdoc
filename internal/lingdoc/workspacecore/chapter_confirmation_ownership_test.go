package workspacecore

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

// The chapter confirmation flag has exactly one write point.
//
// "Is this chapter confirmed" is one question, so the answer must be cleared
// in one place. Three situations make a chapter unconfirmed — the chapter body
// rewrite, the working copy commit and the formal basis change — and each of
// them used to clear the flag on its own. A new path that forgets to clear it
// would report a chapter as confirmed after its content changed.
//
// Two functions may write the flag, and no others:
//
//   - markChapterUnconfirmed, the one place that clears it;
//   - chapterView, which produces the answer by asking the predicate.
//
// The assertion walks the package's own source and reports every assignment to
// a `ConfirmationValid` field outside those two. It looks at assignments only:
// a struct literal that spells the field out (the chapter view starts life with
// the flag false) is not a write to it.

const confirmationWritePoint = "markChapterUnconfirmed"

// confirmationAnswerBuilder is where the flag gets its value: the chapter view
// asks the predicate.
const confirmationAnswerBuilder = "chapterView"

func TestChapterConfirmationFlagHasOneWritePoint(t *testing.T) {
	fset := token.NewFileSet()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("读取包目录失败：%v", err)
	}
	var problems []string
	checked := 0
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, name, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("解析 %s 失败：%v", name, err)
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			if fn.Name.Name != confirmationWritePoint && fn.Name.Name != confirmationAnswerBuilder {
				ast.Inspect(fn.Body, func(node ast.Node) bool {
					assign, ok := node.(*ast.AssignStmt)
					if !ok {
						return true
					}
					for _, lhs := range assign.Lhs {
						sel, ok := lhs.(*ast.SelectorExpr)
						if !ok || sel.Sel.Name != "ConfirmationValid" {
							continue
						}
						checked++
						problems = append(problems, name+":"+strconv.Itoa(fset.Position(assign.Pos()).Line)+
							" 直接写了 ConfirmationValid（应经过 "+confirmationWritePoint+"）")
					}
					return true
				})
				continue
			}
			// The two allowed writers are counted too: a counter that only sees
			// violations would read zero once the code is clean, and the
			// assertion would be vacuous.
			ast.Inspect(fn.Body, func(node ast.Node) bool {
				assign, ok := node.(*ast.AssignStmt)
				if !ok {
					return true
				}
				for _, lhs := range assign.Lhs {
					if sel, ok := lhs.(*ast.SelectorExpr); ok && sel.Sel.Name == "ConfirmationValid" {
						checked++
					}
				}
				return true
			})
		}
	}
	if checked == 0 {
		t.Fatal("没有找到任何 ConfirmationValid 的赋值，断言可能已经失效")
	}
	for _, problem := range problems {
		t.Error(problem)
	}
}

// The write point itself must exist and do what the assertion assumes.
func TestChapterConfirmationWritePointExists(t *testing.T) {
	chapter := Chapter{ConfirmationValid: true}
	markChapterUnconfirmed(&chapter)
	if chapter.ConfirmationValid {
		t.Fatal("写入点没有清掉确认标志")
	}
}

// The file the assertion reads must be the package's own source, not a
// different copy: a stale path would make the assertion vacuously pass.
func TestChapterConfirmationAssertionReadsThePackageItself(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(wd) != "workspacecore" {
		t.Fatalf("断言在 %s 下运行，不是 workspacecore 包", wd)
	}
}
