package parser

import (
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/build"
	goparser "go/parser"
	"go/token"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/KyleBanks/depth"
	"github.com/go-openapi/spec"
)

const (
	// CamelCase indicates using CamelCase strategy for struct field.
	CamelCase = "camelcase"

	// PascalCase indicates using PascalCase strategy for struct field.
	PascalCase = "pascalcase"

	// SnakeCase indicates using SnakeCase strategy for struct field.
	SnakeCase = "snakecase"

	acceptAttr      = "@accept"
	produceAttr     = "@produce"
	scopeAttrPrefix = "@scope."
)

var (
	// ErrRecursiveParseStruct recursively parsing struct.
	ErrRecursiveParseStruct = errors.New("recursively parsing struct")

	// ErrFuncTypeField field type is func.
	ErrFuncTypeField = errors.New("field type is func")

	// ErrFailedConvertPrimitiveType Failed to convert for swag to interpretable type.
	ErrFailedConvertPrimitiveType = errors.New("swag property: failed convert primitive type")
)

// Parser implements a parser for Go source files.
type Parser struct {
	// swagger represents the root document object for the API specification
	swagger *spec.Swagger

	// packages store entities of APIs, definitions, file, package path etc.  and their relations
	packages *PackagesDefinitions

	// parsedSchemas store schemas which have been parsed from ast.TypeSpec
	parsedSchemas map[*TypeSpecDef]*Schema

	// outputSchemas store schemas which will be export to swagger
	outputSchemas map[*TypeSpecDef]*Schema

	// existSchemaNames store names of models for conflict determination
	existSchemaNames map[string]*Schema

	// toBeRenamedSchemas names of models to be renamed
	toBeRenamedSchemas map[string]string

	// toBeRenamedSchemas URLs of ref models to be renamed
	toBeRenamedRefURLs []*url.URL

	// PropNamingStrategy naming strategy
	PropNamingStrategy string

	// ParseVendor parse vendor folder
	ParseVendor bool

	// ParseDependencies whether swag should be parse outside dependency folder
	ParseDependency bool

	// ParseInternal whether swag should parse internal packages
	ParseInternal bool

	// Strict whether swag should error or warn when it detects cases which are most likely user errors
	Strict bool

	// structStack stores full names of the structures that were already parsed or are being parsed now
	structStack []*TypeSpecDef

	// markdownFileDir holds the path to the folder, where markdown files are stored
	markdownFileDir string

	// codeExampleFilesDir holds path to the folder, where code example files are stored
	codeExampleFilesDir string

	// collectionFormatInQuery set the default collectionFormat otherwise then 'csv' for array in query params
	collectionFormatInQuery string

	// excludes excludes dirs and files in SearchDir
	excludes map[string]bool

	// debugging output goes here
	debug Debugger
}

// Debugger is the interface that wraps the basic Printf method.
type Debugger interface {
	Printf(format string, v ...interface{})
}

// New creates a new Parser with default properties.
func New(options ...func(*Parser)) *Parser {
	// parser.swagger.SecurityDefinitions =

	parser := &Parser{
		swagger: &spec.Swagger{
			SwaggerProps: spec.SwaggerProps{
				Info: &spec.Info{
					InfoProps: spec.InfoProps{
						Contact: &spec.ContactInfo{},
						License: nil,
					},
					VendorExtensible: spec.VendorExtensible{
						Extensions: spec.Extensions{},
					},
				},
				Paths: &spec.Paths{
					Paths: make(map[string]spec.PathItem),
				},
				Definitions:         make(map[string]spec.Schema),
				SecurityDefinitions: make(map[string]*spec.SecurityScheme),
			},
		},
		packages:           NewPackagesDefinitions(),
		debug:              log.New(os.Stdout, "", log.LstdFlags),
		parsedSchemas:      make(map[*TypeSpecDef]*Schema),
		outputSchemas:      make(map[*TypeSpecDef]*Schema),
		existSchemaNames:   make(map[string]*Schema),
		toBeRenamedSchemas: make(map[string]string),
		excludes:           make(map[string]bool),
	}

	for _, option := range options {
		option(parser)
	}

	return parser
}

// SetMarkdownFileDirectory sets the directory to search for markdown files.
func SetMarkdownFileDirectory(directoryPath string) func(*Parser) {
	return func(p *Parser) {
		p.markdownFileDir = directoryPath
	}
}

// SetCodeExamplesDirectory sets the directory to search for code example files.
func SetCodeExamplesDirectory(directoryPath string) func(*Parser) {
	return func(p *Parser) {
		p.codeExampleFilesDir = directoryPath
	}
}

// SetExcludedDirsAndFiles sets directories and files to be excluded when searching.
func SetExcludedDirsAndFiles(excludes string) func(*Parser) {
	return func(p *Parser) {
		for _, f := range strings.Split(excludes, ",") {
			f = strings.TrimSpace(f)
			if f != "" {
				f = filepath.Clean(f)
				p.excludes[f] = true
			}
		}
	}
}

// SetStrict sets whether swag should error or warn when it detects cases which are most likely user errors.
func SetStrict(strict bool) func(*Parser) {
	return func(p *Parser) {
		p.Strict = strict
	}
}

// SetDebugger allows the use of user-defined implementations.
func SetDebugger(logger Debugger) func(parser *Parser) {
	return func(p *Parser) {
		p.debug = logger
	}
}

func getPkgName(searchDir string) (string, error) {
	cmd := exec.Command("go", "list", "-f={{.ImportPath}}")
	cmd.Dir = searchDir
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("execute go list command, %s, stdout:%s, stderr:%s", err, stdout.String(), stderr.String())
	}

	outStr, _ := stdout.String(), stderr.String()

	if outStr[0] == '_' { // will shown like _/{GOPATH}/src/{YOUR_PACKAGE} when NOT enable GO MODULE.
		outStr = strings.TrimPrefix(outStr, "_"+build.Default.GOPATH+"/src/")
	}
	f := strings.Split(outStr, "\n")
	outStr = f[0]

	return outStr, nil
}

func initIfEmpty(license *spec.License) *spec.License {
	if license == nil {
		return new(spec.License)
	}

	return license
}

