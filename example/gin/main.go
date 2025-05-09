package main

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"gitlab.xpaas.lenovo.com/observability/lib/go-swagger/example/gin/models"
	"gitlab.xpaas.lenovo.com/observability/lib/go-swagger/swagger"
)

// @Summary SayHello
// @description  sayhello
// @Accept json
// @Produce  json
// @Param body body models.GreatingRequest  true "request"
// @Success 200 {object} models.GreatingResponse
// @Router /greating [post]
func Greating(ctx *gin.Context) {
	var (
		request models.GreatingRequest
		err     error
	)

	defer func() {
		ctx.JSON(http.StatusOK, createResponse(&request))
	}()

	if err = ctx.ShouldBindJSON(&request); err != nil {
		return
	}
}

func createResponse(request *models.GreatingRequest) models.GreatingResponse {
	return models.GreatingResponse{
		Greating: fmt.Sprintf("Hello %s ~", request.Name),
	}
}

func main() {
	app := gin.Default()
	app.POST("/greating", Greating)

	swagger.GinSwagger(app)

	app.Run("0.0.0.0:8080")
}
