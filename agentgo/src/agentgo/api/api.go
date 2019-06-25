package api

import (
	"log"
	"net/http"
	"os"

	"github.com/99designs/gqlgen/handler"
	"github.com/vektah/gqlparser/gqlerror"
	"agentgo/types"
)

var globalDb storeInterface
var globalAPI *API

const defaultPort = "8015"

type storeInterface interface {
	Metrics(filters map[string]string) (result []types.Metric, err error)
}

// API : Structure that contains API's port
type API struct {
	Port string
}

// New : Function that instanciate a new API's port from environment variable or from a default port
func New(db storeInterface) *API {
	globalDb = db
	port := os.Getenv("PORT")
	if port == "" {
		port = defaultPort
	}
	api := &API{Port: port}
	globalAPI = api
	http.HandleFunc("/metrics", api.promExporter)
	return api
}

// Run : Starts our API
func (api API) Run() {
	http.Handle("/", handler.Playground("GraphQL playground", "/graphql"))
	http.Handle("/graphql", handler.GraphQL(NewExecutableSchema(Config{Resolvers: &Resolver{}})))
	log.Fatal(http.ListenAndServe(":"+api.Port, nil))
}

func (api *API) GetMetrics(input Labels) ([]*Metric, error) {
	if globalDb == nil {
		return nil, gqlerror.Errorf("Can not retrieve metrics at this moment. Please try later")
	}
	metricFilters := map[string]string{}
	if len(input.Labels) > 0 {
		for _, filter := range input.Labels {
			metricFilters[filter.Key] = filter.Value
		}
	}
	metrics, _ := globalDb.Metrics(metricFilters)
	metricsRes := []*Metric{}
	for _, metric := range metrics {
		metricRes := &Metric{}
		labels := metric.Labels()
		for key, value := range labels {
			label := &Label{Key: key, Value: value}
			metricRes.Labels = append(metricRes.Labels, label)
		}
		metricsRes = append(metricsRes, metricRes)
	}
	return metricsRes, nil
}