// ParseGeneralAPIInfo parses general api info for given mainAPIFile path.
func (parser *Parser) ParseGeneralAPIInfo(mainAPIFile string) error {
	fileTree, err := goparser.ParseFile(token.NewFileSet(), mainAPIFile, nil, goparser.ParseComments)
	if err != nil {
		return fmt.Errorf("cannot parse source files %s: %s", mainAPIFile, err)
	}

	parser.swagger.Swagger = "2.0"

	for _, comment := range fileTree.Comments {
		comments := strings.Split(comment.Text(), "\n")
		if !isGeneralAPIComment(comments) {
			continue
		}
		err := parseGeneralAPIInfo(parser, comments)
		if err != nil {
			return err
		}
	}

	return nil
}

func (parser *Parser) GinSwagger(dir string, fileName string, fileTree *ast.File) error {
	var err error
	err = parser.packages.CollectAstFile(dir, fileName, fileTree)
	if err != nil {
		return err
	}
	parser.swagger.Swagger = "2.0"

	parser.parsedSchemas, err = parser.packages.ParseTypes()
	if err != nil {
		return err
	}

	for _, astDescription := range fileTree.Decls {
		astDeclaration, ok := astDescription.(*ast.FuncDecl)
		if ok && astDeclaration.Doc != nil && astDeclaration.Doc.List != nil {
			// for per 'function' comment, create a new 'Operation' object
			operation := NewOperation(parser, SetCodeExampleFilesDirectory(parser.codeExampleFilesDir))
			for _, comment := range astDeclaration.Doc.List {
				err := operation.ParseComment(comment.Text, fileTree)
				if err != nil {
					return fmt.Errorf("ParseComment error in file %s :%+v", fileName, err)
				}
			}

			for _, routeProperties := range operation.RouterProperties {
				var pathItem spec.PathItem
				var ok bool

				pathItem, ok = parser.swagger.Paths.Paths[routeProperties.Path]
				if !ok {
					pathItem = spec.PathItem{}
				}

				// check if we already have a operation for this path and method
				if hasRouteMethodOp(pathItem, routeProperties.HTTPMethod) {
					err := fmt.Errorf("route %s %s is declared multiple times", routeProperties.HTTPMethod, routeProperties.Path)
					if parser.Strict {
						return err
					}
					parser.debug.Printf("warning: %s\n", err)
				}

				setRouteMethodOp(&pathItem, routeProperties.HTTPMethod, &operation.Operation)

				parser.swagger.Paths.Paths[routeProperties.Path] = pathItem
			}
		}
	}

	parser.renameRefSchemas()

	return parser.checkOperationIDUniqueness()
}

func parseGeneralAPIInfo(parser *Parser, comments []string) error {
	previousAttribute := ""

	// parsing classic meta data model
	for i, commentLine := range comments {
		attribute := strings.Split(commentLine, " ")[0]
		value := strings.TrimSpace(commentLine[len(attribute):])
		multilineBlock := false
		if previousAttribute == attribute {
			multilineBlock = true
		}
		switch strings.ToLower(attribute) {
		case "@version":
			parser.swagger.Info.Version = value
		case "@title":
			parser.swagger.Info.Title = value
		case "@description":
			if multilineBlock {
				parser.swagger.Info.Description += "\n" + value

				continue
			}
			parser.swagger.Info.Description = value
		case "@description.markdown":
			commentInfo, err := getMarkdownForTag("api", parser.markdownFileDir)
			if err != nil {
				return err
			}
			parser.swagger.Info.Description = string(commentInfo)
		case "@termsofservice":
			parser.swagger.Info.TermsOfService = value
		case "@contact.name":
			parser.swagger.Info.Contact.Name = value
		case "@contact.email":
			parser.swagger.Info.Contact.Email = value
		case "@contact.url":
			parser.swagger.Info.Contact.URL = value
		case "@license.name":
			parser.swagger.Info.License = initIfEmpty(parser.swagger.Info.License)
			parser.swagger.Info.License.Name = value
		case "@license.url":
			parser.swagger.Info.License = initIfEmpty(parser.swagger.Info.License)
			parser.swagger.Info.License.URL = value
		case "@host":
			parser.swagger.Host = value
		case "@basepath":
			parser.swagger.BasePath = value
		case acceptAttr:
			err := parser.ParseAcceptComment(value)
			if err != nil {
				return err
			}
		case produceAttr:
			err := parser.ParseProduceComment(value)
			if err != nil {
				return err
			}
		case "@schemes":
			parser.swagger.Schemes = getSchemes(commentLine)
		case "@tag.name":
			parser.swagger.Tags = append(parser.swagger.Tags, spec.Tag{
				TagProps: spec.TagProps{
					Name: value,
				},
			})
		case "@tag.description":
			tag := parser.swagger.Tags[len(parser.swagger.Tags)-1]
			tag.TagProps.Description = value
			replaceLastTag(parser.swagger.Tags, tag)
		case "@tag.description.markdown":
			tag := parser.swagger.Tags[len(parser.swagger.Tags)-1]
			commentInfo, err := getMarkdownForTag(tag.TagProps.Name, parser.markdownFileDir)
			if err != nil {
				return err
			}
			tag.TagProps.Description = string(commentInfo)
			replaceLastTag(parser.swagger.Tags, tag)
		case "@tag.docs.url":
			tag := parser.swagger.Tags[len(parser.swagger.Tags)-1]
			tag.TagProps.ExternalDocs = &spec.ExternalDocumentation{
				URL: value,
			}
			replaceLastTag(parser.swagger.Tags, tag)
		case "@tag.docs.description":
			tag := parser.swagger.Tags[len(parser.swagger.Tags)-1]
			if tag.TagProps.ExternalDocs == nil {
				return fmt.Errorf("%s needs to come after a @tags.docs.url", attribute)
			}
			tag.TagProps.ExternalDocs.Description = value
			replaceLastTag(parser.swagger.Tags, tag)
		case "@securitydefinitions.basic":
			parser.swagger.SecurityDefinitions[value] = spec.BasicAuth()
		case "@securitydefinitions.apikey":
			attrMap, _, _, err := parseSecAttr(attribute, []string{"@in", "@name"}, comments[i+1:])
			if err != nil {
				return err
			}
			parser.swagger.SecurityDefinitions[value] = spec.APIKeyAuth(attrMap["@name"], attrMap["@in"])
		case "@securitydefinitions.oauth2.application":
			attrMap, scopes, extensions, err := parseSecAttr(attribute, []string{"@tokenurl"}, comments[i+1:])
			if err != nil {
				return err
			}
			parser.swagger.SecurityDefinitions[value] = secOAuth2Application(attrMap["@tokenurl"], scopes, extensions)
		case "@securitydefinitions.oauth2.implicit":
			attrs, scopes, ext, err := parseSecAttr(attribute, []string{"@authorizationurl"}, comments[i+1:])
			if err != nil {
				return err
			}
			parser.swagger.SecurityDefinitions[value] = secOAuth2Implicit(attrs["@authorizationurl"], scopes, ext)
		case "@securitydefinitions.oauth2.password":
			attrs, scopes, ext, err := parseSecAttr(attribute, []string{"@tokenurl"}, comments[i+1:])
			if err != nil {
				return err
			}
			parser.swagger.SecurityDefinitions[value] = secOAuth2Password(attrs["@tokenurl"], scopes, ext)
		case "@securitydefinitions.oauth2.accesscode":
			attrs, scopes, ext, err := parseSecAttr(attribute, []string{"@tokenurl", "@authorizationurl"}, comments[i+1:])
			if err != nil {
				return err
			}
			parser.swagger.SecurityDefinitions[value] = secOAuth2AccessToken(attrs["@authorizationurl"], attrs["@tokenurl"], scopes, ext)
		case "@query.collection.format":
			parser.collectionFormatInQuery = value
		default:
			prefixExtension := "@x-"
			// Prefix extension + 1 char + 1 space  + 1 char
			if len(attribute) > 5 && attribute[:len(prefixExtension)] == prefixExtension {
				extExistsInSecurityDef := false
				// for each security definition
				for _, v := range parser.swagger.SecurityDefinitions {
					// check if extension exists
					_, extExistsInSecurityDef = v.VendorExtensible.Extensions.GetString(attribute[1:])
					// if it exists in at least one, then we stop iterating
					if extExistsInSecurityDef {
						break
					}
				}
				// if it is present on security def, don't add it again
				if extExistsInSecurityDef {
					break
				}

				var valueJSON interface{}
				split := strings.SplitAfter(commentLine, attribute+" ")
				if len(split) < 2 {
					return fmt.Errorf("annotation %s need a value", attribute)
				}
				extensionName := "x-" + strings.SplitAfter(attribute, prefixExtension)[1]
				err := json.Unmarshal([]byte(split[1]), &valueJSON)
				if err != nil {
					return fmt.Errorf("annotation %s need a valid json value", attribute)
				}

				if strings.Contains(extensionName, "logo") {
					parser.swagger.Info.Extensions.Add(extensionName, valueJSON)
				} else {
					if parser.swagger.Extensions == nil {
						parser.swagger.Extensions = make(map[string]interface{})
					}
					parser.swagger.Extensions[attribute[1:]] = valueJSON
				}
			}
		}
		previousAttribute = attribute
	}

	return nil
}

