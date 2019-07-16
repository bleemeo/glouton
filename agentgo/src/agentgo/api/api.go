package api

//go:generate go run github.com/gobuffalo/packr/v2/packr2

import (
	"context"
	"log"
	"net/http"
	"time"

	"agentgo/discovery"
	"agentgo/facts"
	"agentgo/types"

	"github.com/99designs/gqlgen/handler"
	"github.com/go-chi/chi"
	"github.com/gobuffalo/packr/v2"
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

	boxHTML := packr.New("html", "./static")

	// There is a bug about serving directory with packr2 and golang v1.12.4
	// (https://github.com/gobuffalo/packr/issues/198)
	// The bug has been fixed but not yet released
	router.HandleFunc("/logo", func(w http.ResponseWriter, _ *http.Request) {
		image, err := boxHTML.Find("img/logo.png")
		if err != nil {
			log.Printf("DBG2: %v", err)
		}
		w.Write(image)
	})
	router.Handle("/test", http.FileServer(boxHTML))
	router.HandleFunc("/metrics", api.promExporter)
	router.Handle("/playground", handler.Playground("GraphQL playground", "/graphql"))
	router.Handle("/graphql", handler.GraphQL(NewExecutableSchema(Config{Resolvers: &Resolver{api: api}})))
	router.HandleFunc("/*", func(w http.ResponseWriter, _ *http.Request) {
		html, err := boxHTML.Find("index.html")
		if err != nil {
			log.Printf("DBG2: %v", err)
		}
		w.Write(html)
	})
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
