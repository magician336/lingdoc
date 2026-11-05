package workspace

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// The source-marker syntax has exactly one owner: the sourcemarker package.
// Everything above it consumes that package.
//
// This assertion exists because the syntax used to be implemented eight times
// — four byte-identical regular expressions, three hand-written scanners and
// one bare counter. Each copy accepted a different identifier character set,
// so the same body could be accepted on one path and rejected on another.
//
// Two kinds of drift are checked:
//
//  1. A caller re-implements the syntax (its own regexp, or its own scan of
//     the "[[source:" prefix).
//  2. A caller mentions the syntax in a string literal. That is allowed only
//     for the two model prompts, which tell the model how to cite; they are
//     listed with their reason so a new prompt cannot slip in unnoticed.

const sourceMarkerOwner = "sourcemarker"

// markerMentionsOutsideCode lists the production string literals that may
// contain the marker syntax without owning it. Each entry says why.
//
// The prompts are not parsers: they show the model the shape of a citation
// ("[[source:SOURCE_ID]]"), and SOURCE_ID is a placeholder rather than a real
// identifier, so these literals never reach the parser.
var markerMentionsOutsideCode = map[string]string{
	"internal/lingdoc/workspace/generation.go":             "研究写作助手的系统提示词：告诉模型用哪种形状标注引用",
	"internal/lingdoc/workspace/selected_rewrite_model.go": "选区改写助手的系统提示词：告诉模型用哪种形状标注引用",
}

func TestSourceMarkerSyntaxHasOneOwner(t *testing.T) {
	root := filepath.Join("..", "..", "..")
	lingdoc := filepath.Join(root, "internal", "lingdoc")
	owner := filepath.Join(lingdoc, sourceMarkerOwner)
	if _, err := os.Stat(owner); err != nil {
		t.Fatalf("引用的拥有者包不存在：%v", err)
	}
	ownerSrc, err := os.ReadFile(filepath.Join(owner, "sourcemarker.go"))
	if err != nil {
		t.Fatalf("读取拥有者实现失败：%v", err)
	}
	for _, want := range []string{"func IDs(", "func Validate(", "func ReplaceAll(", "func Strip(", "func TokenID("} {
		if !strings.Contains(string(ownerSrc), want) {
			t.Fatalf("拥有者包不再提供 %s，接缝被削掉了", want)
		}
	}

	fset := token.NewFileSet()
	var problems []string
	seenMentions := map[string]bool{}

	err = filepath.WalkDir(lingdoc, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if path == owner {
				return filepath.SkipDir
			}
			return nil
		}
		name := entry.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			rel = path
		}
		rel = filepath.ToSlash(rel)

		file, parseErr := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if parseErr != nil {
			problems = append(problems, rel+": 解析失败："+parseErr.Error())
			return nil
		}
		ast.Inspect(file, func(node ast.Node) bool {
			switch call := node.(type) {
			case *ast.CallExpr:
				problems = append(problems, checkMarkerCall(rel, fset, call)...)
			case *ast.BasicLit:
				if call.Kind != token.STRING {
					break
				}
				value, unquoteErr := strconv.Unquote(call.Value)
				if unquoteErr != nil || !strings.Contains(value, "[[source:") {
					break
				}
				if _, allowed := markerMentionsOutsideCode[rel]; !allowed {
					problems = append(problems, rel+": 出现了引用标记字面量但不在允许清单里："+firstLine(value))
				} else {
					seenMentions[rel] = true
				}
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatalf("扫描生产代码失败：%v", err)
	}
	if len(problems) > 0 {
		t.Fatalf("引用标记语法不再是唯一实现：\n  %s", strings.Join(problems, "\n  "))
	}
	for rel := range markerMentionsOutsideCode {
		if !seenMentions[rel] {
			t.Fatalf("允许清单里的 %s 已经没有引用标记字面量，请把它从清单里删掉", rel)
		}
	}
}

// checkMarkerCall reports a caller that re-implements the marker syntax
// instead of consuming the owning package.
func checkMarkerCall(rel string, fset *token.FileSet, call *ast.CallExpr) []string {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return nil
	}
	pkg, ok := sel.X.(*ast.Ident)
	if !ok {
		return nil
	}
	name := sel.Sel.Name
	arg := ""
	if len(call.Args) > 0 {
		if lit, litOK := call.Args[0].(*ast.BasicLit); litOK && lit.Kind == token.STRING {
			if v, err := strconv.Unquote(lit.Value); err == nil {
				arg = v
			}
		}
	}
	switch {
	case pkg.Name == "regexp" && (name == "MustCompile" || name == "Compile"):
		if strings.Contains(arg, "source:") {
			return []string{fmt.Sprintf("%s:%d 自己编译了引用标记的正则", rel, fset.Position(call.Pos()).Line)}
		}
	case pkg.Name == "strings" && (name == "Count" || name == "Index" || name == "IndexByte" || name == "HasPrefix" || name == "HasSuffix"):
		if strings.Contains(arg, "[[source:") {
			return []string{fmt.Sprintf("%s:%d 自己扫描了引用标记前缀", rel, fset.Position(call.Pos()).Line)}
		}
	}
	return nil
}

func firstLine(value string) string {
	value = strings.TrimSpace(value)
	if idx := strings.Index(value, "\n"); idx >= 0 {
		value = value[:idx]
	}
	if len(value) > 60 {
		value = value[:60] + "…"
	}
	return value
}