// ParseAcceptComment parses comment for given `accept` comment string.
func (parser *Parser) ParseAcceptComment(commentLine string) error {
	return parseMimeTypeList(commentLine, &parser.swagger.Consumes, "%v accept type can't be accepted")
}

// ParseProduceComment parses comment for given `produce` comment string.
func (parser *Parser) ParseProduceComment(commentLine string) error {
	return parseMimeTypeList(commentLine, &parser.swagger.Produces, "%v produce type can't be accepted")
}

func isGeneralAPIComment(comments []string) bool {
	for _, commentLine := range comments {
		attribute := strings.ToLower(strings.Split(commentLine, " ")[0])
		switch attribute {
		// The @summary, @router, @success,@failure  annotation belongs to Operation
		case "@summary", "@router", "@success", "@failure", "@response":
			return false
		}
	}

	return true
}

func parseSecAttr(context string, search []string, lines []string) (map[string]string, map[string]string, map[string]interface{}, error) {
	attrMap := map[string]string{}
	scopes := map[string]string{}
	extensions := map[string]interface{}{}
	for _, v := range lines {
		securityAttr := strings.ToLower(strings.Split(v, " ")[0])
		for _, findterm := range search {
			if securityAttr == findterm {
				attrMap[securityAttr] = strings.TrimSpace(v[len(securityAttr):])

				continue
			}
		}
		isExists, err := isExistsScope(securityAttr)
		if err != nil {
			return nil, nil, nil, err
		}
		if isExists {
			scopes[securityAttr[len(scopeAttrPrefix):]] = v[len(securityAttr):]
		}
		if strings.HasPrefix(securityAttr, "@x-") {
			// Add the custom attribute without the @
			extensions[securityAttr[1:]] = strings.TrimSpace(v[len(securityAttr):])
		}
		// next securityDefinitions
		if strings.Index(securityAttr, "@securitydefinitions.") == 0 {
			break
		}
	}

	if len(attrMap) != len(search) {
		return nil, nil, nil, fmt.Errorf("%s is %v required", context, search)
	}

	return attrMap, scopes, extensions, nil
}

func secOAuth2Application(tokenURL string, scopes map[string]string,
	extensions map[string]interface{}) *spec.SecurityScheme {
	securityScheme := spec.OAuth2Application(tokenURL)
	securityScheme.VendorExtensible.Extensions = handleSecuritySchemaExtensions(extensions)
	for scope, description := range scopes {
		securityScheme.AddScope(scope, description)
	}

	return securityScheme
}

func secOAuth2Implicit(authorizationURL string, scopes map[string]string,
	extensions map[string]interface{}) *spec.SecurityScheme {
	securityScheme := spec.OAuth2Implicit(authorizationURL)
	securityScheme.VendorExtensible.Extensions = handleSecuritySchemaExtensions(extensions)
	for scope, description := range scopes {
		securityScheme.AddScope(scope, description)
	}

	return securityScheme
}

func secOAuth2Password(tokenURL string, scopes map[string]string,
	extensions map[string]interface{}) *spec.SecurityScheme {
	securityScheme := spec.OAuth2Password(tokenURL)
	securityScheme.VendorExtensible.Extensions = handleSecuritySchemaExtensions(extensions)
	for scope, description := range scopes {
		securityScheme.AddScope(scope, description)
	}

	return securityScheme
}

func secOAuth2AccessToken(authorizationURL, tokenURL string,
	scopes map[string]string, extensions map[string]interface{}) *spec.SecurityScheme {
	securityScheme := spec.OAuth2AccessToken(authorizationURL, tokenURL)
	securityScheme.VendorExtensible.Extensions = handleSecuritySchemaExtensions(extensions)
	for scope, description := range scopes {
		securityScheme.AddScope(scope, description)
	}

	return securityScheme
}

