// Copyright 2015-2026 Bleemeo
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
	"strings"

	"github.com/bleemeo/glouton/config"
	"github.com/bleemeo/glouton/discovery"
)

const (
	attrUsed               = "used"
	attrCollectionCount    = "CollectionCount"
	attrCollectionTime     = "CollectionTime"
	attrCount              = "Count"
	attrValue              = "Value"
	attrCompletedTaskCount = "CompletedTaskCount"
	attrPulls              = "Pulls"
	attrRequestCount       = "requestCount"
	attrProcessingTime     = "processingTime"
	metricJvmGC            = "jvm_gc"
	metricRequests         = "requests"
	metricRequestTime      = "request_time"
	typeNameName           = "name"
	typeNameKeyspace       = "keyspace"
	typeNameScope          = "scope"
	mbeanCatalinaGlobalReq = "Catalina:type=GlobalRequestProcessor,name=*"
	mbeanGCName            = "java.lang:type=GarbageCollector,name=*"
)

//nolint:gochecknoglobals
var (
	defaultGenericMetrics = []config.JmxMetric{
		{
			Name:      "jvm_heap_used",
			MBean:     "java.lang:type=Memory",
			Attribute: "HeapMemoryUsage",
			Path:      attrUsed,
		},
		{
			Name:      "jvm_non_heap_used",
			MBean:     "java.lang:type=Memory",
			Attribute: "NonHeapMemoryUsage",
			Path:      attrUsed,
		},
		{
			Name:      metricJvmGC,
			MBean:     mbeanGCName,
			Attribute: attrCollectionCount,
			Derive:    true,
			Sum:       true,
			TypeNames: []string{typeNameName},
		},
		{
			Name:      "jvm_gc_utilization",
			MBean:     mbeanGCName,
			Attribute: attrCollectionTime,
			Derive:    true,
			Sum:       true,
			TypeNames: []string{typeNameName},
			Scale:     0.1, // time is in ms/s. Convert in %
		},
	}

	defaultServiceMetrics = map[discovery.ServiceName][]config.JmxMetric{
		discovery.CassandraService: {
			{
				Name:      "read_requests_sum",
				MBean:     "org.apache.cassandra.metrics:type=ClientRequest,scope=Read,name=Latency",
				Attribute: attrCount,
				Derive:    true,
			},
			{
				Name:      "read_time_average",
				MBean:     "org.apache.cassandra.metrics:type=ClientRequest,scope=Read,name=TotalLatency",
				Attribute: attrCount,
				Scale:     0.000001, // convert from microsecond to second
				Derive:    true,
			},
			{
				Name:      "write_requests_sum",
				MBean:     "org.apache.cassandra.metrics:type=ClientRequest,scope=Write,name=Latency",
				Attribute: attrCount,
				Derive:    true,
			},
			{
				Name:      "write_time_average",
				MBean:     "org.apache.cassandra.metrics:type=ClientRequest,scope=Write,name=TotalLatency",
				Attribute: attrCount,
				Scale:     0.000001, // convert from microsecond to second
				Derive:    true,
			},
			{
				Name:      "bloom_filter_false_ratio_sum",
				MBean:     "org.apache.cassandra.metrics:type=Table,name=BloomFilterFalseRatio",
				Attribute: attrValue,
				Scale:     100, // convert from ratio (0 to 1) to percent
			},
			{
				Name:      "sstable_sum",
				MBean:     "org.apache.cassandra.metrics:type=Table,name=LiveSSTableCount",
				Attribute: attrValue,
			},
		},
		discovery.BitBucketService: {
			{
				Name:      "events",
				MBean:     "com.atlassian.bitbucket.thread-pools:name=EventThreadPool",
				Attribute: attrCompletedTaskCount,
				Derive:    true,
			},
			{
				Name:      "io_tasks",
				MBean:     "com.atlassian.bitbucket.thread-pools:name=IoPumpThreadPool",
				Attribute: attrCompletedTaskCount,
				Derive:    true,
			},
			{
				Name:      "tasks",
				MBean:     "com.atlassian.bitbucket.thread-pools:name=ScheduledThreadPool",
				Attribute: attrCompletedTaskCount,
				Derive:    true,
			},
			{
				Name:      "pulls",
				MBean:     "com.atlassian.bitbucket:name=ScmStatistics",
				Attribute: attrPulls,
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
				Name:      metricRequests,
				MBean:     mbeanCatalinaGlobalReq,
				Attribute: attrRequestCount,
				TypeNames: []string{typeNameName},
				Derive:    true,
				Sum:       true,
			},
			{
				Name:      metricRequestTime,
				MBean:     mbeanCatalinaGlobalReq,
				Attribute: attrProcessingTime,
				TypeNames: []string{typeNameName},
				Derive:    true,
				Sum:       true,
				Ratio:     metricRequests,
				Scale:     0.001, // convert from millisecond to second
			},
			{
				Name:      metricRequests,
				MBean:     "Tomcat:type=GlobalRequestProcessor,name=*",
				Attribute: attrRequestCount,
				TypeNames: []string{typeNameName},
				Derive:    true,
				Sum:       true,
			},
			{
				Name:      metricRequestTime,
				MBean:     "Tomcat:type=GlobalRequestProcessor,name=*",
				Attribute: attrProcessingTime,
				TypeNames: []string{typeNameName},
				Derive:    true,
				Sum:       true,
				Ratio:     metricRequests,
				Scale:     0.001, // convert from millisecond to second
			},
		},
		discovery.ConfluenceService: {
			{
				Name:      "last_index_time",
				MBean:     "Confluence:name=IndexingStatistics",
				Attribute: "LastElapsedMilliseconds",
				Scale:     0.001, // convert from millisecond to second
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
				Scale:     0.001, // convert from millisecond to second
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
				Name:      metricRequests,
				MBean:     "Standalone:type=GlobalRequestProcessor,name=*",
				Attribute: attrRequestCount,
				TypeNames: []string{typeNameName},
				Derive:    true,
				Sum:       true,
			},
			{
				Name:      metricRequestTime,
				MBean:     "Standalone:type=GlobalRequestProcessor,name=*",
				Attribute: attrProcessingTime,
				TypeNames: []string{typeNameName},
				Derive:    true,
				Sum:       true,
				Ratio:     metricRequests,
				Scale:     0.001, // convert from millisecond to second
			},
		},
		discovery.JIRAService: {
			{
				Name:      metricRequests,
				MBean:     mbeanCatalinaGlobalReq,
				Attribute: attrRequestCount,
				TypeNames: []string{typeNameName},
				Derive:    true,
				Sum:       true,
			},
			{
				Name:      metricRequestTime,
				MBean:     mbeanCatalinaGlobalReq,
				Attribute: attrProcessingTime,
				TypeNames: []string{typeNameName},
				Derive:    true,
				Sum:       true,
				Ratio:     metricRequests,
				Scale:     0.001, // convert from millisecond to second
			},
		},
		discovery.KafkaService: {
			{
				Name:      "topics_count",
				MBean:     "kafka.controller:type=KafkaController,name=GlobalTopicCount",
				Attribute: attrValue,
			},
			{
				Name:      "produce_requests_sum",
				MBean:     "kafka.server:type=BrokerTopicMetrics,name=TotalProduceRequestsPerSec",
				Attribute: attrCount,
				Derive:    true,
			},
			{
				Name:      "fetch_requests_sum",
				MBean:     "kafka.server:type=BrokerTopicMetrics,name=TotalFetchRequestsPerSec",
				Attribute: attrCount,
				Derive:    true,
			},
			{
				Name:      "produce_time_average",
				MBean:     "kafka.network:type=RequestMetrics,name=TotalTimeMs,request=Produce",
				Attribute: "Mean",
				Scale:     0.001, // convert from millisecond to second
			},
			{
				Name:      "fetch_time_average",
				MBean:     "kafka.network:type=RequestMetrics,name=TotalTimeMs,request=FetchConsumer",
				Attribute: "Mean",
				Scale:     0.001, // convert from millisecond to second
			},
		},
	}

	cassandraDetailedTableMetrics = []config.JmxMetric{
		{
			Name:      "bloom_filter_false_ratio",
			MBean:     "org.apache.cassandra.metrics:type=Table,keyspace={keyspace},scope={table},name=BloomFilterFalseRatio",
			Attribute: attrValue,
			TypeNames: []string{typeNameKeyspace, typeNameScope},
			Scale:     100,
		},
		{
			Name:      "sstable",
			MBean:     "org.apache.cassandra.metrics:type=Table,keyspace={keyspace},scope={table},name=LiveSSTableCount",
			Attribute: attrValue,
			TypeNames: []string{typeNameKeyspace, typeNameScope},
		},
		{
			Name:      "read_time",
			MBean:     "org.apache.cassandra.metrics:type=Table,keyspace={keyspace},scope={table},name=ReadTotalLatency",
			Attribute: attrCount,
			Scale:     0.000001, // convert from microsecond to second
			TypeNames: []string{typeNameKeyspace, typeNameScope},
			Derive:    true,
		},
		{
			Name:      "read_requests",
			MBean:     "org.apache.cassandra.metrics:type=Table,keyspace={keyspace},scope={table},name=ReadLatency",
			Attribute: attrCount,
			TypeNames: []string{typeNameKeyspace, typeNameScope},
			Derive:    true,
		},
		{
			Name:      "write_time",
			MBean:     "org.apache.cassandra.metrics:type=Table,keyspace={keyspace},scope={table},name=WriteTotalLatency",
			Attribute: attrCount,
			Scale:     0.000001, // convert from microsecond to second
			TypeNames: []string{typeNameKeyspace, typeNameScope},
			Derive:    true,
		},
		{
			Name:      "write_requests",
			MBean:     "org.apache.cassandra.metrics:type=Table,keyspace={keyspace},scope={table},name=WriteLatency",
			Attribute: attrCount,
			TypeNames: []string{typeNameKeyspace, typeNameScope},
			Derive:    true,
		},
	}

	kafkaDetailedTopicMetrics = []config.JmxMetric{
		{
			Name:      "produce_requests",
			MBean:     "kafka.server:type=BrokerTopicMetrics,name=TotalProduceRequestsPerSec,topic={topic}",
			Attribute: attrCount,
			TypeNames: []string{"topic"},
			Derive:    true,
		},
		{
			Name:      "fetch_requests",
			MBean:     "kafka.server:type=BrokerTopicMetrics,name=TotalFetchRequestsPerSec,topic={topic}",
			Attribute: attrCount,
			TypeNames: []string{"topic"},
			Derive:    true,
		},
	}
)

