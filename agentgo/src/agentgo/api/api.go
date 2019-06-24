package api

import (
	"log"
	"net/http"
	"os"

	"github.com/99designs/gqlgen/handler"
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
	http.Handle("/", handler.Playground("GraphQL playground", "/graphql"))
	http.Handle("/graphql", handler.GraphQL(NewExecutableSchema(Config{Resolvers: &Resolver{}})))
	log.Fatal(http.ListenAndServe(":"+api.Port, nil))
}
