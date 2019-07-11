package api

import (
	"context"
	"log"
	"net/http"
	"time"

	"agentgo/facts"
	"agentgo/types"
	"agentgo/discovery"

	"github.com/99designs/gqlgen/handler"
	"github.com/go-chi/chi"
	"github.com/rs/cors"
)

type storeInterface interface {
	Metrics(filters map[string]string) (result []types.Metric, err error)
}

type dockerInterface interface {
	Containers(ctx context.Context, maxAge time.Duration, includeIgnored bool) (containers []facts.Container, err error)
}

// API : Structure that contains API's port
type API struct {
	bindAddress  string
	router       http.Handler
	db           storeInterface
	dockerFact   dockerInterface
	psFact       *facts.ProcessProvider
	factProvider *facts.FactProvider
	disc         *discovery.Discovery
}

// New : Function that instantiate a new API's port from environment variable or from a default port
func New(db storeInterface, dockerFact *facts.DockerProvider, psFact *facts.ProcessProvider, factProvider *facts.FactProvider, bindAddress string, disc *discovery.Discovery) *API {
	router := chi.NewRouter()
	router.Use(cors.New(cors.Options{
		AllowedOrigins:   []string{"*"},
		AllowCredentials: true,
		Debug:            false,
	}).Handler)
	api := &API{bindAddress: bindAddress, db: db, psFact: psFact, dockerFact: dockerFact, factProvider: factProvider, disc: disc}
	router.HandleFunc("/metrics", api.promExporter)
	router.Handle("/playground", handler.Playground("GraphQL playground", "/graphql"))
	router.Handle("/graphql", handler.GraphQL(NewExecutableSchema(Config{Resolvers: &Resolver{api: api}})))
	api.router = router
	return api
}

// Run : Starts our API
func (api API) Run() {
	log.Printf("Starting API on %s", api.bindAddress)
	if err := http.ListenAndServe(api.bindAddress, api.router); err != http.ErrServerClosed {
		log.Printf("Failed to start API server: %v", err)
	}
}