func handleSecuritySchemaExtensions(providedExtensions map[string]interface{}) spec.Extensions {
	var extensions spec.Extensions
	if len(providedExtensions) > 0 {
		extensions = make(map[string]interface{}, len(providedExtensions))
		for key, value := range providedExtensions {
			extensions[key] = value
		}
	}

	return extensions
}

func getMarkdownForTag(tagName string, dirPath string) ([]byte, error) {
	filesInfos, err := ioutil.ReadDir(dirPath)
	if err != nil {
		return nil, err
	}

	for _, fileInfo := range filesInfos {
		if fileInfo.IsDir() {
			continue
		}
		fileName := fileInfo.Name()

		if !strings.Contains(fileName, ".md") {
			continue
		}

		if strings.Contains(fileName, tagName) {
			fullPath := filepath.Join(dirPath, fileName)
			commentInfo, err := ioutil.ReadFile(fullPath)
			if err != nil {
				return nil, fmt.Errorf("Failed to read markdown file %s error: %s ", fullPath, err)
			}

			return commentInfo, nil
		}
	}

	return nil, fmt.Errorf("Unable to find markdown file for tag %s in the given directory", tagName)
}

func isExistsScope(scope string) (bool, error) {
	s := strings.Fields(scope)
	for _, v := range s {
		if strings.Contains(v, scopeAttrPrefix) {
			if strings.Contains(v, ",") {
				return false, fmt.Errorf("@scope can't use comma(,) get=" + v)
			}
		}
	}

	return strings.Contains(scope, scopeAttrPrefix), nil
}

// getSchemes parses swagger schemes for given commentLine.
func getSchemes(commentLine string) []string {
	attribute := strings.ToLower(strings.Split(commentLine, " ")[0])

	return strings.Split(strings.TrimSpace(commentLine[len(attribute):]), " ")
}

// ParseRouterAPIInfo parses router api info for given astFile.
func (parser *Parser) ParseRouterAPIInfo(fileName string, astFile *ast.File) error {
	for _, astDescription := range astFile.Decls {
		astDeclaration, ok := astDescription.(*ast.FuncDecl)
		if ok && astDeclaration.Doc != nil && astDeclaration.Doc.List != nil {
			// for per 'function' comment, create a new 'Operation' object
			operation := NewOperation(parser, SetCodeExampleFilesDirectory(parser.codeExampleFilesDir))
			for _, comment := range astDeclaration.Doc.List {
				err := operation.ParseComment(comment.Text, astFile)
				if err != nil {
					return fmt.Errorf("ParseComment error in file %s :%+v", fileName, err)
				}
			}

			for _, routeProperties := range operation.RouterProperties {
				var pathItem spec.PathItem
				var ok bool

				pathItem, ok = parser.swagger.Paths.Paths[routeProperties.Path]
				if !ok {
					pathItem = spec.PathItem{}
				}

				// check if we already have a operation for this path and method
				if hasRouteMethodOp(pathItem, routeProperties.HTTPMethod) {
					err := fmt.Errorf("route %s %s is declared multiple times", routeProperties.HTTPMethod, routeProperties.Path)
					if parser.Strict {
						return err
					}
					parser.debug.Printf("warning: %s\n", err)
				}

				setRouteMethodOp(&pathItem, routeProperties.HTTPMethod, &operation.Operation)

				parser.swagger.Paths.Paths[routeProperties.Path] = pathItem
			}
		}
	}

	return nil
}

func setRouteMethodOp(pathItem *spec.PathItem, method string, op *spec.Operation) {
	switch strings.ToUpper(method) {
	case http.MethodGet:
		pathItem.Get = op
	case http.MethodPost:
		pathItem.Post = op
	case http.MethodDelete:
		pathItem.Delete = op
	case http.MethodPut:
		pathItem.Put = op
	case http.MethodPatch:
		pathItem.Patch = op
	case http.MethodHead:
		pathItem.Head = op
	case http.MethodOptions:
		pathItem.Options = op
	}
}

func hasRouteMethodOp(pathItem spec.PathItem, method string) bool {
	switch strings.ToUpper(method) {
	case http.MethodGet:
		return pathItem.Get != nil
	case http.MethodPost:
		return pathItem.Post != nil
	case http.MethodDelete:
		return pathItem.Delete != nil
	case http.MethodPut:
		return pathItem.Put != nil
	case http.MethodPatch:
		return pathItem.Patch != nil
	case http.MethodHead:
		return pathItem.Head != nil
	case http.MethodOptions:
		return pathItem.Options != nil
	}

	return false
}

func convertFromSpecificToPrimitive(typeName string) (string, error) {
	name := typeName
	if strings.ContainsRune(name, '.') {
		name = strings.Split(name, ".")[1]
	}
	switch strings.ToUpper(name) {
	case "TIME", "OBJECTID", "UUID":
		return STRING, nil
	case "DECIMAL":
		return NUMBER, nil
	}

	return typeName, ErrFailedConvertPrimitiveType
}

func (parser *Parser) getTypeSchema(typeName string, file *ast.File, ref bool) (*spec.Schema, error) {
	if IsGolangPrimitiveType(typeName) {
		return PrimitiveSchema(TransToValidSchemeType(typeName)), nil
	}

	schemaType, err := convertFromSpecificToPrimitive(typeName)
	if err == nil {
		return PrimitiveSchema(schemaType), nil
	}

	typeSpecDef := parser.packages.FindTypeSpec(typeName, file, parser.ParseDependency)
	if typeSpecDef == nil {
		return nil, fmt.Errorf("cannot find type definition: %s", typeName)
	}

	schema, ok := parser.parsedSchemas[typeSpecDef]
	if !ok {
		var err error
		schema, err = parser.ParseDefinition(typeSpecDef)
		if err != nil {
			if err == ErrRecursiveParseStruct && ref {
				return parser.getRefTypeSchema(typeSpecDef, schema), nil
			}

			return nil, err
		}
	}

	if ref && len(schema.Schema.Type) > 0 && schema.Schema.Type[0] == OBJECT {
		return parser.getRefTypeSchema(typeSpecDef, schema), nil
	}

	return schema.Schema, nil
}

