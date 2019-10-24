// Copyright 2015-2019 Bleemeo
//
// bleemeo.com an infrastructure monitoring solution in the Cloud
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package api

//go:generate go run github.com/gobuffalo/packr/v2/packr2

import (
	"context"
	"net/http"
	"time"

	"glouton/discovery"
	"glouton/facts"
	"glouton/logger"
	"glouton/types"

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

type agentInterface interface {
	BleemeoRegistrationAt() time.Time
	BleemeoLastReport() time.Time
	BleemeoConnected() bool
	Tags() []string
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
	agent        agentInterface
}

// New : Function that instantiate a new API's port from environment variable or from a default port
func New(db storeInterface, dockerFact *facts.DockerProvider, psFact *facts.ProcessProvider, factProvider *facts.FactProvider, bindAddress string, disc *discovery.Discovery, agent agentInterface, promExporter http.Handler) *API {
	router := chi.NewRouter()
	router.Use(cors.New(cors.Options{
		AllowedOrigins:   []string{"*"},
		AllowCredentials: true,
		Debug:            false,
	}).Handler)
	api := &API{bindAddress: bindAddress, db: db, psFact: psFact, dockerFact: dockerFact, factProvider: factProvider, disc: disc, agent: agent}

	boxAssets := packr.New("assets", "./static/assets")
	boxHTML := packr.New("html", "./static")

	router.Handle("/metrics", promExporter) // promhttp.InstrumentMetricHandler(reg, promhttp.HandlerFor(reg, promhttp.HandlerOpts{})))
	router.Handle("/playground", handler.Playground("GraphQL playground", "/graphql"))
	router.Handle("/graphql", handler.GraphQL(NewExecutableSchema(Config{Resolvers: &Resolver{api: api}})))
	router.Handle("/static/*", http.StripPrefix("/static", http.FileServer(boxAssets)))
	router.HandleFunc("/*", func(w http.ResponseWriter, r *http.Request) {
		html, _ := boxHTML.Find("index.html")
		_, err := w.Write(html)
		if err != nil {
			logger.V(2).Printf("fail to serve index.html: %v", err)
		}
	})
	api.router = router
	return api
}

// Run : Starts our API
func (api API) Run(ctx context.Context) error {
	srv := http.Server{
		Addr:    api.bindAddress,
		Handler: api.router,
	}

	idleConnsClosed := make(chan struct{})
	go func() {
		<-ctx.Done()
		subCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := srv.Shutdown(subCtx); err != nil {
			logger.V(2).Printf("HTTP server Shutdown: %v", err)
		}
		close(idleConnsClosed)
	}()
	logger.Printf("Starting API on %s ✔️", api.bindAddress)
	logger.Printf("To access the local panel connect to http://%s 🌐", api.bindAddress)
	if err := srv.ListenAndServe(); err != http.ErrServerClosed {
		return err
	}
	return nil
}
