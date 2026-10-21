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

// 这组断言守的是「AI 不得直接写正式章节版本、不得建确认、不得应用变更集、
// 不得导出」（开发计划 A12、A13、A14）。
//
// 判据测的是**知识归属**，不是字符串:
//
//   - 正式章节版本的写入口只有一处语义：工作副本被提交成正式版本
//     （workspacecore.Service.CommitWorkingCopy），以及候选被显式采纳后
//     由采纳端建版本。AI 路径只产出**候选**，候选变成正式版本只走人工确认
//     的采纳。
//   - 章节确认的写入口是 candidateadoption.ConfirmationService.ConfirmChapter。
//   - 变更集应用是 workspacecore.Service.ApplyChangeSet（它自己就在人工门禁
//     名单里）。
//   - 导出与交付快照由 internal/lingdoc/delivery 拥有（ExportService /
//     ReleaseService）。
//
// 所以判据是：**AI 路径的文件里不得出现这些写入口的名字，也不得出现这些表
// 的名字**。AI 路径一旦自己伸手去写这些东西，这条断言就红。
//
// 扫描范围只含两条模型驱动的路径：整章生成与选段改写。
// internal/lingdoc/workspace/agent_context.go 是**人**发起的 Agent 动作分发
// （它调的 ApplyChangeSet / 导出都在人工门禁之后），不是 AI 路径，所以不在
// 扫描范围内。

const (
	// aiPathRepoRoot 是仓库根目录相对本包（internal/lingdoc/candidateadoption）的路径。
	aiPathRepoRoot = "../../.."
	// aiPathDeliveryPackage 是导出与交付快照的拥有者包目录。
	aiPathDeliveryPackage = "internal/lingdoc/delivery"
)

// guardedTables 是「正式版本 / 确认 / 变更集 / 导出」这些写入口背后的表。
var guardedTables = map[string]string{
	"lingdoc_chapter_versions":      "正式章节版本",
	"lingdoc_chapter_confirmations": "章节确认",
	"lingdoc_change_sets":           "变更集",
	"lingdoc_delivery_exports":      "导出产物",
}

// guardedWriteEntries 是那些写入口的名字。
//
// 判据按**名字**判，因为这些名字就是知识归属的标记：谁拥有这个名字，谁就
// 拥有那次写。
var guardedWriteEntries = map[string]string{
	"CommitWorkingCopy": "把工作副本提交成正式章节版本",
	"ConfirmChapter":    "建立章节确认",
	"ApplyChangeSet":    "应用变更集",
}

// TestAIPathsCannotWriteFormalVersionsConfirmationsChangeSetsOrExports 断言
// AI 路径够不到正式版本、确认、变更集与导出。
func TestAIPathsCannotWriteFormalVersionsConfirmationsChangeSetsOrExports(t *testing.T) {
	files := aiPathFiles(t)
	if len(files) == 0 {
		t.Fatal("没有扫描到任何 AI 路径文件，断言可能已经失效")
	}
	fset := token.NewFileSet()
	var violations []string
	for _, path := range files {
		source, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("读取 %s 失败：%v", path, err)
		}
		file, err := parser.ParseFile(fset, path, source, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("解析 %s 失败：%v", path, err)
		}
		rel, _ := filepath.Rel(filepath.Clean(aiPathRepoRoot), path)
		rel = filepath.ToSlash(rel)

		ast.Inspect(file, func(node ast.Node) bool {
			switch typed := node.(type) {
			case *ast.BasicLit:
				if typed.Kind != token.STRING {
					return true
				}
				text, err := strconv.Unquote(typed.Value)
				if err != nil {
					return true
				}
				if meaning, guarded := guardedTables[text]; guarded {
					violations = append(violations, rel+":"+aiGuardLine(fset, typed.Pos())+
						" 写了 "+meaning+" 的表名 "+text+"（AI 路径不得直接写它）")
				}
			case *ast.SelectorExpr:
				if meaning, guarded := guardedWriteEntries[typed.Sel.Name]; guarded {
					violations = append(violations, rel+":"+aiGuardLine(fset, typed.Pos())+
						" 引用了写入口 "+typed.Sel.Name+"（"+meaning+"；AI 路径不得直接调用它）")
				}
			}
			return true
		})

		// 导出与交付快照的拥有者是 delivery 包：AI 路径一旦引入它，就可能
		// 够到导出入口。
		for _, spec := range file.Imports {
			importPath, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				continue
			}
			if strings.Contains(importPath, aiPathDeliveryPackage) {
				violations = append(violations, rel+":"+aiGuardLine(fset, spec.Pos())+
					" 引入了导出与交付快照的拥有者包（AI 路径不得导出）")
			}
		}
	}
	for _, violation := range violations {
		t.Error(violation)
	}
}

// aiPathFiles 列出两条模型驱动路径上的非测试文件。
func aiPathFiles(t *testing.T) []string {
	t.Helper()
	root := filepath.Clean(aiPathRepoRoot)
	var files []string
	generation := filepath.Join(root, "internal", "lingdoc", "generation")
	entries, err := os.ReadDir(generation)
	if err != nil {
		t.Fatalf("读取生成路径目录失败：%v", err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		files = append(files, filepath.Join(generation, entry.Name()))
	}
	workspace := filepath.Join(root, "internal", "lingdoc", "workspace")
	for _, name := range []string{"selected_rewrite.go", "selected_rewrite_model.go"} {
		path := filepath.Join(workspace, name)
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("找不到改写路径文件 %s：%v", name, err)
		}
		files = append(files, path)
	}
	return files
}

// aiGuardLine 返回一个位置所在的行号。
func aiGuardLine(fset *token.FileSet, pos token.Pos) string {
	return strconv.Itoa(fset.Position(pos).Line)
}
