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
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
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

	boxAssets := packr.New("assets", "./static/assets")
	boxHTML := packr.New("html", "./static")

	reg := prometheus.NewPedanticRegistry()
	reg.MustRegister(
		api,
		prometheus.NewGoCollector(),
		prometheus.NewProcessCollector(prometheus.ProcessCollectorOpts{}),
	)
	router.Handle("/metrics", promhttp.InstrumentMetricHandler(reg, promhttp.HandlerFor(reg, promhttp.HandlerOpts{Registry: reg})))
	router.Handle("/playground", handler.Playground("GraphQL playground", "/graphql"))
	router.Handle("/graphql", handler.GraphQL(NewExecutableSchema(Config{Resolvers: &Resolver{api: api}})))
	router.Handle("/static/*", http.StripPrefix("/static", http.FileServer(boxAssets)))
	router.HandleFunc("/*", func(w http.ResponseWriter, r *http.Request) {
		html, _ := boxHTML.Find("index.html")
		_, err := w.Write(html)
		if err != nil {
			log.Printf("DBG2: fail to serve index.html: %v", err)
		}
	})
	api.router = router
	return api
}

// Run : Starts our API
func (api API) Run(_ context.Context) error {
	log.Printf("Starting API on %s", api.bindAddress)
	if err := http.ListenAndServe(api.bindAddress, api.router); err != http.ErrServerClosed {
		return err
	}
	return nil
}
