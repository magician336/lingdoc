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
// 扫描范围是「**所有驱动模型的文件**」，判据有两条：
//
//  1. **调用了模型调用点**：RewriteSelected（选段与评审）与 Generate（整章）。
//     这两个名字是模型边界上的入口名——谁调用它，谁就是把请求交到模型面前的
//     那一层。
//  2. **显式必扫清单**：模型适配器的实现文件与请求组装文件。判据一发现不了
//     「实现了模型调用点」的文件——实现是方法声明，不是调用——所以清单是第二
//     道保险。
//
// internal/lingdoc/workspace/agent_context.go 是**人**发起的 Agent 动作分发
// （它调的 ApplyChangeSet / 导出都在人工门禁之后），它不驱动模型，因此不在
// 扫描范围内；http.go 同理（它是 HTTP 边界，不是模型驱动方）。

const (
	// aiPathRepoRoot 是仓库根目录相对本包（internal/lingdoc/candidateadoption）的路径。
	aiPathRepoRoot = "../../.."
	// aiPathDeliveryPackage 是导出与交付快照的拥有者包目录。
	aiPathDeliveryPackage = "internal/lingdoc/delivery"
)

// aiPathModelCallSites 是模型调用点的名字。
//
// 判据是**调用点**：谁调用它，谁就驱动模型。它测的是知识归属（这条文件把
// 请求交到了模型面前），不是文件名的形状。
var aiPathModelCallSites = map[string]string{
	"RewriteSelected": "选段与评审的模型调用点",
	"Generate":        "整章生成的模型调用点",
}

// aiPathMustScan 是必须进入扫描面的文件（相对仓库根，斜杠分隔）。
//
// 判据：这条文件**驱动模型**——它是模型适配器的实现方，或它把请求组装好交给
// 模型。整包扫描会把与 AI 无关的 HTTP 与交付代码拖进来（误报），按文件列举
// 又会在新增模型驱动文件时漏掉（漏报）；两条判据合起来才是完整的扫描面。
var aiPathMustScan = []string{
	// 整章生成的模型适配器与它的请求组装。
	"internal/lingdoc/workspace/generation.go",
	// 选段管道的模型适配器与它的请求组装。
	"internal/lingdoc/workspace/selected_rewrite.go",
	"internal/lingdoc/workspace/selected_rewrite_model.go",
	// 评审的执行方：它同样把请求交到模型面前。
	"internal/lingdoc/workspace/selected_rewrite_review.go",
	// 可选上下文的解析方：它决定什么进入模型请求。
	"internal/lingdoc/workspace/selected_rewrite_context.go",
}

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

// TestAIPathScanSurfaceCoversEveryModelDrivingFile 断言扫描面本身没有缩水。
//
// 没有这一条，守护是「恰好没写」而不是「守得住」：扫描面少一条文件，守护照样
// 是绿的。因此这里把「必须被扫到」的文件逐个点名——工作台侧的五个模型驱动方
// 一个都不能少。
func TestAIPathScanSurfaceCoversEveryModelDrivingFile(t *testing.T) {
	scanned := map[string]bool{}
	for _, path := range aiPathFiles(t) {
		rel, err := filepath.Rel(filepath.Clean(aiPathRepoRoot), path)
		if err != nil {
			t.Fatalf("计算相对路径失败：%v", err)
		}
		scanned[filepath.ToSlash(rel)] = true
	}
	if len(scanned) == 0 {
		t.Fatal("没有扫描到任何 AI 路径文件，断言可能已经失效")
	}
	for _, rel := range aiPathMustScan {
		if !scanned[rel] {
			t.Errorf("模型驱动文件 %s 不在扫描面内，守护对它不生效", rel)
		}
	}
	// 生成管道整包在扫描面内：它的每个文件都在这条管道上。
	generation, err := os.ReadDir(filepath.Join(filepath.Clean(aiPathRepoRoot), "internal", "lingdoc", "generation"))
	if err != nil {
		t.Fatalf("读取生成路径目录失败：%v", err)
	}
	for _, entry := range generation {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		rel := "internal/lingdoc/generation/" + entry.Name()
		if !scanned[rel] {
			t.Errorf("生成管道文件 %s 不在扫描面内，守护对它不生效", rel)
		}
	}
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

// aiPathFiles 列出所有驱动模型的文件（非测试文件）。
//
// 扫描面由三部分组成：生成包整个目录、工作台包里**调用了模型调用点**的文件、
// 以及一份**显式必扫清单**。三者缺一，扫描面就不完整：只扫生成包会漏掉工作台
// 侧的模型驱动方，只扫清单会漏掉将来新增的调用点。
//
// 扫描面为空 ⇒ 断言失效（下面的断言会红）。
func aiPathFiles(t *testing.T) []string {
	t.Helper()
	root := filepath.Clean(aiPathRepoRoot)
	seen := map[string]bool{}
	var files []string
	add := func(path string) {
		if seen[path] {
			return
		}
		seen[path] = true
		files = append(files, path)
	}

	// 生成包整包都是模型驱动的：它的每个文件都在这条管道上。
	generation := filepath.Join(root, "internal", "lingdoc", "generation")
	entries, err := os.ReadDir(generation)
	if err != nil {
		t.Fatalf("读取生成路径目录失败：%v", err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		add(filepath.Join(generation, entry.Name()))
	}

	// 工作台包只扫「驱动模型」的文件，不整包扫：里面还有 HTTP 边界、交付与
	// 治理代码，那些文件不是 AI 路径，整包扫会误报。
	workspace := filepath.Join(root, "internal", "lingdoc", "workspace")
	entries, err = os.ReadDir(workspace)
	if err != nil {
		t.Fatalf("读取工作台路径目录失败：%v", err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		path := filepath.Join(workspace, entry.Name())
		if drivesModel(path) {
			add(path)
		}
	}

	// 显式必扫清单：模型适配器的实现与请求组装，AST 判据发现不了它们。
	for _, rel := range aiPathMustScan {
		path := filepath.Join(root, filepath.FromSlash(rel))
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("必扫的 AI 路径文件 %s 不存在：%v", rel, err)
		}
		add(path)
	}
	return files
}

// drivesModel 说这条文件是否驱动模型。
//
// 判据是**调用了模型调用点**：RewriteSelected / Generate。谁调用它，谁就是把
// 请求交到模型面前的那一层。它只看调用，不看实现——实现由必扫清单兜住。
func drivesModel(path string) bool {
	source, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, source, parser.SkipObjectResolution)
	if err != nil {
		return false
	}
	found := false
	ast.Inspect(file, func(node ast.Node) bool {
		if found {
			return false
		}
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if _, guarded := aiPathModelCallSites[selector.Sel.Name]; guarded {
			found = true
			return false
		}
		return true
	})
	return found
}

// aiGuardLine 返回一个位置所在的行号。
func aiGuardLine(fset *token.FileSet, pos token.Pos) string {
	return strconv.Itoa(fset.Position(pos).Line)
}
