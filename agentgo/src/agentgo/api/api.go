package api

import (
	"log"
	"net/http"
	"os"

	"agentgo/facts"
	"agentgo/types"

	"github.com/99designs/gqlgen/handler"
	"github.com/go-chi/chi"
	"github.com/rs/cors"
)

const defaultPort = "8015"

type storeInterface interface {
	Metrics(filters map[string]string) (result []types.Metric, err error)
}

// API : Structure that contains API's port
type API struct {
	Port   string
	router *chi.Mux
	db     storeInterface
}

// New : Function that instantiate a new API's port from environment variable or from a default port
func New(db storeInterface, dockerFact *facts.DockerProvider) *API {
	router := chi.NewRouter()
	port := os.Getenv("PORT")
	if port == "" {
		port = defaultPort
	}
	router.Use(cors.New(cors.Options{
		AllowedOrigins:   []string{"*"},
		AllowCredentials: true,
		Debug:            false,
	}).Handler)
	api := &API{Port: port, db: db}
	router.HandleFunc("/metrics", api.promExporter)
	router.Handle("/playground", handler.Playground("GraphQL playground", "/graphql"))
	router.Handle("/graphql", handler.GraphQL(NewExecutableSchema(Config{Resolvers: &Resolver{api: api, dockerFact: dockerFact}})))
	api.router = router
	return api
}

// Run : Starts our API
func (api API) Run() {
	log.Fatal(http.ListenAndServe(":"+api.Port, api.router))
}
