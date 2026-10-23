package types_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

// 这组断言守的是「租户角色词表只有一个拥有者」。
//
// 租户角色（owner / admin / contributor / viewer）是仓内**一个**概念：用户在
// 租户里的身份。它有且只有一份词表，定义在本文件所属的包里（internal/types），
// 判定入口也只有一处（TenantRole.IsValid）。
//
// 词表一旦有第二个拥有者，两处就会各说各话：审计列 actor_role 里会出现
// 一个只在一处合法的词，而读那一列的人无从判断它是「角色写错了」还是
// 「读的人不知道还有另一套词」。
//
// 判据分四层，从��到外：
//
//  1. 词表的成员与取值固定为四个，且 IsValid 是唯一的判定入口；
//  2. 词表成员的**标识符**在拥有者包之外只允许带限定符引用
//     （types.TenantRoleOwner）—— 不允许裸名，裸名说明有人自己抄了一份
//     常量词表；
//  3. 词表成员的**取值字面量**不允许在任何地方被列成一份清单
//     （形如 []string{"owner", "admin"} 的复合字面量）；
//  4. 持有 Actor.Role 的两个面（rulesetstore / templategovernance）不允许拿
//     租户角色字面量做比较——它们手里的角色词只能来自权威词表。
//
// 第 4 层刻意只覆盖这两个面：仓内还有别的角色词表（成员的项目角色、治理
// 角色、职能角色，以及跨租户共享用的 OrgMemberRole），它们与租户角色是
// 不同的概念，共享部分词。把它们并进来就是要求本守护替别的概念做判断，
// 那既越界也判不准。

// tenantRoleVocabularyOwner 是租户角色词表的拥有者包。
const tenantRoleVocabularyOwner = "types"

// tenantRoleVocabularyMembers 是词表的四个成员标识符。
//
// 这里列的是**名字**，不是取值。取值在 tenantRoleWords 里。
var tenantRoleVocabularyMembers = []string{
	"TenantRoleOwner",
	"TenantRoleAdmin",
	"TenantRoleContributor",
	"TenantRoleViewer",
}

// tenantRoleWords 是词表成员的取值。
//
// 只列这四个词：它们是租户角色的**取值**。别的词表（成员项目角色、治理角色、
// 职能角色、OrgMemberRole）里的词不在这里，因为它们不是同一个概念。
var tenantRoleWords = map[string]bool{
	"owner":       true,
	"admin":       true,
	"contributor": true,
	"viewer":      true,
}

// actorRoleHolders 是持有 Actor.Role 的两个面。
//
// 这两个面把角色写进审计列，所以它们的角色词必须来自权威词表；它们自己
// 不持有第二份清单。
var actorRoleHolders = []string{
	"internal/lingdoc/rulesetstore",
	"internal/lingdoc/templategovernance",
}

// TestTenantRoleVocabularyIsTheOnlyDefinition 断言词表的成员与取值固定，
// 并且 IsValid 是唯一的判定入口。
func TestTenantRoleVocabularyIsTheOnlyDefinition(t *testing.T) {
	declared := map[string]bool{
		string(types.TenantRoleOwner):       true,
		string(types.TenantRoleAdmin):       true,
		string(types.TenantRoleContributor): true,
		string(types.TenantRoleViewer):      true,
	}
	if len(declared) != 4 {
		t.Fatalf("tenant role vocabulary has %d distinct values (%v), want exactly four", len(declared), declared)
	}
	for word := range tenantRoleWords {
		if !declared[word] {
			t.Errorf("tenant role vocabulary is missing %q", word)
		}
	}
	for word := range declared {
		if !tenantRoleWords[word] {
			t.Errorf("tenant role vocabulary has an unexpected value %q", word)
		}
	}
	for word := range tenantRoleWords {
		if !types.TenantRole(word).IsValid() {
			t.Errorf("IsValid rejected the declared tenant role %q", word)
		}
	}
	// 词表外的值一律不合法。空串与大小写变体在列：词表是精确匹配的。
	for _, word := range []string{
		"", " ", "member", "collaborator", "author", "researcher", "reviewer",
		"tenant_admin", "tenant_viewer", "template_publisher",
		"OWNER", "Owner", " admin", "admin ",
	} {
		if types.TenantRole(word).IsValid() {
			t.Errorf("IsValid accepted %q, which is outside the tenant role vocabulary", word)
		}
	}
}

