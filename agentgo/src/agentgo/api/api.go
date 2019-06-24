package api

import (
	"log"
	"net/http"
	"os"

	"agentgo/types"
)

const defaultPort = "8015"

type store interface {
	Metrics(filters map[string]string) (result []types.Metric, err error)
}

// API : Structure that contains API's port
type API struct {
	Port string
	db   store
}

// New : Function that instanciate a new API's port from environment variable or from a default port
func New(db store) *API {
	port := os.Getenv("PORT")
	if port == "" {
		port = defaultPort
	}
	api := &API{Port: port, db: db}
	http.HandleFunc("/metrics", api.promExporter)
	return api
}

// Run : Starts our API
func (api API) Run() {
	log.Fatal(http.ListenAndServe(":"+api.Port, nil))
}
