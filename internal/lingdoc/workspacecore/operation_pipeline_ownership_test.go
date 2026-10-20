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

// The operation pipeline has exactly one owner.
//
// The pipeline is the order and the rules that every mutating operation runs
// under: the key check, the human decision gate, the request digest, the
// source recheck, the authorization, the audit write, the idempotency record
// and the business write. It lives in `operationcore`.
//
// The workspace used to carry that pipeline inside itself, in
// `service_facade.go`, next to the aggregate's own code. That is the shape
// this assertion exists to prevent: the moment the aggregate reads or writes
// the idempotency record on its own, the two can drift, and a replay can
// stop meaning the same thing here and there.
//
// The assertion walks the workspace's own source and reports any place that
// writes the idempotency record outside the pipeline's ledger seam.
//
// The write is the exclusive business of the pipeline: a stored record is the
// pipeline's proof that the operation already happened, so only the pipeline
// may create one. A read is a different matter - the provenance check reads
// the record to confirm that it and the stored proof still agree, and that
// check is the workspace's own consistency rule, not a pipeline step. Only
// the write is asserted here.
//
// Two kinds of file are out of its reach and are not counted:
//
//   - this package's pipeline seam, which is where the workspace hands the
//     pipeline its ledger;
//   - test files, which produce fixtures rather than run the pipeline.
//
// The transaction boundary is deliberately not asserted here: the workspace
// opens transactions for its own reads and single-aggregate writes all over
// the place, and those are not pipeline steps. The pipeline's own boundary is
// asserted where it lives, in `operationcore`.

func TestOperationPipelineHasOneOwner(t *testing.T) {
	// The seam file is where the workspace is supposed to reach for the
	// pipeline's collaborators. Everything else in the package must go
	// through it.
	const seamFile = "operation_pipeline.go"

	root := filepath.Join("..", "..", "..")
	domain := filepath.Join(root, "internal", "lingdoc", "workspacecore")
	if _, err := os.Stat(domain); err != nil {
		t.Fatalf("找不到工作区核心目录：%v", err)
	}

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
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		if filepath.Base(path) == seamFile {
			return nil
		}
		source, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		checked++

		file, err := parser.ParseFile(fset, path, source, parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		ast.Inspect(file, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			// The receiver is the workspace's own operation record store, or
			// a local variable holding one.
			receiver, ok := sel.X.(*ast.Ident)
			if !ok {
				return true
			}
			if receiver.Name != "operations" {
				return true
			}
			if sel.Sel.Name != "SaveOperation" {
				return true
			}
			violations = append(violations, rel+":"+strconv.Itoa(fset.Position(call.Pos()).Line)+
				" 幂等记录被绕过管道写入（应经过 operationcore 的 Ledger 接缝）")
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatalf("扫描工作区核心失败：%v", err)
	}
	if checked == 0 {
		t.Fatal("没有扫描到任何文件，断言可能已经失效")
	}
	for _, violation := range violations {
		t.Error(violation)
	}
}