// GetJMXMetrics parses the jmx info and returns a list of JmxMetric struct.
func GetJMXMetrics(service discovery.Service) []config.JmxMetric {
	if !service.Active {
		return nil
	}

	if service.Config.JMXPort == 0 {
		return nil
	}

	if service.IPAddress == "" {
		return nil
	}

	metrics := service.Config.JMXMetrics
	metrics = append(metrics, defaultGenericMetrics...)
	metrics = append(metrics, defaultServiceMetrics[service.ServiceType]...)

	switch service.ServiceType { //nolint:exhaustive,nolintlint
	case discovery.CassandraService:
		for _, name := range service.Config.DetailedItems {
			part := strings.Split(name, ".")
			if len(part) == 2 {
				replacer := strings.NewReplacer(
					"{"+typeNameKeyspace+"}", part[0],
					"{table}", part[1],
				)

				for _, metric := range cassandraDetailedTableMetrics {
					metric.MBean = replacer.Replace(metric.MBean)
					metrics = append(metrics, metric)
				}
			}
		}
	case discovery.KafkaService:
		for _, topic := range service.Config.DetailedItems {
			replacer := strings.NewReplacer(
				"{topic}", topic,
			)

			for _, metric := range kafkaDetailedTopicMetrics {
				metric.MBean = replacer.Replace(metric.MBean)
				metrics = append(metrics, metric)
			}
		}
	}

	return metrics
}
