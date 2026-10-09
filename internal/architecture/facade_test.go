package architecture_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

const sdkPath = "github.com/vasyza/pumpfun-sdk"

func repository(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("The test path is not available.")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

func selector(expr ast.Expr) *ast.SelectorExpr {
	switch expr := expr.(type) {
	case *ast.SelectorExpr:
		return expr
	case *ast.IndexExpr:
		return selector(expr.X)
	case *ast.IndexListExpr:
		return selector(expr.X)
	default:
		return nil
	}
}

func TestRootContainsOnlyPublicFacade(t *testing.T) {
	root := repository(t)
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(root, name)
			fset := token.NewFileSet()
			file, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
			if err != nil {
				t.Fatal(err)
			}
			if file.Name.Name != "pumpfun" {
				t.Fatal("The facade must use package pumpfun.")
			}
			if name == "doc.go" {
				if len(file.Decls) != 0 {
					t.Fatal("The package document must contain no code.")
				}
				return
			}
			module := strings.TrimSuffix(name, ".go")
			alias := "sdk" + strings.ToUpper(module[:1]) + module[1:]
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.HasPrefix(string(data), "// Public "+module+" API; implementation lives in internal/"+module+".\n") {
				t.Fatal("The facade must start with its module comment.")
			}
			if len(file.Imports) != 1 || file.Imports[0].Name == nil || file.Imports[0].Name.Name != alias || file.Imports[0].Path.Value != strconv.Quote(sdkPath+"/internal/"+module) {
				t.Fatal("The facade must import its module with the SDK alias.")
			}
			checkAlias := func(expr ast.Expr) {
				t.Helper()
				sel := selector(expr)
				if sel == nil {
					t.Fatal("The public value must be an alias from its module.")
				}
				id, ok := sel.X.(*ast.Ident)
				if !ok || id.Name != alias {
					t.Fatal("The alias must use the module import.")
				}
			}
			for _, declaration := range file.Decls {
				switch declaration := declaration.(type) {
				case *ast.GenDecl:
					for _, spec := range declaration.Specs {
						switch spec := spec.(type) {
						case *ast.ImportSpec:
						case *ast.TypeSpec:
							if !spec.Assign.IsValid() {
								t.Fatal("The public type must be a type alias.")
							}
							checkAlias(spec.Type)
						case *ast.ValueSpec:
							if declaration.Tok != token.CONST && (declaration.Tok != token.VAR || module != "errs") {
								t.Fatal("Only constant aliases and error references are permitted.")
							}
							if len(spec.Names) != len(spec.Values) {
								t.Fatal("Each value must have a module alias.")
							}
							for _, value := range spec.Values {
								checkAlias(value)
							}
						default:
							t.Fatal("The facade contains an unsupported declaration.")
						}
					}
				case *ast.FuncDecl:
					if declaration.Doc == nil || declaration.Recv != nil || declaration.Body == nil || len(declaration.Body.List) != 1 || fset.Position(declaration.Pos()).Line != fset.Position(declaration.End()).Line {
						t.Fatal("The wrapper must have a comment and one line of code.")
					}
					ret, ok := declaration.Body.List[0].(*ast.ReturnStmt)
					if !ok || len(ret.Results) != 1 {
						t.Fatal("The wrapper must return one module call.")
					}
					call, ok := ret.Results[0].(*ast.CallExpr)
					if !ok {
						t.Fatal("The wrapper must call its module.")
					}
					checkAlias(call.Fun)
					for _, arg := range call.Args {
						if _, ok := arg.(*ast.Ident); !ok {
							t.Fatal("The wrapper must pass its input without changes.")
						}
					}
				default:
					t.Fatal("The facade contains an unsupported declaration.")
				}
			}
		})
	}
}

func TestSDKDoesNotImportApplicationCode(t *testing.T) {
	root := repository(t)
	for _, module := range []string{"client", "coins", "trades", "curve", "stream", "solana", "models", "errs", "transport"} {
		paths, err := filepath.Glob(filepath.Join(root, "internal", module, "*.go"))
		if err != nil {
			t.Fatal(err)
		}
		for _, path := range paths {
			if strings.HasSuffix(path, "_test.go") {
				continue
			}
			file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
			if err != nil {
				t.Fatal(err)
			}
			for _, spec := range file.Imports {
				importPath, err := strconv.Unquote(spec.Path.Value)
				if err != nil {
					t.Fatal(err)
				}
				for _, blocked := range []string{sdkPath + "/internal/cli", sdkPath + "/internal/mcp", sdkPath + "/internal/config", sdkPath + "/internal/logging", "github.com/spf13/cobra", "github.com/modelcontextprotocol/go-sdk"} {
					if importPath == blocked || strings.HasPrefix(importPath, blocked+"/") {
						t.Errorf("SDK file %s imports application package %s", path, importPath)
					}
				}
			}
		}
	}
}