func (parser *Parser) renameRefSchemas() {
	if len(parser.toBeRenamedSchemas) == 0 {
		return
	}

	// rename schemas in swagger.Definitions
	for name, pkgPath := range parser.toBeRenamedSchemas {
		if schema, ok := parser.swagger.Definitions[name]; ok {
			delete(parser.swagger.Definitions, name)
			name = parser.renameSchema(name, pkgPath)
			parser.swagger.Definitions[name] = schema
		}
	}

	// rename URLs if match
	for _, refURL := range parser.toBeRenamedRefURLs {
		parts := strings.Split(refURL.Fragment, "/")
		name := parts[len(parts)-1]
		if pkgPath, ok := parser.toBeRenamedSchemas[name]; ok {
			parts[len(parts)-1] = parser.renameSchema(name, pkgPath)
			refURL.Fragment = strings.Join(parts, "/")
		}
	}
}

func (parser *Parser) renameSchema(name, pkgPath string) string {
	parts := strings.Split(name, ".")
	name = fullTypeName(pkgPath, parts[len(parts)-1])
	name = strings.ReplaceAll(name, "/", "_")

	return name
}

func (parser *Parser) getRefTypeSchema(typeSpecDef *TypeSpecDef, schema *Schema) *spec.Schema {
	_, ok := parser.outputSchemas[typeSpecDef]
	if !ok {
		existSchema, ok := parser.existSchemaNames[schema.Name]
		if ok {
			// store the first one to be renamed after parsing over
			_, ok = parser.toBeRenamedSchemas[existSchema.Name]
			if !ok {
				parser.toBeRenamedSchemas[existSchema.Name] = existSchema.PkgPath
			}
			// rename not the first one
			schema.Name = parser.renameSchema(schema.Name, schema.PkgPath)
		} else {
			parser.existSchemaNames[schema.Name] = schema
		}
		parser.swagger.Definitions[schema.Name] = spec.Schema{}

		if schema.Schema != nil {
			parser.swagger.Definitions[schema.Name] = *schema.Schema
		}

		parser.outputSchemas[typeSpecDef] = schema
	}

	refSchema := RefSchema(schema.Name)
	// store every URL
	parser.toBeRenamedRefURLs = append(parser.toBeRenamedRefURLs, refSchema.Ref.Ref.GetURL())

	return refSchema
}

func (parser *Parser) isInStructStack(typeSpecDef *TypeSpecDef) bool {
	for _, specDef := range parser.structStack {
		if typeSpecDef == specDef {
			return true
		}
	}

	return false
}

// ParseDefinition parses given type spec that corresponds to the type under
// given name and package, and populates swagger schema definitions registry
// with a schema for the given type
func (parser *Parser) ParseDefinition(typeSpecDef *TypeSpecDef) (*Schema, error) {
	typeName := typeSpecDef.FullName()
	refTypeName := TypeDocName(typeName, typeSpecDef.TypeSpec)

	schema, ok := parser.parsedSchemas[typeSpecDef]
	if ok {
		parser.debug.Printf("Skipping '%s', already parsed.", typeName)

		return schema, nil
	}

	if parser.isInStructStack(typeSpecDef) {
		parser.debug.Printf("Skipping '%s', recursion detected.", typeName)

		return &Schema{
				Name:    refTypeName,
				PkgPath: typeSpecDef.PkgPath,
				Schema:  PrimitiveSchema(OBJECT),
			},
			ErrRecursiveParseStruct
	}
	parser.structStack = append(parser.structStack, typeSpecDef)

	parser.debug.Printf("Generating %s", typeName)

	definition, err := parser.parseTypeExpr(typeSpecDef.File, typeSpecDef.TypeSpec.Type, false)
	if err != nil {
		return nil, err
	}

	s := Schema{
		Name:    refTypeName,
		PkgPath: typeSpecDef.PkgPath,
		Schema:  definition,
	}
	parser.parsedSchemas[typeSpecDef] = &s

	// update an empty schema as a result of recursion
	s2, ok := parser.outputSchemas[typeSpecDef]
	if ok {
		parser.swagger.Definitions[s2.Name] = *definition
	}

	return &s, nil
}

func fullTypeName(pkgName, typeName string) string {
	if pkgName != "" {
		return pkgName + "." + typeName
	}

	return typeName
}

// parseTypeExpr parses given type expression that corresponds to the type under
// given name and package, and returns swagger schema for it.
func (parser *Parser) parseTypeExpr(file *ast.File, typeExpr ast.Expr, ref bool) (*spec.Schema, error) {
	switch expr := typeExpr.(type) {
	// type Foo interface{}
	case *ast.InterfaceType:
		return &spec.Schema{}, nil

	// type Foo struct {...}
	case *ast.StructType:
		return parser.parseStruct(file, expr.Fields)

	// type Foo Baz
	case *ast.Ident:
		return parser.getTypeSchema(expr.Name, file, ref)

	// type Foo *Baz
	case *ast.StarExpr:
		return parser.parseTypeExpr(file, expr.X, ref)

	// type Foo pkg.Bar
	case *ast.SelectorExpr:
		if xIdent, ok := expr.X.(*ast.Ident); ok {
			return parser.getTypeSchema(fullTypeName(xIdent.Name, expr.Sel.Name), file, ref)
		}
	// type Foo []Baz
	case *ast.ArrayType:
		itemSchema, err := parser.parseTypeExpr(file, expr.Elt, true)
		if err != nil {
			return nil, err
		}

		return spec.ArrayProperty(itemSchema), nil
	// type Foo map[string]Bar
	case *ast.MapType:
		if _, ok := expr.Value.(*ast.InterfaceType); ok {
			return spec.MapProperty(nil), nil
		}
		schema, err := parser.parseTypeExpr(file, expr.Value, true)
		if err != nil {
			return nil, err
		}

		return spec.MapProperty(schema), nil

	case *ast.FuncType:
		return nil, ErrFuncTypeField
	// ...
	default:
		parser.debug.Printf("Type definition of type '%T' is not supported yet. Using 'object' instead.\n", typeExpr)
	}

	return PrimitiveSchema(OBJECT), nil
}

func (parser *Parser) parseStruct(file *ast.File, fields *ast.FieldList) (*spec.Schema, error) {
	required := make([]string, 0)
	properties := make(map[string]spec.Schema)
	for _, field := range fields.List {
		fieldProps, requiredFromAnon, err := parser.parseStructField(file, field)
		if err != nil {
			if err == ErrFuncTypeField {
				continue
			}

			return nil, err
		}
		if len(fieldProps) == 0 {
			continue
		}
		required = append(required, requiredFromAnon...)
		for k, v := range fieldProps {
			properties[k] = v
		}
	}

	sort.Strings(required)

	return &spec.Schema{
		SchemaProps: spec.SchemaProps{
			Type:       []string{OBJECT},
			Properties: properties,
			Required:   required,
		},
	}, nil
}

