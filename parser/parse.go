package parser

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/importer"
	"go/parser"
	"go/printer"
	"go/token"
	"go/types"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"
)

type SwaggerConfig struct {
	ParseDirs      []string
	Filter         string
	PrintGenerate  bool
	SwaggerOptions []func(*Parser)
	// SwaggerURL        string
	OutputDir         string
	FormatSwaggerJSON bool
}

type Option func(*SwaggerConfig)

func ParseDir(app *gin.Engine, options ...Option) error {
	var (
		fileSet token.FileSet
		config  SwaggerConfig
	)

	for _, option := range options {
		option(&config)
	}

	if len(config.SwaggerOptions) == 0 {
		config.SwaggerOptions = []func(*Parser){
			func(p *Parser) {
				p.swagger.BasePath = "/"
				p.swagger.Host = ""
				p.ParseDependency = true
			},
		}
	} else {
		config.SwaggerOptions = append(config.SwaggerOptions, func(p *Parser) {
			p.swagger.BasePath = "/"
			p.swagger.Host = ""
			p.ParseDependency = true
		})
	}
	config.Filter = "func(*gin.Context)"

	if len(config.ParseDirs) > 0 {
		p := New(config.SwaggerOptions...)

		for _, path := range config.ParseDirs {
			packageDir, err := getPkgName(path)
			if err != nil {
				log.Printf("warning: failed to get package name in dir: %s, error: %s", path, err.Error())
			}

			err = p.getAllGoFileInfo(packageDir, path)
			if err != nil {
				return err
			}

			packages, err := parser.ParseDir(&fileSet, path, nil, parser.ParseComments)
			if err != nil {
				return err
			}

			// iterate over all packages in the directory
			for _, pkg := range packages {
				// iterate over all files within the package
				for name, astTree := range pkg.Files {
					baseName := filepath.Base(name)

					fileAST, err := ParseFileAST(baseName, astTree, fileSet, GetGinRouteInfos(app), config.Filter, config.PrintGenerate)
					if err != nil {
						return err
					}

					if fileAST != nil {
						if err := p.GinSwagger(path, baseName, fileAST.source); err != nil {
							return err
						}
					}

					// if err := p.GinSwagger(path, baseName, astTree); err != nil {
					// 	return err
					// }

				}
			}
		}

		var (
			bytes []byte
			err   error
		)
		if config.FormatSwaggerJSON {
			bytes, err = json.MarshalIndent(p.GetSwagger(), "", "  ")
			if err != nil {
				return err
			}
		} else {
			bytes, err = p.GetSwagger().MarshalJSON()
			if err != nil {
				return err
			}
		}

		if err := os.WriteFile(filepath.Join(config.OutputDir, "swagger.json"), bytes, os.ModePerm); err != nil {
			return err
		}
	}

	return nil
}

