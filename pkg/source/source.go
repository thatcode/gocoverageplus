// Package source reads source files from a given path and create entities from this
package source

import (
	"fmt"
	"go/ast"
	"go/token"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/tools/go/packages"

	"github.com/Fabianexe/gocoverageplus/pkg/entity"
)

func LoadSources(path string, excludePaths []string) (*entity.Project, error) {
	goPackages, err := getGoPaths(path, excludePaths)
	if err != nil {
		return nil, err
	}

	cfg := &packages.Config{
		Mode: packages.NeedName |
			packages.NeedFiles |
			packages.NeedSyntax |
			packages.NeedTypesInfo |
			packages.NeedModule |
			packages.NeedTypes,
		Dir: path,
	}

	pkgs, err := packages.Load(cfg, goPackages...)
	if err != nil {
		return nil, err
	}

	var countPackages, countFiles, countMethods int
	allPackages := make([]*entity.Package, 0, len(pkgs))
	for _, pkg := range pkgs {
		pack := &entity.Package{
			Name:  pkg.PkgPath,
			Files: make([]*entity.File, 0, len(pkg.Syntax)),
			Fset:  pkg.Fset,
		}

		slog.Debug("Package", "Path", pkg.PkgPath, "Files", len(pkg.Syntax))
		for i, fileAst := range pkg.Syntax {
			methodsMap := make(map[string][]*entity.Method)
			for _, decl := range fileAst.Decls {
				// Normal function Declaration found
				if fun, ok := decl.(*ast.FuncDecl); ok {
					method := readFunc(fun.Name.Name, fun.Body, pkg)
					if method == nil {
						continue
					}

					countMethods++
					className := getClassName(fun)
					methodsMap[className] = append(methodsMap[className], method)

					continue
				}
				// Normal var found. Could contain functions
				if gen, ok := decl.(*ast.GenDecl); ok && gen.Tok == token.VAR {
					for _, spec := range gen.Specs {
						valueSpec, ok := spec.(*ast.ValueSpec)
						// only interested in ValueSpecs
						if !ok {
							continue
						}

						// Check all value definitions
						for pos, value := range valueSpec.Values {
							// found a function that is called. We extract the function
							if callExpr, ok := value.(*ast.CallExpr); ok {
								value = callExpr.Fun
							}

							funcLit, ok := value.(*ast.FuncLit)
							// only interested in inplace defined functions with a var name
							if !ok || len(valueSpec.Names) < pos {
								continue
							}

							method := readFunc(valueSpec.Names[pos].Name, funcLit.Body, pkg)
							if method == nil {
								continue
							}

							countMethods++
							methodsMap["-"] = append(methodsMap["-"], method)
						}
					}
				}
			}

			var methodCount int
			for className, methods := range methodsMap {
				file := &entity.File{
					Name:     className,
					FilePath: pkg.GoFiles[i],
					Ast:      fileAst,
					Methods:  methods,
				}
				pack.Files = append(pack.Files, file)

				methodCount += len(methods)
			}

			slog.Debug("File", "Name", filepath.Base(pkg.GoFiles[i]), "Methods", methodCount)

			countFiles++
		}

		countPackages++
		allPackages = append(allPackages, pack)
	}
	slog.Info("Source reading Finished", "Packages", countPackages, " Files", countFiles, " Methods", countMethods)
	return &entity.Project{Packages: allPackages}, nil
}

// readFunc processes a function's body, constructing and returning a Method representation by using the given name and package.
func readFunc(name string, body *ast.BlockStmt, pkg *packages.Package) *entity.Method {
	method := &entity.Method{
		Name: name,
		Body: body,
		File: pkg.Fset.File(body.Pos()),
	}

	// start after the function declaration
	startLine := pkg.Fset.Position(body.Lbrace).Line + 1
	endLine := pkg.Fset.Position(body.End()).Line
	if startLine >= endLine {
		return nil
	}

	bV := &branchVisitor{
		fset: pkg.Fset,
	}

	ast.Walk(bV, body)

	method.Tree = bV.getTree()

	return method
}

func getGoPaths(path string, excludePaths []string) ([]string, error) {
	goPath := make(map[string]struct{}, 1000)
	err := filepath.Walk(path, func(path string, info os.FileInfo, err error) error {
		if filepath.Ext(path) == ".go" {
			goPath[filepath.Dir(path)] = struct{}{}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	fullExcludePaths := make([]string, 0, len(excludePaths))
	for _, exclude := range excludePaths {
		middle := ""
		if !strings.HasSuffix(exclude, "/") {
			middle = "/"
		}

		fullExcludePaths = append(fullExcludePaths, path+middle+exclude)
	}

	p := path + "/pkg"
	goPackages := make([]string, 0, len(goPath))
packLoop:
	for pack := range goPath {
		for _, exclude := range fullExcludePaths {
			if strings.HasPrefix(pack, exclude) {
				continue packLoop
			}
		}

		fmt.Println(p, pack)
		goPackages = append(goPackages, pack)
	}
	return goPackages, nil
}

func getClassName(fun *ast.FuncDecl) string {
	if fun.Recv == nil {
		return "-"
	}

	if star, ok := fun.Recv.List[0].Type.(*ast.StarExpr); ok {
		if index, ok := star.X.(*ast.IndexExpr); ok {
			return index.X.(*ast.Ident).Name
		}

		if index, ok := star.X.(*ast.IndexListExpr); ok {
			return index.X.(*ast.Ident).Name
		}

		return star.X.(*ast.Ident).Name
	}

	if index, ok := fun.Recv.List[0].Type.(*ast.IndexExpr); ok {
		return index.X.(*ast.Ident).Name
	}

	if index, ok := fun.Recv.List[0].Type.(*ast.IndexListExpr); ok {
		return index.X.(*ast.Ident).Name
	}

	return fun.Recv.List[0].Type.(*ast.Ident).Name
}
