package workspace

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/clienterror"
	"github.com/gin-gonic/gin"
)

// The client presentation of a business failure has exactly one declaration
// mechanism: the clienterror package.
//
// There used to be a second one. A failure could state its outward shape with
// a private interface — ClientStatus() int, ClientCode() string,
// ClientMessage() string — and the transport would look for that interface
// first. The two mechanisms are not interchangeable: the transport renders
// clienterror.Presenter, so a presentation declared on the retired interface
// is invisible to it. The failure does not crash and does not fail loudly. It
// falls through to the generic 500, and the caller is told "internal_error"
// for a failure whose own code named the cause exactly.
//
// That is not hypothetical. A merge brought in an error that declared its
// presentation on the retired interface; no test failed, because no test
// asserted the rendered code. The guard below makes the retirement
// structural instead of a convention: the method names of the retired
// interface may not come back anywhere in the host tree.
//
// A declaration on the retired interface is not "a second way to do the same
// thing" — it is a way to say nothing at all, because the transport will
// never read it.

// retiredPresentationMethods are the method names of the interface the
// clienterror package replaced. Declaring one of them is the defect.
var retiredPresentationMethods = map[string]bool{
	"ClientStatus":  true,
	"ClientCode":    true,
	"ClientMessage": true,
}

func TestClientPresentationHasOneDeclarationMechanism(t *testing.T) {
	root := filepath.Join("..", "..", "..")
	internal := filepath.Join(root, "internal")
	if _, err := os.Stat(internal); err != nil {
		t.Fatalf("找不到 internal 目录：%v", err)
	}

	fset := token.NewFileSet()
	var problems []string

	err := filepath.WalkDir(internal, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		if filepath.Ext(path) != ".go" {
			return nil
		}
		// A test file may build a fixture that resembles the retired shape;
		// only production code has to answer for the outward form.
		if strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, parseErr := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if parseErr != nil {
			return parseErr
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv == nil {
				continue
			}
			if !retiredPresentationMethods[fn.Name.Name] {
				continue
			}
			rel, relErr := filepath.Rel(root, path)
			if relErr != nil {
				rel = path
			}
			problems = append(problems, rel+":"+strconv.Itoa(fset.Position(fn.Pos()).Line)+
				" 声明了 "+fn.Name.Name+"()——对外形态只能通过 internal/clienterror 声明")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("扫描 internal 失败：%v", err)
	}
	for _, problem := range problems {
		t.Error(problem)
	}
}

// TestSendErrorRendersTheDeclaredForm checks the rendering end of the
// mechanism: the shape an error declares is the shape the caller sees.
//
// The contract test above pins the declared shapes; this one pins that the
// declared shape is what reaches the response. Without it, an error could
// declare a perfect form and still be rendered as the generic 500 — which is
// exactly what a presentation on the retired interface produced.
func TestSendErrorRendersTheDeclaredForm(t *testing.T) {
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/v1/lingdoc/projects/p", nil)

	declared := clienterror.New(clienterror.Form{
		Status: 500, Code: "audit_write_failed",
		Message: "审计记录写入失败，本次操作未生效，请稍后重试。",
	}, "audit_write_failed: storage error")

	sendError(ctx, declared)

	if recorder.Code != 500 {
		t.Fatalf("status = %d, want 500", recorder.Code)
	}
	var body struct {
		Error struct {
			Code      string `json:"code"`
			Message   string `json:"message"`
			Retryable bool   `json:"retryable"`
		} `json:"error"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("解析响应失败：%v", err)
	}
	if body.Error.Code != "audit_write_failed" {
		t.Fatalf("code = %q, want %q", body.Error.Code, "audit_write_failed")
	}
	if body.Error.Message != "审计记录写入失败，本次操作未生效，请稍后重试。" {
		t.Fatalf("message = %q", body.Error.Message)
	}
	if body.Error.Retryable {
		t.Fatal("retryable = true, want false")
	}
}
