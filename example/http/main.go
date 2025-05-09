package main

import (
	"net/http"
)

func main() {
	http.HandleFunc("/hello", sayHello)

	http.ListenAndServe(":8080", nil)
}

// @Summary SayHello
// @description  sayhello
// @Accept json
// @Produce  json
// @Success 200 {string} string
// @Router /hello [get]
func sayHello(rw http.ResponseWriter, r *http.Request) {
	rw.WriteHeader(http.StatusOK)
	rw.Write([]byte("hello"))
}
