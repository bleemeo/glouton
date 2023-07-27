// Copyright 2015-2023 Bleemeo
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

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"glouton/config"
	"glouton/discovery"
	"glouton/facts"
	"glouton/logger"
	"glouton/prometheus/promql"
	"glouton/threshold"
	"glouton/types"
	"glouton/utils/archivewriter"
	"html/template"
	"io"
	"net/http"
	"net/http/pprof"
	"path"
	"strings"
	"time"

	"github.com/99designs/gqlgen/graphql/handler"
	"github.com/99designs/gqlgen/graphql/playground"
	"github.com/go-chi/chi"
	"github.com/rs/cors"
	_ "github.com/urfave/cli/v2" // Prevent go mod tidy from removing gqlgen dependencies
)

//go:embed static
var staticFolder embed.FS

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
	LocalUIDisabled    bool
	Endpoints          config.WebEndpoints
	MetricFormat       types.MetricFormat
	DB                 metricQueryable
	ContainerRuntime   containerInterface
	PsFact             *facts.ProcessProvider
	FactProvider       *facts.FactProvider
	Discovery          *discovery.Discovery
	AgentInfo          agentInterface
	PrometheurExporter http.Handler
	Threshold          *threshold.Registry
	DiagnosticPage     func(ctx context.Context) string
	DiagnosticArchive  func(ctx context.Context, w types.ArchiveWriter) error

	router http.Handler
}

type gloutonUIConfig struct {
	StaticCDNURL string
}

type assetsFileServer struct {
	fs http.Handler
}

func (f *assetsFileServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	r.URL.Path = path.Join("static/assets", r.URL.Path)

	// let the client browser decode the gzipped js files
	if strings.HasPrefix(r.URL.Path, "static/assets/js/") {
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

	fallbackIndex := []byte("Error while initializing local UI. See Glouton logs")

	var indexBody []byte

	indexFileName := "static/index.html"
	if api.LocalUIDisabled {
		indexFileName = "static/index_disabled.html"
	}

	indexFile, err := staticFolder.Open(indexFileName)
	if err == nil {
		indexBody, err = io.ReadAll(indexFile)
		indexFile.Close()
	}

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

	diagnosticFile, err := staticFolder.Open("static/diagnostic.html")
	if err == nil {
		diagnosticBody, err = io.ReadAll(diagnosticFile)
		diagnosticFile.Close()
	}

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
		content := api.DiagnosticPage(r.Context())

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

		zipFile := newZipWriter(w)
		defer zipFile.Close()

		if err := api.diagnosticArchive(r.Context(), zipFile); err != nil {
			logger.V(1).Printf("failed to serve diagnostic.zip (current file %s): %v", zipFile.CurrentFileName(), err)
		}
	})

	router.HandleFunc("/diagnostic.tar", func(w http.ResponseWriter, r *http.Request) {
		hdr := w.Header()
		hdr.Add("Content-Type", "application/x-tar")

		archive := newTarWriter(w)
		defer archive.Close()

		if err := api.diagnosticArchive(r.Context(), archive); err != nil {
			logger.V(1).Printf("failed to serve diagnostic.tar (current file %s): %v", archive.CurrentFileName(), err)
		}
	})

	router.HandleFunc("/diagnostic.txt", func(w http.ResponseWriter, r *http.Request) {
		hdr := w.Header()
		hdr.Add("Content-Type", "text/plain; charset=utf-8")

		archive := newTextArchive(w)
		defer archive.Close()

		if err := api.diagnosticArchive(r.Context(), archive); err != nil {
			logger.V(1).Printf("failed to serve diagnostic.txt (current file %s): %v", archive.CurrentFileName(), err)
		}
	})

	router.HandleFunc("/diagnostic.txt/*", func(w http.ResponseWriter, r *http.Request) {
		hdr := w.Header()
		hdr.Add("Content-Type", "text/plain; charset=utf-8")

		var archive types.ArchiveWriter

		subPath := strings.TrimPrefix(r.URL.Path, "/diagnostic.txt/")

		if strings.Contains(subPath, "*") {
			realArchive := newTextArchive(w)
			defer realArchive.Close()

			archive = archivewriter.NewFilterWriter(subPath, realArchive)
		} else {
			archive = archivewriter.NewSingleFileWriter(subPath, w)
		}

		if err := api.diagnosticArchive(r.Context(), archive); err != nil {
			logger.V(1).Printf("failed to serve diagnostic.txt (current file %s): %v", archive.CurrentFileName(), err)
		}
	})

	if api.Endpoints.DebugEnable {
		router.Handle("/debug/pprof/*", http.HandlerFunc(pprof.Index))
		router.Handle("/debug/pprof/cmdline", http.HandlerFunc(pprof.Cmdline))
		router.Handle("/debug/pprof/profile", http.HandlerFunc(pprof.Profile))
		router.Handle("/debug/pprof/symbol", http.HandlerFunc(pprof.Symbol))
		router.Handle("/debug/pprof/trace", http.HandlerFunc(pprof.Trace))
	}

	router.Handle("/static/*", http.StripPrefix("/static", &assetsFileServer{fs: http.FileServer(http.FS(staticFolder))}))
	router.HandleFunc("/*", func(w http.ResponseWriter, r *http.Request) {
		var err error
		if indexTmpl == nil {
			_, err = w.Write(fallbackIndex)
		} else {
			staticURL := api.StaticCDNURL
			if !strings.HasSuffix(staticURL, "/") {
				staticURL += "/"
			}

			err = indexTmpl.Execute(w, gloutonUIConfig{
				StaticCDNURL: staticURL,
			})
		}
		if err != nil {
			logger.V(2).Printf("fail to serve index.html: %v", err)
		}
	})

	api.router = router
}

func (api *API) diagnosticArchive(ctx context.Context, archive types.ArchiveWriter) error {
	if err := api.DiagnosticArchive(ctx, archive); err != nil {
		currentFile := archive.CurrentFileName()

		file, err2 := archive.Create("diagnostic-error.txt")
		if err2 != nil {
			return err
		}

		errFull := fmt.Errorf("writing file %s: %w", currentFile, err)

		fmt.Fprintf(file, "%s\n", errFull.Error())

		return errFull
	}

	return nil
}

// Run starts our API.
func (api *API) Run(ctx context.Context) error {
	api.init()

	srv := http.Server{
		Addr:              api.BindAddress,
		Handler:           api.router,
		ReadHeaderTimeout: 10 * time.Second,
	}

	idleConnsClosed := make(chan struct{})

	go func() {
		defer types.ProcessPanic()

		<-ctx.Done()

		// Shutdown context. It must outlive the parent context.
		subCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		if err := srv.Shutdown(subCtx); err != nil {
			logger.V(2).Printf("HTTP server Shutdown: %v", err)
		}

		close(idleConnsClosed)
	}()

	logger.Printf("Starting API on %s ✔️", api.BindAddress)

	if !api.LocalUIDisabled {
		logger.Printf("To access the local panel connect to http://%s 🌐", api.BindAddress)
	}

	err := srv.ListenAndServe()
	if !errors.Is(err, http.ErrServerClosed) {
		<-idleConnsClosed

		return nil
	}

	return err
}
