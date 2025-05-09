package parser

import (
	"fmt"
	"log"
	"regexp"
	"sort"
	"strings"

	"github.com/gin-gonic/gin"
)

var (
	// commentRegExp = regexp.MustCompile("@(Summary|Description|Accept|Produce|Params|Success|Failure|Response|Header|Router)")

	commentSummaryRegExp     = regexp.MustCompile(`@Summary (.*)`)
	commentDiscriptionRegExp = regexp.MustCompile(`@Description (.*)`)
	commentMimeRegExp        = regexp.MustCompile(`@(Accept|Produce) (json-api|json-stream|xml|plain|html|mpfd|x-www-form-urlencoded|json|octet-stream|png|jpeg|gif)`)
	commentParamRegExp       = regexp.MustCompile("@Params (.*) (query|path|header|body|formData) (.*) (true|false) (.*)")
	commentResponseRegExp    = regexp.MustCompile(`@(Success|Failure|Response)`)
	commentHeaderRegExp      = regexp.MustCompile("@Header")
	commentRouterRegExp      = regexp.MustCompile(`@Router (.*) (.*) (.*)`)
	commentPathParamRegExp   = regexp.MustCompile(`((:|\*)(\w*))|(\{(\w*)\})`)
)

type GinSwagger struct {
	FunctionDesc
	summaries    []string
	descriptions []string
	params       []string
	failures     []string
	headers      []string
	accept       string
	produce      string
	success      string
	response     string
	router       string

	others []string
}

func GetGinComments(funcDesc FunctionDesc, routeInfos map[string]gin.RouteInfo) []string {

	results := []string{}

	desc := &GinSwagger{
		FunctionDesc: funcDesc,
		summaries:    make([]string, 0),
		descriptions: make([]string, 0),
		params:       make([]string, 0),
		failures:     make([]string, 0),
		headers:      make([]string, 0),
		others:       make([]string, 0),
	}

	desc.parseComments()
	desc.generateComments(routeInfos)

	results = append(results, desc.summaries...)
	results = append(results, desc.descriptions...)
	results = append(results, desc.params...)
	results = append(results, desc.failures...)
	results = append(results, desc.headers...)
	results = append(results, desc.others...)
	if desc.accept != "" {
		results = append(results, desc.accept)
	}
	if desc.produce != "" {
		results = append(results, desc.produce)
	}
	if desc.success != "" {
		results = append(results, desc.success)
	}
	if desc.response != "" {
		results = append(results, desc.response)
	}
	if desc.router != "" {
		results = append(results, desc.router)
	}

	sort.Strings(results)

	return results
}

func (desc *GinSwagger) parseComments() {
	for _, comment := range desc.Comments {
		if commentSummaryRegExp.MatchString(comment) {
			desc.summaries = append(desc.summaries, comment)
		} else if commentDiscriptionRegExp.MatchString(comment) {
			desc.descriptions = append(desc.descriptions, comment)
		} else if commentParamRegExp.MatchString(comment) {
			desc.params = append(desc.params, comment)
		} else if commentResponseRegExp.MatchString(comment) {
			if strings.HasPrefix(comment, "// @Success") {
				desc.success = comment
			} else if strings.HasPrefix(comment, "// @Failure") {
				desc.failures = append(desc.failures, comment)
			} else if strings.HasPrefix(comment, "// @Response") {
				desc.response = comment
			} else {
				desc.others = append(desc.others, comment)
			}
		} else if commentHeaderRegExp.MatchString(comment) {
			desc.headers = append(desc.headers, comment)
		} else if commentMimeRegExp.MatchString(comment) {
			if strings.HasPrefix(comment, "// @Accept") {
				desc.accept = comment
			} else if strings.HasPrefix(comment, "// @Produce") {
				desc.produce = comment
			}
		} else if commentRouterRegExp.MatchString(comment) {
			desc.router = comment
		} else {
			desc.others = append(desc.others, comment)
		}
	}
}

