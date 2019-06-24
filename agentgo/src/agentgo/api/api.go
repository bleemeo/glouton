package api

import (
	"log"
	"net/http"
	"os"
)

const defaultPort = "8015"

// API : Structure that contains API's port
type API struct {
	Port string
}

// New : Function that instanciate a new API's port from environment variable or from a default port
func New() *API {
	port := os.Getenv("PORT")
	if port == "" {
		port = defaultPort
	}
	return &API{Port: port}
}

// Run : Starts our API
func (api API) Run() {
	log.Fatal(http.ListenAndServe(":"+api.Port, nil))
}
