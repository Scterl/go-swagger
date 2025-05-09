package main

import (
	"net/http"
	"strings"

	"github.com/VictoriaMetrics/VictoriaMetrics/lib/buildinfo"
	"github.com/VictoriaMetrics/VictoriaMetrics/lib/envflag"
	"github.com/VictoriaMetrics/VictoriaMetrics/lib/httpserver"
	"github.com/VictoriaMetrics/VictoriaMetrics/lib/logger"
	"github.com/VictoriaMetrics/VictoriaMetrics/lib/procutil"
)

func main() {
	envflag.Parse()
	buildinfo.Init()

	sig := procutil.WaitForSigterm()
	logger.Infof("service received signal %s", sig)
	if err := httpserver.Stop(":8080"); err != nil {
		logger.Fatalf("cannot stop the webservice: %s", err)
	}
}

func mux(response http.ResponseWriter, request *http.Request) bool {
	if !strings.HasPrefix(request.RequestURI, "/api") {
		return false
	}

	switch request.RequestURI {
	case "/api/hello":
		sayHello(response, request)
	default:
		return false
	}

	return true
}

// @Summary SayHello
// @description  sayhello
// @Accept json
// @Produce  json
// @Success 200 {string} string
// @Router /api/hello [get]
func sayHello(response http.ResponseWriter, request *http.Request) {
	response.WriteHeader(http.StatusOK)
	response.Write([]byte("hello"))
}
