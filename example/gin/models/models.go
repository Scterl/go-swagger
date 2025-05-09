package models

type GreatingRequest struct {
	Name string `json:"name"`
}

type GreatingResponse struct {
	Greating string `json:"greating"`
}