func (desc *GinSwagger) generateComments(routeInfos map[string]gin.RouteInfo) {
	// Summery
	genSummary := fmt.Sprintf("// @Summary %s", desc.Name)
	isHasSummary := false
	for _, summary := range desc.summaries {
		if summary == genSummary {
			isHasSummary = true
			break
		}
	}
	if !isHasSummary {
		desc.summaries = append([]string{genSummary}, desc.summaries...)
	}

	pathParams := map[string]string{}

	// Param
	for _, callExpr := range desc.Exprs {
		selector := fmt.Sprintf("%s.%s", callExpr.Receiver, callExpr.Name)

		// Response
		switch selector {
		case "*github.com/gin-gonic/gin.Context.JSON", "*github.com/gin-gonic/gin.Context.JSONP":
			splitType := strings.Split(callExpr.Args[1].Type, "/")
			var argType string
			if len(splitType) == 1 || len(splitType) == 0 {
				argType = callExpr.Args[1].Type
			} else {
				argType = splitType[len(splitType)-1]
			}
			if callExpr.Args[0].Name == "http.StatusOK" || callExpr.Args[0].Value == "200" {
				if len(desc.success) > 0 {
					continue
				}

				desc.success = fmt.Sprintf("// @Success %s {object} %s", callExpr.Args[0].Value, argType)
			} else {
				desc.failures = append(desc.failures, fmt.Sprintf("// @Failure %s {object} %s", callExpr.Args[0].Value, argType))
			}
		case "*github.com/gin-gonic/gin.Context.XML":
			log.Printf("[WARNING] not support %s generate response", selector)
		case "*github.com/gin-gonic/gin.Context.YAML":
			log.Printf("[WARNING] not support %s generate response", selector)
		case "*github.com/gin-gonic/gin.Context.ProtoBuf":
			log.Printf("[WARNING] not support %s generate response", selector)
		}

		// Param Query
		switch selector {
		// query
		case "*github.com/gin-gonic/gin.Context.Query":
			argName := strings.Trim(callExpr.Args[0].Name, `"`)
			desc.params = appendParam(
				desc.params,
				fmt.Sprintf("// @Param %s", argName),
				fmt.Sprintf("// @Param %s query string false %s", argName, callExpr.Args[0].Value),
			)
		case "*github.com/gin-gonic/gin.Context.QueryArray":
			argName := strings.Trim(callExpr.Args[0].Name, `"`)
			desc.params = appendParam(
				desc.params,
				fmt.Sprintf("// @Param %s", argName),
				fmt.Sprintf("// @Param %s query []string false %s", argName, callExpr.Args[0].Value),
			)
		case "*github.com/gin-gonic/gin.Context.QueryMap":
			argName := strings.Trim(callExpr.Args[0].Name, `"`)
			desc.params = appendParam(
				desc.params,
				fmt.Sprintf("// @Param %s", argName),
				fmt.Sprintf("// @Param %s query object false %s", argName, callExpr.Args[0].Value),
			)
		case "*github.com/gin-gonic/gin.Context.DefaultQuery":
			argName := strings.Trim(callExpr.Args[0].Name, `"`)
			desc.params = appendParam(
				desc.params,
				fmt.Sprintf("// @Param %s", argName),
				fmt.Sprintf("// @Param %s query object false %s default(%s)", argName, callExpr.Args[0].Value, callExpr.Args[1].Value),
			)
		case "*github.com/gin-gonic/gin.Context.GetQuery":
			argName := strings.Trim(callExpr.Args[0].Name, `"`)
			desc.params = appendParam(
				desc.params,
				fmt.Sprintf("// @Param %s", argName),
				fmt.Sprintf("// @Param %s query object false %s", argName, callExpr.Args[0].Value),
			)
		case "*github.com/gin-gonic/gin.Context.GetQueryArray":
			argName := strings.Trim(callExpr.Args[0].Name, `"`)
			desc.params = appendParam(
				desc.params,
				fmt.Sprintf("// @Param %s", argName),
				fmt.Sprintf("// @Param %s query []string false %s", argName, callExpr.Args[0].Value),
			)
		case "*github.com/gin-gonic/gin.Context.GetQueryMap":
			argName := strings.Trim(callExpr.Args[0].Name, `"`)
			desc.params = appendParam(
				desc.params,
				fmt.Sprintf("// @Param %s", argName),
				fmt.Sprintf("// @Param %s query object false %s", argName, callExpr.Args[0].Value),
			)
		case "*github.com/gin-gonic/gin.Context.ShouldBindQuery":
			splitType := strings.Split(callExpr.Args[0].Type, "/")
			var argType string
			if len(splitType) == 1 || len(splitType) == 0 {
				argType = callExpr.Args[1].Type
			} else {
				argType = splitType[len(splitType)-1]
			}
			argName := strings.Trim(callExpr.Args[0].Name, `"`)
			desc.params = appendParam(
				desc.params,
				fmt.Sprintf("// @Param %s", argName),
				fmt.Sprintf(`// @Param %s query %s false "%s"`, argName, argType, argName),
			)
		// path param
		case "*github.com/gin-gonic/gin.Context.Param":
			splitType := strings.Split(callExpr.Args[0].Type, "/")
			var argType string
			if len(splitType) == 1 {
				argType = callExpr.Args[0].Type
			} else {
				argType = splitType[len(splitType)-1]
			}
			argName := strings.Trim(callExpr.Args[0].Name, `"`)
			desc.params = appendParam(
				desc.params,
				fmt.Sprintf("// @Param %s", argName),
				fmt.Sprintf(`// @Param %s path %s true "%s"`, argName, argType, argName),
			)
		// post data
		case "*github.com/gin-gonic/gin.Context.ShouldBindJSON":
			splitType := strings.Split(callExpr.Args[0].Type, "/")
			var argType string
			if len(splitType) == 1 || len(splitType) == 0 {
				argType = callExpr.Args[1].Type
			} else {
				argType = splitType[len(splitType)-1]
			}
			argName := strings.Trim(callExpr.Args[0].Name, `"`)
			desc.params = appendParam(
				desc.params,
				fmt.Sprintf("// @Param %s", argName),
				fmt.Sprintf(`// @Param %s body %s true "%s"`, argName, argType, argName),
			)
		default:
			// log.Printf("[ERROR] generate swagger Params not support %s method at func %s", selector, desc.fset.Position(desc.source.Pos()))
		}
	}

	for key, info := range routeInfos {
		if strings.HasSuffix(key, desc.PackageName) {
			if len(desc.router) > 0 {
				// check swagger Router format
				swaggerQueryParams := parseQueryPathParams(desc.router)
				swaggerFormat := commentPathParamRegExp.ReplaceAllString(desc.router, "@@")
				pathFormat := commentPathParamRegExp.ReplaceAllString(info.Path, "@@")
				if !strings.ContainsAny(swaggerFormat, pathFormat) {
					log.Printf("[ERROR] swagger comment %s is not match gin path %s\n", desc.router, info.Path)
					return
				}
				if len(swaggerQueryParams) != len(pathParams) {
					log.Printf("[ERROR] swagger comment %s's params is not match gin path %s\n", desc.router, info.Path)
					return
				}
				for _, param := range swaggerQueryParams {
					if _, exist := pathParams[param]; !exist {
						log.Printf("[ERROR] query path %s, path param %s is not found in func %s %s\n", info.Path, param, desc.Name, desc.fset.Position(desc.source.Pos()))
						return
					}
				}
			} else {
				// generate swagger Route comment
				path := commentPathParamRegExp.ReplaceAllStringFunc(info.Path, func(s string) string {
					str := strings.TrimPrefix(strings.TrimPrefix(s, ":"), "*")
					return "{" + str + "}"
				})
				desc.router = fmt.Sprintf("// @Router %s [%s] %s", path, info.Method, desc.PackageName)
			}
		}
	}
}

func appendParam(params []string, p, source string) []string {
	isHas := false
	for _, param := range params {
		if strings.HasPrefix(param, p) {
			isHas = true
			break
		}
	}
	if !isHas {
		params = append(params, source)
	}

	return params
}

func parseQueryPathParams(path string) []string {
	results := []string{}
	paramStrs := commentPathParamRegExp.FindAllString(path, -1)
	for _, param := range paramStrs {
		str := strings.TrimPrefix(strings.TrimPrefix(param, ":"), "*")
		str = strings.TrimSuffix(strings.TrimPrefix(str, "{"), "}")
		results = append(results, str)

	}
	return results
}
