### Swagger工具
## 工具介绍
* 辅助生成Swagger配置文件
* 基于 swag 和 go ast 
* 兼容 swag 的配置语法
* 可以开启代码扫描自动生成一部分 swagger 配置
* 可以覆盖自动生成的 swagger 配置
* 工具分为两部分，集成 swagger页面和自动生成 swagger.json (自动生成不稳定，推荐使用swag init)

## 集成swagger页面
* gin 框架集成 可参考 example/gin/main.go
```
app := gin.Default()
app.POST("/greating", Greating)
app.GET("/sayhello/:name", SayHello)

// 调用这个API就可以直接集成
swagger.GinSwagger(app)

app.Run("0.0.0.0:1323")
```
* go 原生框架集成 可参考 example/http/main.go
```
// 方式一 使用新建的 Serve Mux
mux := http.NewServeMux()
mux.HandleFunc("/hello", sayHello)
swagger.Swagger(mux)
http.ListenAndServe(":8080", mux)

// 方式二 使用默认的 Serve Mux
http.HandleFunc("/hello", sayHello)
swagger.Swagger(http.DefaultServeMux)
http.ListenAndServe(":8080", nil)
```
* victoria 框架集成 可参考 example/victoria/main.go
```
envflag.Parse()
buildinfo.Init()

go httpserver.Serve("0.0.0.0:8080", swagger.NewVictoriaSwagger().Swagger())

sig := procutil.WaitForSigterm()
logger.Infof("service received signal %s", sig)
if err := httpserver.Stop(":8080"); err != nil {
	logger.Fatalf("cannot stop the webservice: %s", err)
}
```
## 生成swagger.json
推荐使用 swag init 工具 https://github.com/swaggo/swag  
加载swagger.json的顺序是 ./swagger.json ./docs/swagger.json  
使用 swag init 生成 docs/swagger.json 需要确保当前目录不存在 swagger.json
### 使用 go-swagger 自动生成工具（目前存在问题）
```
type SwaggerConfig struct {
	ParseDirs         []string
	Filter            string
	PrintGenerate     bool
	SwaggerOptions    []func(*Parser)
	SwaggerURL        string
	OutputDir         string
	FormatSwaggerJSON bool
}
```
* ParseDirs         需要扫描的代码文件夹，不指定扫描的文件夹，就会默认读取当前文件夹下的 doc.json Swagger 配置文件
* Filter            需要过滤的函数签名  默认 func(*gin.Context)
* PrintGenerate     是否打印添加 swagger 注释的函数
* SwaggerOptions    修改 swagger 配置文件
* SwaggerURL        Swagger 的访问路径 e.g http://localhost:1323 (如果需要外部访问，必须是服务器IP)
* OutputDir         Swagger 配置文件的文件夹
* FormatSwaggerJSON 生成的Swagger配置文件是否有缩进
## 自动生成的前提条件
* ParseDir(app *gin.Engine, options ...Option) 方法需要的 *gin.Engine 是注册路由之后的，不然无法拿到路由信息
* ParseDir(app *gin.Engine, options ...Option) options 需要指定扫描的文件夹，不然不会扫描并生成配置文件
* 支持扫描的方法必须显示的存在被扫描的方法内部，方法嵌套不会被扫描到
## 目前支持的自动生成注解的方法
* github.com/gin-gonic/gin.Context.JSON
* github.com/gin-gonic/gin.Context.JSONP
* github.com/gin-gonic/gin.Context.Query
* github.com/gin-gonic/gin.Context.QueryArray
* github.com/gin-gonic/gin.Context.QueryMap
* github.com/gin-gonic/gin.Context.DefaultQuery
* github.com/gin-gonic/gin.Context.GetQuery
* github.com/gin-gonic/gin.Context.GetQueryArray
* github.com/gin-gonic/gin.Context.GetQueryMap
* github.com/gin-gonic/gin.Context.ShouldBindQuery
* github.com/gin-gonic/gin.Context.Param
* github.com/gin-gonic/gin.Context.ShouldBindJSON
后续会持续增加
## 使用方法
```
package main

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"gitlab.xpaas.lenovo.com/observability/lib/go-swagger/parser"
)

func Greating(ctx *gin.Context) {
	var (
		request GreatingRequest
		err     error
	)

	defer func() {
		ctx.JSON(http.StatusOK, createResponse(&request))
	}()

	if err = ctx.ShouldBindJSON(&request); err != nil {
		return
	}
}

func SayHello(ctx *gin.Context) {
	var (
		response GreatingResponse
	)

	defer func() {
		ctx.JSON(http.StatusOK, response)
	}()

	response = GreatingResponse{
		Greating: ctx.Param("name"),
	}
}

func createResponse(request *GreatingRequest) GreatingResponse {
	return GreatingResponse{
		Greating: fmt.Sprintf("Hello %s ~", request.Name),
	}
}

func main() {
	app := gin.Default()
	app.POST("/greating", Greating)
	app.GET("/sayhello/:name", SayHello)

	if err := parser.ParseDir(app, func(sc *parser.SwaggerConfig) {
		sc.ParseDirs = []string{"."}
		sc.PrintGenerate = true
		sc.FormatSwaggerJSON = true
		sc.SwaggerURL = "http://localhost:1323"
	}); err != nil {
		panic(err)
	}

	app.Run(":1323")
}

type GreatingRequest struct {
	Name string `json:"name"`
}

type GreatingResponse struct {
	Greating string `json:"greating"`
}

```