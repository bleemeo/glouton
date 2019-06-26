package api

import (
	"log"
	"net/http"
	"os"

	"agentgo/types"
	"github.com/99designs/gqlgen/handler"
)

const defaultPort = "8015"

type storeInterface interface {
	Metrics(filters map[string]string) (result []types.Metric, err error)
}

// API : Structure that contains API's port
type API struct {
	Port string
	db   storeInterface
}

// New : Function that instantiate a new API's port from environment variable or from a default port
func New(db storeInterface) *API {
	port := os.Getenv("PORT")
	if port == "" {
		port = defaultPort
	}
	api := &API{Port: port, db: db}
	http.HandleFunc("/metrics", api.promExporter)
	http.Handle("/", handler.Playground("GraphQL playground", "/graphql"))
	http.Handle("/graphql", handler.GraphQL(NewExecutableSchema(Config{Resolvers: &Resolver{api: api}})))
	return api
}

// Run : Starts our API
func (api API) Run() {
	log.Fatal(http.ListenAndServe(":"+api.Port, nil))
}