type structField struct {
	desc         string
	schemaType   string
	arrayType    string
	formatType   string
	isRequired   bool
	readOnly     bool
	exampleValue interface{}
	maximum      *float64
	minimum      *float64
	multipleOf   *float64
	maxLength    *int64
	minLength    *int64
	enums        []interface{}
	defaultValue interface{}
	extensions   map[string]interface{}
}

func (parser *Parser) parseStructField(file *ast.File, field *ast.Field) (map[string]spec.Schema, []string, error) {
	if field.Names == nil {
		if field.Tag != nil {
			skip, ok := reflect.StructTag(strings.ReplaceAll(field.Tag.Value, "`", "")).Lookup("swaggerignore")
			if ok && strings.EqualFold(skip, "true") {
				return nil, nil, nil
			}
		}

		typeName, err := getFieldType(field.Type)
		if err != nil {
			return nil, nil, err
		}
		schema, err := parser.getTypeSchema(typeName, file, false)
		if err != nil {
			return nil, nil, err
		}
		if len(schema.Type) > 0 && schema.Type[0] == OBJECT {
			if len(schema.Properties) == 0 {
				return nil, nil, nil
			}

			properties := map[string]spec.Schema{}
			for k, v := range schema.Properties {
				properties[k] = v
			}

			return properties, schema.SchemaProps.Required, nil
		}

		// for alias type of non-struct types ,such as array,map, etc. ignore field tag.
		return map[string]spec.Schema{typeName: *schema}, nil, nil
	}

	fieldName, schema, err := parser.getFieldName(field)
	if err != nil {
		return nil, nil, err
	}
	if fieldName == "" {
		return nil, nil, nil
	}
	if schema == nil {
		typeName, err := getFieldType(field.Type)
		if err == nil {
			// named type
			schema, err = parser.getTypeSchema(typeName, file, true)
		} else {
			// unnamed type
			schema, err = parser.parseTypeExpr(file, field.Type, false)
		}
		if err != nil {
			return nil, nil, err
		}
	}

	types := parser.GetSchemaTypePath(schema, 2)
	if len(types) == 0 {
		return nil, nil, fmt.Errorf("invalid type for field: %s", field.Names[0])
	}

	structField, err := parser.parseFieldTag(field, types)
	if err != nil {
		return nil, nil, err
	}

	if structField.schemaType == STRING && types[0] != structField.schemaType {
		schema = PrimitiveSchema(structField.schemaType)
	}

	schema.Description = structField.desc
	schema.ReadOnly = structField.readOnly
	schema.Default = structField.defaultValue
	schema.Example = structField.exampleValue
	if structField.schemaType != ARRAY {
		schema.Format = structField.formatType
	}
	schema.Extensions = structField.extensions
	eleSchema := schema
	if structField.schemaType == ARRAY {
		eleSchema = schema.Items.Schema
		eleSchema.Format = structField.formatType
	}
	eleSchema.Maximum = structField.maximum
	eleSchema.Minimum = structField.minimum
	eleSchema.MultipleOf = structField.multipleOf
	eleSchema.MaxLength = structField.maxLength
	eleSchema.MinLength = structField.minLength
	eleSchema.Enum = structField.enums

	var tagRequired []string
	if structField.isRequired {
		tagRequired = append(tagRequired, fieldName)
	}

	return map[string]spec.Schema{fieldName: *schema}, tagRequired, nil
}

func getFieldType(field ast.Expr) (string, error) {
	switch fieldType := field.(type) {
	case *ast.Ident:
		return fieldType.Name, nil
	case *ast.SelectorExpr:
		packageName, err := getFieldType(fieldType.X)
		if err != nil {
			return "", err
		}

		return fullTypeName(packageName, fieldType.Sel.Name), nil

	case *ast.StarExpr:
		fullName, err := getFieldType(fieldType.X)
		if err != nil {
			return "", err
		}

		return fullName, nil
	default:
		return "", fmt.Errorf("unknown field type %#v", field)
	}
}

func (parser *Parser) getFieldName(field *ast.Field) (name string, schema *spec.Schema, err error) {
	// Skip non-exported fields.
	if !ast.IsExported(field.Names[0].Name) {
		return "", nil, nil
	}

	if field.Tag != nil {
		// `json:"tag"` -> json:"tag"
		structTag := reflect.StructTag(strings.Replace(field.Tag.Value, "`", "", -1))
		ignoreTag := structTag.Get("swaggerignore")
		if strings.EqualFold(ignoreTag, "true") {
			return "", nil, nil
		}

		name = structTag.Get("json")
		// json:"tag,hoge"
		if name = strings.TrimSpace(strings.Split(name, ",")[0]); name == "-" {
			return "", nil, nil
		}

		typeTag := structTag.Get("swaggertype")
		if typeTag != "" {
			parts := strings.Split(typeTag, ",")
			schema, err = BuildCustomSchema(parts)
			if err != nil {
				return "", nil, err
			}
		}
	}

	if name == "" {
		switch parser.PropNamingStrategy {
		case SnakeCase:
			name = toSnakeCase(field.Names[0].Name)
		case PascalCase:
			name = field.Names[0].Name
		default:
			name = toLowerCamelCase(field.Names[0].Name)
		}
	}

	return name, schema, err
}

