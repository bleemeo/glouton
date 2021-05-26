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

//go:generate go run github.com/go-bindata/go-bindata/v3/go-bindata -o api-bindata.go -pkg api -fs -nocompress -nomemcopy -prefix static static/...

import (
	"context"
	"errors"
	"fmt"
	"html/template"
	"io"
	"io/ioutil"
	"net/http"
	"path"
	"strings"
	"time"

	"glouton/discovery"
	"glouton/facts"
	"glouton/logger"
	"glouton/prometheus/promql"
	"glouton/store"
	"glouton/threshold"
	"glouton/types"

	"github.com/99designs/gqlgen/graphql/handler"
	"github.com/99designs/gqlgen/graphql/playground"
	"github.com/go-chi/chi"
	"github.com/rs/cors"
)

type containerInterface interface {
	Containers(ctx context.Context, maxAge time.Duration, includeIgnored bool) (containers []facts.Container, err error)
}

type agentInterface interface {
	BleemeoRegistrationAt() time.Time
	BleemeoLastReport() time.Time
	BleemeoConnected() bool
	Tags() []string
}

// API contains API's port.
type API struct {
	BindAddress        string
	StaticCDNURL       string
	MetricFormat       types.MetricFormat
	DB                 *store.Store
	ContainerRuntime   containerInterface
	PsFact             *facts.ProcessProvider
	FactProvider       *facts.FactProvider
	Disccovery         *discovery.Discovery
	AgentInfo          agentInterface
	PrometheurExporter http.Handler
	Threshold          *threshold.Registry
	DiagnosticPage     func() string
	DiagnosticZip      func(w io.Writer) error

	router http.Handler
}

type gloutonUIConfig struct {
	StaticCDNURL string
}

type assetsFileServer struct {
	fs http.Handler
}

func (f *assetsFileServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	r.URL.Path = path.Join("assets", r.URL.Path)

	// let the client browser decode the gzipped js files
	if strings.HasPrefix(r.URL.Path, "assets/js/") {
		w.Header().Add("Content-Encoding", "gzip")
	}

	f.fs.ServeHTTP(w, r)
}

func (api *API) init() {
	router := chi.NewRouter()
	router.Use(cors.New(cors.Options{
		AllowedOrigins:   []string{"*"},
		AllowCredentials: true,
		Debug:            false,
	}).Handler)

	staticFolder := AssetFile()

	fallbackIndex := []byte("Error while initializing local UI. See Glouton logs")

	var indexBody []byte

	indexFile, err := staticFolder.Open("/index.html")
	if err == nil {
		indexBody, err = ioutil.ReadAll(indexFile)
	}

	indexFile.Close()

	if err != nil {
		logger.Printf("Error while loading index.html. Local UI will be broken: %v", err)

		indexBody = fallbackIndex
	}

	indexTmpl, err := template.New("index").Parse(string(indexBody))
	if err != nil {
		logger.Printf("Error while loading index.html. Local UI will be broken: %v", err)
	}

	var diagnosticTmpl *template.Template

	var diagnosticBody []byte

	diagnosticFile, err := staticFolder.Open("/diagnostic.html")
	if err == nil {
		diagnosticBody, err = ioutil.ReadAll(diagnosticFile)
	}

	diagnosticFile.Close()

	if err != nil {
		logger.Printf("Error while loading diagnostic.html: %v", err)
	} else {
		diagnosticTmpl, err = template.New("diagnostic").Parse(string(diagnosticBody))
		if err != nil {
			diagnosticTmpl = nil
			logger.Printf("Error while loading diagnostic.html. Local UI will be broken: %v", err)
		}
	}

	promql := promql.PromQL{}
	router.Mount("/api/v1", promql.Register(api.DB))
	router.Handle("/metrics", api.PrometheurExporter)
	router.Handle("/playground", playground.Handler("GraphQL playground", "/graphql"))
	router.Handle("/graphql", handler.NewDefaultServer(NewExecutableSchema(Config{Resolvers: &Resolver{api: api}})))
	router.HandleFunc("/diagnostic", func(w http.ResponseWriter, r *http.Request) {
		content := api.DiagnosticPage()

		var err error

		if diagnosticTmpl == nil {
			fmt.Fprintln(w, "diagnostic.html load failed. Fallback to simple text")
			_, err = w.Write([]byte(content))
		} else {
			err = diagnosticTmpl.Execute(w, content)
		}

		if err != nil {
			logger.V(2).Printf("failed to serve index.html: %v", err)
		}
	})

	router.HandleFunc("/diagnostic.zip", func(w http.ResponseWriter, r *http.Request) {
		hdr := w.Header()
		hdr.Add("Content-Type", "application/zip")

		if err := api.DiagnosticZip(w); err != nil {
			logger.V(1).Printf("failed to serve diagnostic.zip: %v", err)
		}
	})

	router.Handle("/static/*", http.StripPrefix("/static", &assetsFileServer{fs: http.FileServer(staticFolder)}))
	router.HandleFunc("/*", func(w http.ResponseWriter, r *http.Request) {
		var err error
		if indexTmpl == nil {
			_, err = w.Write(fallbackIndex)
		} else {
			err = indexTmpl.Execute(w, gloutonUIConfig{
				StaticCDNURL: api.StaticCDNURL,
			})
		}
		if err != nil {
			logger.V(2).Printf("fail to serve index.html: %v", err)
		}
	})

	api.router = router
}

// Run starts our API.
func (api *API) Run(ctx context.Context) error {
	api.init()

	srv := http.Server{
		Addr:    api.BindAddress,
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

	logger.Printf("Starting API on %s ✔️", api.BindAddress)
	logger.Printf("To access the local panel connect to http://%s 🌐", api.BindAddress)

	if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		return err
	}

	return nil
}