func ParseFileAST(name string, tree *ast.File, fileSet token.FileSet, routeInfos map[string]gin.RouteInfo, filterStr string, printGenerate bool) (*File, error) {

	config := types.Config{
		Importer: importer.ForCompiler(&fileSet, "source", nil),
	}

	info := types.Info{
		// 表达式对应的类型
		Types: make(map[ast.Expr]types.TypeAndValue),
		// 被定义的标示符
		Defs: make(map[*ast.Ident]types.Object),
		// 被使用的标示符
		Uses: make(map[*ast.Ident]types.Object),
		// 选择器,只能针对类型/对象.字段/method的选择，package.API这种不会记录在这里
		Selections: make(map[*ast.SelectorExpr]*types.Selection),
	}

	if _, err := config.Check("", &fileSet, []*ast.File{tree}, &info); err != nil {
		return nil, err
	}

	functionDescs := []FunctionDesc{}

	fileComments := []*ast.CommentGroup{}

	for _, declaration := range tree.Decls {

		switch decValue := declaration.(type) {
		case *ast.FuncDecl:
			expr, err := parser.ParseExpr(filterStr)
			if err != nil {
				log.Println(err)
				continue
			}

			except := strings.ReplaceAll(ExprString(expr), " ", "")
			act := strings.ReplaceAll(ExprString(decValue.Type), " ", "")
			if except != act {
				continue
			}

			log.Printf("[INFO] match %s named %s at line %s", except, decValue.Name.Name, fileSet.Position(decValue.Pos()))
			functionDesc := FunctionDesc{
				source: decValue,
				fset:   fileSet,

				Name:        decValue.Name.Name,
				Comments:    make([]string, 0),
				PackageName: fmt.Sprintf("%s.%s", tree.Name.Name, decValue.Name.Name),
				Params:      parseFuncItemInfo(decValue.Type.Params, info),
				Results:     parseFuncItemInfo(decValue.Type.Results, info),
				Vars:        make(map[string]FuncItem),
				Exprs:       make([]ExprItem, 0),
			}

			if decValue.Doc != nil && decValue.Doc.List != nil {
				for _, comment := range decValue.Doc.List {
					functionDesc.Comments = append(functionDesc.Comments, comment.Text)
				}
			}

			if decValue.Recv != nil && decValue.Recv.List != nil {
				recv := decValue.Recv.List[0]
				functionDesc.PackageName = fmt.Sprintf("%s.%s.%s", tree.Name.Name, strings.TrimPrefix(ExprString(recv.Type), "*"), decValue.Name.Name)
			}

			ast.Inspect(decValue.Body, func(n ast.Node) bool {
				switch node := n.(type) {
				// 获取函数体变量
				case *ast.Ident:
					if info.Defs[node] != nil {
						functionDesc.Vars[node.Name] = FuncItem{
							Name: node.Name,
							Type: info.Defs[node].Type().String(),
						}
					}
				// 获取函数内函数调用
				case *ast.CallExpr:
					selector, ok := node.Fun.(*ast.SelectorExpr)
					if !ok {
						return true
					}

					selectorType, exist := info.Selections[selector]
					if !exist {
						return true
					}

					if selectorType.Kind() != types.MethodVal {
						return true
					}

					args := make([]ExprArgItem, 0)

					for _, argEntry := range node.Args {
						argType, exist := info.Types[argEntry]
						if !exist {
							continue
						}

						var value string
						if argType.Value != nil {
							value = argType.Value.ExactString()
						}

						args = append(args, ExprArgItem{
							Type:  argType.Type.String(),
							Name:  ExprString(argEntry),
							Value: value,
						})
					}

					functionDesc.Exprs = append(functionDesc.Exprs, ExprItem{
						Receiver: selectorType.Recv().String(),
						Name:     selectorType.Obj().Name(),
						Args:     args,
					})

				}

				return true
			})

			functionDescs = append(functionDescs, functionDesc)

			comments := GetGinComments(functionDesc, routeInfos)
			commentMap := &ast.CommentGroup{List: make([]*ast.Comment, len(comments))}
			for index, comment := range comments {
				commentMap.List[index] = &ast.Comment{
					Text: comment,
				}
			}

			decValue.Doc = commentMap

			fileComments = append(fileComments, commentMap)

			if printGenerate {
				printer.Fprint(os.Stdout, &fileSet, decValue)
				fmt.Println()
			}
		default:
			// fmt.Printf("(AST: %T) Skiping\n", decValue)
		}

	}
	tree.Comments = append(tree.Comments, fileComments...)
	file := NewFile(name, tree)
	file.Functions = functionDescs

	return file, nil
}

func parseFuncItemInfo(node *ast.FieldList, info types.Info) []FuncItem {
	items := []FuncItem{}

	if node == nil || node.List == nil {
		return items
	}

	for _, field := range node.List {
		for _, nameEntry := range field.Names {
			value, exist := info.Types[field.Type]
			if !exist {
				continue
			}

			items = append(items, FuncItem{
				Name: nameEntry.Name,
				Type: value.Type.String(),
			})
		}
	}

	return items
}

func GetGinRouteInfos(app *gin.Engine) map[string]gin.RouteInfo {
	routes := make(map[string]gin.RouteInfo)
	for _, info := range app.Routes() {
		routes[info.Handler] = info
	}
	return routes
}