func (parser *Parser) parseFieldTag(field *ast.Field, types []string) (*structField, error) {
	structField := &structField{
		//    name:       field.Names[0].Name,
		schemaType: types[0],
	}
	if len(types) > 1 && (types[0] == ARRAY || types[0] == OBJECT) {
		structField.arrayType = types[1]
	}

	if field.Doc != nil {
		structField.desc = strings.TrimSpace(field.Doc.Text())
	}
	if structField.desc == "" && field.Comment != nil {
		structField.desc = strings.TrimSpace(field.Comment.Text())
	}

	if field.Tag == nil {
		return structField, nil
	}
	// `json:"tag"` -> json:"tag"
	structTag := reflect.StructTag(strings.Replace(field.Tag.Value, "`", "", -1))

	jsonTag := structTag.Get("json")
	// json:"name,string" or json:",string"

	exampleTag := structTag.Get("example")
	if exampleTag != "" {
		structField.exampleValue = exampleTag
		if !strings.Contains(jsonTag, ",string") {
			example, err := defineTypeOfExample(structField.schemaType, structField.arrayType, exampleTag)
			if err != nil {
				return nil, err
			}
			structField.exampleValue = example
		}
	}
	formatTag := structTag.Get("format")
	if formatTag != "" {
		structField.formatType = formatTag
	}
	bindingTag := structTag.Get("binding")
	if bindingTag != "" {
		for _, val := range strings.Split(bindingTag, ",") {
			if val == "required" {
				structField.isRequired = true

				break
			}
		}
	}
	validateTag := structTag.Get("validate")
	if validateTag != "" {
		for _, val := range strings.Split(validateTag, ",") {
			if val == "required" {
				structField.isRequired = true

				break
			}
		}
	}
	extensionsTag := structTag.Get("extensions")
	if extensionsTag != "" {
		structField.extensions = map[string]interface{}{}
		for _, val := range strings.Split(extensionsTag, ",") {
			parts := strings.SplitN(val, "=", 2)
			if len(parts) == 2 {
				structField.extensions[parts[0]] = parts[1]
			} else {
				if len(parts[0]) > 0 && string(parts[0][0]) == "!" {
					structField.extensions[string(parts[0][1:])] = false
				} else {
					structField.extensions[parts[0]] = true
				}
			}
		}
	}
	enumsTag := structTag.Get("enums")
	if enumsTag != "" {
		enumType := structField.schemaType
		if structField.schemaType == ARRAY {
			enumType = structField.arrayType
		}

		for _, e := range strings.Split(enumsTag, ",") {
			value, err := defineType(enumType, e)
			if err != nil {
				return nil, err
			}
			structField.enums = append(structField.enums, value)
		}
	}
	defaultTag := structTag.Get("default")
	if defaultTag != "" {
		value, err := defineType(structField.schemaType, defaultTag)
		if err != nil {
			return nil, err
		}
		structField.defaultValue = value
	}

	if IsNumericType(structField.schemaType) || IsNumericType(structField.arrayType) {
		maximum, err := getFloatTag(structTag, "maximum")
		if err != nil {
			return nil, err
		}
		structField.maximum = maximum

		minimum, err := getFloatTag(structTag, "minimum")
		if err != nil {
			return nil, err
		}
		structField.minimum = minimum

		multipleOf, err := getFloatTag(structTag, "multipleOf")
		if err != nil {
			return nil, err
		}
		structField.multipleOf = multipleOf
	}
	if structField.schemaType == STRING || structField.arrayType == STRING {
		maxLength, err := getIntTag(structTag, "maxLength")
		if err != nil {
			return nil, err
		}
		structField.maxLength = maxLength

		minLength, err := getIntTag(structTag, "minLength")
		if err != nil {
			return nil, err
		}
		structField.minLength = minLength
	}
	readOnly := structTag.Get("readonly")
	if readOnly != "" {
		structField.readOnly = readOnly == "true"
	}

	// perform this after setting everything else (min, max, etc...)
	if strings.Contains(jsonTag, ",string") { // @encoding/json: "It applies only to fields of string, floating point, integer, or boolean types."
		defaultValues := map[string]string{
			// Zero Values as string
			STRING:  "",
			INTEGER: "0",
			BOOLEAN: "false",
			NUMBER:  "0",
		}

		defaultValue, ok := defaultValues[structField.schemaType]
		if ok {
			structField.schemaType = STRING

			if structField.exampleValue == nil {
				// if exampleValue is not defined by the user,
				// we will force an example with a correct value
				// (eg: int->"0", bool:"false")
				structField.exampleValue = defaultValue
			}
		}
	}

	return structField, nil
}

// GetSchemaTypePath get path of schema type.
func (parser *Parser) GetSchemaTypePath(schema *spec.Schema, depth int) []string {
	if schema == nil || depth == 0 {
		return nil
	}
	name := schema.Ref.String()
	if name != "" {
		if pos := strings.LastIndexByte(name, '/'); pos >= 0 {
			name = name[pos+1:]
			if schema, ok := parser.swagger.Definitions[name]; ok {
				return parser.GetSchemaTypePath(&schema, depth)
			}
		}

		return nil
	}
	if len(schema.Type) > 0 {
		switch schema.Type[0] {
		case ARRAY:
			depth--
			s := []string{schema.Type[0]}

			return append(s, parser.GetSchemaTypePath(schema.Items.Schema, depth)...)
		case OBJECT:
			if schema.AdditionalProperties != nil && schema.AdditionalProperties.Schema != nil {
				// for map
				depth--
				s := []string{schema.Type[0]}

				return append(s, parser.GetSchemaTypePath(schema.AdditionalProperties.Schema, depth)...)
			}
		}

		return []string{schema.Type[0]}
	}

	return []string{ANY}
}

func replaceLastTag(slice []spec.Tag, element spec.Tag) {
	slice = slice[:len(slice)-1]
	slice = append(slice, element)
}

func getFloatTag(structTag reflect.StructTag, tagName string) (*float64, error) {
	strValue := structTag.Get(tagName)
	if strValue == "" {
		return nil, nil
	}

	value, err := strconv.ParseFloat(strValue, 64)
	if err != nil {
		return nil, fmt.Errorf("can't parse numeric value of %q tag: %v", tagName, err)
	}

	return &value, nil
}

func getIntTag(structTag reflect.StructTag, tagName string) (*int64, error) {
	strValue := structTag.Get(tagName)
	if strValue == "" {
		return nil, nil
	}

	value, err := strconv.ParseInt(strValue, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("can't parse numeric value of %q tag: %v", tagName, err)
	}

	return &value, nil
}

func toSnakeCase(in string) string {
	runes := []rune(in)
	length := len(runes)

	var out []rune
	for i := 0; i < length; i++ {
		if i > 0 && unicode.IsUpper(runes[i]) &&
			((i+1 < length && unicode.IsLower(runes[i+1])) || unicode.IsLower(runes[i-1])) {
			out = append(out, '_')
		}
		out = append(out, unicode.ToLower(runes[i]))
	}

	return string(out)
}

func toLowerCamelCase(in string) string {
	runes := []rune(in)

	var out []rune
	flag := false
	for i, curr := range runes {
		if (i == 0 && unicode.IsUpper(curr)) || (flag && unicode.IsUpper(curr)) {
			out = append(out, unicode.ToLower(curr))
			flag = true
		} else {
			out = append(out, curr)
			flag = false
		}
	}

	return string(out)
}

