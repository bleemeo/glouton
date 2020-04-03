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

package jmxtrans

import (
	"encoding/json"
	"glouton/discovery"
	"glouton/logger"
	"strings"
)

type jmxMetric struct {
	Name      string
	MBean     string
	Attribute string
	Path      string
	Derive    bool
	Sum       bool
	TypeNames []string
	Scale     float64
	Ratio     string
}

// nolint: gochecknoglobals
var defaultGenericMetrics = []jmxMetric{
	{
		Name:      "jvm_heap_used",
		MBean:     "java.lang:type=Memory",
		Attribute: "HeapMemoryUsage",
		Path:      "used",
	},
	{
		Name:      "jvm_non_heap_used",
		MBean:     "java.lang:type=Memory",
		Attribute: "NonHeapMemoryUsage",
		Path:      "used",
	},
	{
		Name:      "jvm_gc",
		MBean:     "java.lang:type=GarbageCollector,name=*",
		Attribute: "CollectionCount",
		Derive:    true,
		Sum:       true,
		TypeNames: []string{"name"},
	},
	{
		Name:      "jvm_gc_time",
		MBean:     "java.lang:type=GarbageCollector,name=*",
		Attribute: "CollectionTime",
		Derive:    true,
		Sum:       true,
		TypeNames: []string{"name"},
	},
	{
		Name:      "jvm_gc_utilization",
		MBean:     "java.lang:type=GarbageCollector,name=*",
		Attribute: "CollectionTime",
		Derive:    true,
		Sum:       true,
		TypeNames: []string{"name"},
		Scale:     0.1, // time is in ms/s. Convert in %
	},
}

// nolint: gochecknoglobals
var defaultServiceMetrics = map[discovery.ServiceName][]jmxMetric{
	discovery.CassandraService: {
		{
			Name:      "read_requests",
			MBean:     "org.apache.cassandra.metrics:type=ClientRequest,scope=Read,name=Latency",
			Attribute: "Count",
			Derive:    true,
		},
		{
			Name:      "read_time",
			MBean:     "org.apache.cassandra.metrics:type=ClientRequest,scope=Read,name=TotalLatency",
			Attribute: "Count",
			Scale:     0.001, // convert from microsecond to millisecond
			Derive:    true,
		},
		{
			Name:      "write_requests",
			MBean:     "org.apache.cassandra.metrics:type=ClientRequest,scope=Write,name=Latency",
			Attribute: "Count",
			Derive:    true,
		},
		{
			Name:      "write_time",
			MBean:     "org.apache.cassandra.metrics:type=ClientRequest,scope=Write,name=TotalLatency",
			Attribute: "Count",
			Scale:     0.001, // convert from microsecond to millisecond
			Derive:    true,
		},
		{
			Name:      "bloom_filter_false_ratio",
			MBean:     "org.apache.cassandra.metrics:type=Table,name=BloomFilterFalseRatio",
			Attribute: "Value",
			Scale:     100, // convert from ratio (0 to 1) to percent
		},
		{
			Name:      "sstable",
			MBean:     "org.apache.cassandra.metrics:type=Table,name=LiveSSTableCount",
			Attribute: "Value",
		},
	},
	discovery.BitBucketService: {
		{
			Name:      "events",
			MBean:     "com.atlassian.bitbucket.thread-pools:name=EventThreadPool",
			Attribute: "CompletedTaskCount",
			Derive:    true,
		},
		{
			Name:      "io_tasks",
			MBean:     "com.atlassian.bitbucket.thread-pools:name=IoPumpThreadPool",
			Attribute: "CompletedTaskCount",
			Derive:    true,
		},
		{
			Name:      "tasks",
			MBean:     "com.atlassian.bitbucket.thread-pools:name=ScheduledThreadPool",
			Attribute: "CompletedTaskCount",
			Derive:    true,
		},
		{
			Name:      "pulls",
			MBean:     "com.atlassian.bitbucket:name=ScmStatistics",
			Attribute: "Pulls",
			Derive:    true,
		},
		{
			Name:      "pushes",
			MBean:     "com.atlassian.bitbucket:name=ScmStatistics",
			Attribute: "Pushes",
			Derive:    true,
		},
		{
			Name:      "queued_scm_clients",
			MBean:     "com.atlassian.bitbucket:name=HostingTickets",
			Attribute: "QueuedRequests",
		},
		{
			Name:      "queued_scm_commands",
			MBean:     "com.atlassian.bitbucket:name=CommandTickets",
			Attribute: "QueuedRequests",
		},
		{
			Name:      "queued_events",
			MBean:     "com.atlassian.bitbucket:name=EventStatistics",
			Attribute: "QueueLength",
		},
		{
			Name:      "ssh_connections",
			MBean:     "com.atlassian.bitbucket:name=SshSessions",
			Attribute: "SessionCreatedCount",
			Derive:    true,
		},
		{
			Name:      "requests",
			MBean:     "Catalina:type=GlobalRequestProcessor,name=*",
			Attribute: "requestCount",
			TypeNames: []string{"name"},
			Derive:    true,
			Sum:       true,
		},
		{
			Name:      "request_time",
			MBean:     "Catalina:type=GlobalRequestProcessor,name=*",
			Attribute: "processingTime",
			TypeNames: []string{"name"},
			Derive:    true,
			Sum:       true,
			Ratio:     "requests",
		},
		{
			Name:      "requests",
			MBean:     "Tomcat:type=GlobalRequestProcessor,name=*",
			Attribute: "requestCount",
			TypeNames: []string{"name"},
			Derive:    true,
			Sum:       true,
		},
		{
			Name:      "request_time",
			MBean:     "Tomcat:type=GlobalRequestProcessor,name=*",
			Attribute: "processingTime",
			TypeNames: []string{"name"},
			Derive:    true,
			Sum:       true,
			Ratio:     "requests",
		},
	},
	discovery.ConfluenceService: {
		{
			Name:      "last_index_time",
			MBean:     "Confluence:name=IndexingStatistics",
			Attribute: "LastElapsedMilliseconds",
		},
		{
			Name:      "queued_index_tasks",
			MBean:     "Confluence:name=IndexingStatistics",
			Attribute: "TaskQueueLength",
		},
		{
			Name:      "db_query_time",
			MBean:     "Confluence:name=SystemInformation",
			Attribute: "DatabaseExampleLatency",
		},
		{
			Name:      "queued_mails",
			MBean:     "Confluence:name=MailTaskQueue",
			Attribute: "TasksSize",
		},
		{
			Name:      "queued_error_mails",
			MBean:     "Confluence:name=MailTaskQueue",
			Attribute: "ErrorQueueSize",
		},
		{
			Name:      "requests",
			MBean:     "Standalone:type=GlobalRequestProcessor,name=*",
			Attribute: "requestCount",
			TypeNames: []string{"name"},
			Derive:    true,
			Sum:       true,
		},
		{
			Name:      "request_time",
			MBean:     "Standalone:type=GlobalRequestProcessor,name=*",
			Attribute: "processingTime",
			TypeNames: []string{"name"},
			Derive:    true,
			Sum:       true,
			Ratio:     "requests",
		},
	},
	discovery.JIRAService: {
		{
			Name:      "requests",
			MBean:     "Catalina:type=GlobalRequestProcessor,name=*",
			Attribute: "requestCount",
			TypeNames: []string{"name"},
			Derive:    true,
			Sum:       true,
		},
		{
			Name:      "request_time",
			MBean:     "Catalina:type=GlobalRequestProcessor,name=*",
			Attribute: "processingTime",
			TypeNames: []string{"name"},
			Derive:    true,
			Sum:       true,
			Ratio:     "requests",
		},
	},
}