// TestTenantRoleVocabularyMembersHaveExactlyOneOwner 断言词表成员的标识符在
// 拥有者包之外只以带限定符的形式出现。
//
// 裸名意味着那个包自己抄了一份常量词表：两份常量以后会各走各的。
func TestTenantRoleVocabularyMembersHaveExactlyOneOwner(t *testing.T) {
	root := tenantRoleRepoRoot(t)
	base := filepath.Join(root, "internal")
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
		rel := relTo(root, path)
		// 拥有者包内部随便用：同一个包里的裸名就是「通过拥有者包引用词表」。
		inOwnerDir := strings.HasPrefix(rel, "internal/types/")
		if !inOwnerDir {
			violations = append(violations, tenantRoleVocabularyViolations(fset, file, rel)...)
		}
		if inOwnerDir {
			for _, name := range tenantRoleVocabularyMembers {
				if definesTenantRoleConstant(file, name) {
					ownerDefinitions[name] = true
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("scanning internal/ failed: %v", err)
	}
	for _, violation := range violations {
		t.Error(violation)
	}
	// 拥有者必须真的定义了这四个成员，否则守护是空转的（改名即失效）。
	for _, name := range tenantRoleVocabularyMembers {
		if !ownerDefinitions[name] {
			t.Errorf("the tenant role vocabulary owner %s does not define %s; the guard is spinning", tenantRoleVocabularyOwner, name)
		}
	}
}

// TestNoSecondTenantRoleVocabularyIsSpelledOut 断言仓内没有任何地方把租户
// 角色的取值列成第二份清单。
//
// 判据是「一个复合字面量里同时出现两个以上不同的租户角色取值」。散落的
// 单次比较不算清单：那是判定，不是词表。
func TestNoSecondTenantRoleVocabularyIsSpelledOut(t *testing.T) {
	root := tenantRoleRepoRoot(t)
	base := filepath.Join(root, "internal")
	var violations []string
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
		rel := relTo(root, path)
		ast.Inspect(file, func(node ast.Node) bool {
			literal, ok := node.(*ast.CompositeLit)
			if !ok {
				return true
			}
			words := map[string]bool{}
			for _, element := range literal.Elts {
				basic, ok := element.(*ast.BasicLit)
				if !ok || basic.Kind != token.STRING {
					continue
				}
				value, err := strconv.Unquote(basic.Value)
				if err != nil {
					continue
				}
				if tenantRoleWords[value] {
					words[value] = true
				}
			}
			if len(words) >= 2 {
				violations = append(violations, rel+":"+strconv.Itoa(fset.Position(literal.Pos()).Line)+
					" spells out a tenant role vocabulary ("+strings.Join(sortedKeys(words), ", ")+
					"); the vocabulary is owned by "+tenantRoleVocabularyOwner+" and must not be copied")
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatalf("scanning internal/ failed: %v", err)
	}
	for _, violation := range violations {
		t.Error(violation)
	}
}

// TestActorRoleHoldersDoNotJudgeRolesByLiteral 断言持有 Actor.Role 的两个面
// 不拿租户角色字面量做判定。
//
// 这些面的角色词只能来自权威词表：它们把角色写进审计列，写进去的必须是
// 读者能解释的词。判定也必须走同一份词表，否则「写进去的是词表里的词、
// 判的是另一份词」这种分家就会发生。
func TestActorRoleHoldersDoNotJudgeRolesByLiteral(t *testing.T) {
	root := tenantRoleRepoRoot(t)
	fset := token.NewFileSet()
	for _, holder := range actorRoleHolders {
		dir := filepath.Join(root, filepath.FromSlash(holder))
		var violations []string
		err := filepath.WalkDir(dir, func(path string, entry os.DirEntry, err error) error {
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
			rel := relTo(root, path)
			ast.Inspect(file, func(node ast.Node) bool {
				binary, ok := node.(*ast.BinaryExpr)
				if !ok {
					return true
				}
				switch binary.Op {
				case token.EQL, token.NEQ, token.LSS, token.GTR, token.LEQ, token.GEQ:
				default:
					return true
				}
				for _, side := range []ast.Expr{binary.X, binary.Y} {
					basic, ok := side.(*ast.BasicLit)
					if !ok || basic.Kind != token.STRING {
						continue
					}
					value, err := strconv.Unquote(basic.Value)
					if err != nil || !tenantRoleWords[value] {
						continue
					}
					violations = append(violations, rel+":"+strconv.Itoa(fset.Position(basic.Pos()).Line)+
						" compares a role against the literal "+strconv.Quote(value)+
						"; use types.TenantRole and its vocabulary instead")
				}
				return true
			})
			return nil
		})
		if err != nil {
			t.Fatalf("scanning %s failed: %v", holder, err)
		}
		for _, violation := range violations {
			t.Error(violation)
		}
	}
}

// tenantRoleVocabularyViolations 列出这条文件对词表成员的越界使用。
func tenantRoleVocabularyViolations(fset *token.FileSet, file *ast.File, rel string) []string {
	// 带限定符的引用是合法的：types.TenantRoleOwner。限定符的名字按这条文件
	// **实际导入**的名字算——别名也是带限定符的引用。
	owner := tenantRoleOwnerQualifier(file)
	qualified := map[*ast.Ident]bool{}
	ast.Inspect(file, func(node ast.Node) bool {
		selector, ok := node.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		sel := selector.Sel
		if !slices.Contains(tenantRoleVocabularyMembers, sel.Name) {
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
		if !slices.Contains(tenantRoleVocabularyMembers, ident.Name) {
			return true
		}
		if qualified[ident] {
			return true
		}
		violations = append(violations, rel+":"+strconv.Itoa(fset.Position(ident.Pos()).Line)+
			" uses the tenant role vocabulary member "+ident.Name+" without its qualifier; the vocabulary is owned by "+
			tenantRoleVocabularyOwner+" and must be referenced through it")
		return true
	})
	return violations
}

// tenantRoleOwnerQualifier 说这条文件用什么名字导入拥有者包。
//
// 别名也算：别名仍然是「通过拥有者包引用词表」，不是抄了一份。没有导入就是
// 空值——此时任何裸名使用都是越界。
func tenantRoleOwnerQualifier(file *ast.File) string {
	for _, spec := range file.Imports {
		path, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			continue
		}
		if path != "github.com/Tencent/WeKnora/internal/types" {
			continue
		}
		if spec.Name != nil {
			return spec.Name.Name
		}
		return tenantRoleVocabularyOwner
	}
	return ""
}

// definesTenantRoleConstant 说这条文件是否定义了这个词表成员。
func definesTenantRoleConstant(file *ast.File, name string) bool {
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

// tenantRoleRepoRoot 定位仓库根目录。
//
// 用本测试文件的绝对路径反推，因此在任何工作目录下都成立。
func tenantRoleRepoRoot(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate the guard test source file")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(thisFile), "..", ".."))
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("repository root not found at %s: %v", root, err)
	}
	return root
}

// relTo 把绝对路径说成相对仓库根的斜杠路径。
func relTo(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return filepath.ToSlash(path)
	}
	return filepath.ToSlash(rel)
}

// sortedKeys 返回集合的键，按字典序排列，让失败信息可复核。
func sortedKeys(set map[string]bool) []string {
	keys := make([]string, 0, len(set))
	for key := range set {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}