// defineTypeOfExample example value define the type (object and array unsupported)
func defineTypeOfExample(schemaType, arrayType, exampleValue string) (interface{}, error) {
	switch schemaType {
	case STRING:
		return exampleValue, nil
	case NUMBER:
		v, err := strconv.ParseFloat(exampleValue, 64)
		if err != nil {
			return nil, fmt.Errorf("example value %s can't convert to %s err: %s", exampleValue, schemaType, err)
		}

		return v, nil
	case INTEGER:
		v, err := strconv.Atoi(exampleValue)
		if err != nil {
			return nil, fmt.Errorf("example value %s can't convert to %s err: %s", exampleValue, schemaType, err)
		}

		return v, nil
	case BOOLEAN:
		v, err := strconv.ParseBool(exampleValue)
		if err != nil {
			return nil, fmt.Errorf("example value %s can't convert to %s err: %s", exampleValue, schemaType, err)
		}

		return v, nil
	case ARRAY:
		values := strings.Split(exampleValue, ",")
		result := make([]interface{}, 0)
		for _, value := range values {
			v, err := defineTypeOfExample(arrayType, "", value)
			if err != nil {
				return nil, err
			}
			result = append(result, v)
		}

		return result, nil
	case OBJECT:
		if arrayType == "" {
			return nil, fmt.Errorf("%s is unsupported type in example value `%s`", schemaType, exampleValue)
		}

		values := strings.Split(exampleValue, ",")
		result := map[string]interface{}{}
		for _, value := range values {
			mapData := strings.Split(value, ":")

			if len(mapData) == 2 {
				v, err := defineTypeOfExample(arrayType, "", mapData[1])
				if err != nil {
					return nil, err
				}
				result[mapData[0]] = v
			} else {
				return nil, fmt.Errorf("example value %s should format: key:value", exampleValue)
			}
		}

		return result, nil
	}

	return nil, fmt.Errorf("%s is unsupported type in example value %s", schemaType, exampleValue)
}

// GetAllGoFileInfo gets all Go source files information for given searchDir.
func (parser *Parser) getAllGoFileInfo(packageDir, searchDir string) error {
	return filepath.Walk(searchDir, func(path string, f os.FileInfo, err error) error {
		if err := parser.Skip(path, f); err != nil {
			return err
		} else if f.IsDir() {
			return nil
		}

		relPath, err := filepath.Rel(searchDir, path)
		if err != nil {
			return err
		}

		return parser.parseFile(filepath.ToSlash(filepath.Dir(filepath.Clean(filepath.Join(packageDir, relPath)))), path, nil)
	})
}

func (parser *Parser) getAllGoFileInfoFromDeps(pkg *depth.Pkg) error {
	ignoreInternal := pkg.Internal && !parser.ParseInternal
	if ignoreInternal || !pkg.Resolved { // ignored internal and not resolved dependencies
		return nil
	}

	// Skip cgo
	if pkg.Raw == nil && pkg.Name == "C" {
		return nil
	}
	srcDir := pkg.Raw.Dir
	files, err := ioutil.ReadDir(srcDir) // only parsing files in the dir(don't contains sub dir files)
	if err != nil {
		return err
	}

	for _, f := range files {
		if f.IsDir() {
			continue
		}

		path := filepath.Join(srcDir, f.Name())
		if err := parser.parseFile(pkg.Name, path, nil); err != nil {
			return err
		}
	}

	for i := 0; i < len(pkg.Deps); i++ {
		if err := parser.getAllGoFileInfoFromDeps(&pkg.Deps[i]); err != nil {
			return err
		}
	}

	return nil
}

func (parser *Parser) parseFile(packageDir, path string, src interface{}) error {
	if strings.HasSuffix(strings.ToLower(path), "_test.go") || filepath.Ext(path) != ".go" {
		return nil
	}

	// positions are relative to FileSet
	astFile, err := goparser.ParseFile(token.NewFileSet(), path, src, goparser.ParseComments)
	if err != nil {
		return fmt.Errorf("ParseFile error:%+v", err)
	}

	err = parser.packages.CollectAstFile(packageDir, path, astFile)
	if err != nil {
		return err
	}

	return nil
}

func getOperationID(itm spec.PathItem) (string, string) {
	if itm.Get != nil {
		return http.MethodGet, itm.Get.ID
	}
	if itm.Put != nil {
		return http.MethodPut, itm.Put.ID
	}
	if itm.Post != nil {
		return http.MethodPost, itm.Post.ID
	}
	if itm.Delete != nil {
		return http.MethodDelete, itm.Delete.ID
	}
	if itm.Options != nil {
		return http.MethodOptions, itm.Options.ID
	}
	if itm.Head != nil {
		return http.MethodHead, itm.Head.ID
	}
	if itm.Patch != nil {
		return http.MethodTrace, itm.Patch.ID
	}

	return "", ""
}

func (parser *Parser) checkOperationIDUniqueness() error {
	// operationsIds contains all operationId annotations to check it's unique
	operationsIds := make(map[string]string)

	for path, itm := range parser.swagger.Paths.Paths {
		method, id := getOperationID(itm)
		err := saveOperationID(operationsIds, id, fmt.Sprintf("%s %s", method, path))
		if err != nil {
			return err
		}
	}

	return nil
}

func saveOperationID(operationsIds map[string]string, operationID, currentPath string) error {
	if operationID == "" {
		return nil
	}
	previousPath, ok := operationsIds[operationID]
	if ok {
		return fmt.Errorf(
			"duplicated @id annotation '%s' found in '%s', previously declared in: '%s'",
			operationID, currentPath, previousPath)
	}
	operationsIds[operationID] = currentPath

	return nil
}

// Skip returns filepath.SkipDir error if match vendor and hidden folder.
func (parser *Parser) Skip(path string, f os.FileInfo) error {
	if f.IsDir() {
		if !parser.ParseVendor && f.Name() == "vendor" || // ignore "vendor"
			f.Name() == "docs" || // exclude docs
			len(f.Name()) > 1 && f.Name()[0] == '.' { // exclude all hidden folder
			return filepath.SkipDir
		}

		if parser.excludes != nil {
			if _, ok := parser.excludes[path]; ok {
				return filepath.SkipDir
			}
		}
	}

	return nil
}

// GetSwagger returns *spec.Swagger which is the root document object for the API specification.
func (parser *Parser) GetSwagger() *spec.Swagger {
	return parser.swagger
}