// nolint: gochecknoglobals
var cassandraDetailedTableMetrics = []jmxMetric{
	{
		Name:      "bloom_filter_false_ratio",
		MBean:     "org.apache.cassandra.metrics:type=Table,keyspace={keyspace},scope={table},name=BloomFilterFalseRatio",
		Attribute: "Value",
		TypeNames: []string{"keyspace", "scope"},
		Scale:     100,
	},
	{
		Name:      "sstable",
		MBean:     "org.apache.cassandra.metrics:type=Table,keyspace={keyspace},scope={table},name=LiveSSTableCount",
		Attribute: "Value",
		TypeNames: []string{"keyspace", "scope"},
	},
	{
		Name:      "read_time",
		MBean:     "org.apache.cassandra.metrics:type=Table,keyspace={keyspace},scope={table},name=ReadTotalLatency",
		Attribute: "Count",
		TypeNames: []string{"keyspace", "scope"},
		Derive:    true,
	},
	{
		Name:      "read_requests",
		MBean:     "org.apache.cassandra.metrics:type=Table,keyspace={keyspace},scope={table},name=ReadLatency",
		Attribute: "Count",
		TypeNames: []string{"keyspace", "scope"},
		Derive:    true,
	},
	{
		Name:      "write_time",
		MBean:     "org.apache.cassandra.metrics:type=Table,keyspace={keyspace},scope={table},name=WriteTotalLatency",
		Attribute: "Count",
		TypeNames: []string{"keyspace", "scope"},
		Derive:    true,
	},
	{
		Name:      "write_requests",
		MBean:     "org.apache.cassandra.metrics:type=Table,keyspace={keyspace},scope={table},name=WriteLatency",
		Attribute: "Count",
		TypeNames: []string{"keyspace", "scope"},
		Derive:    true,
	},
}

func getJMXMetrics(service discovery.Service) []jmxMetric {
	var result []jmxMetric

	if service.ExtraAttributes["jmx_metrics"] != "" {
		err := json.Unmarshal([]byte(service.ExtraAttributes["jmx_metrics"]), &result)
		if err != nil {
			logger.V(1).Printf("unable to read \"jmx_metrics\" for service %s", err, service.String())

			result = nil
		}
	}

	result = append(result, defaultGenericMetrics...)
	result = append(result, defaultServiceMetrics[service.ServiceType]...)

	if service.ServiceType == discovery.CassandraService {
		var detailedTables []string

		err := json.Unmarshal([]byte(service.ExtraAttributes["cassandra_detailed_tables"]), &detailedTables)
		if err == nil && len(detailedTables) > 0 {
			for _, name := range detailedTables {
				part := strings.Split(name, ".")
				if len(part) == 2 {
					replacer := strings.NewReplacer(
						"{keyspace}", part[0],
						"{table}", part[1],
					)

					for _, metric := range cassandraDetailedTableMetrics {
						metric.MBean = replacer.Replace(metric.MBean)
						result = append(result, metric)
					}
				}
			}
		}
	}

	return result
}
