// Copyright 2015-2025 Bleemeo
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

package logprocessing

// Shared test constants used across multiple test files.
const (
	testAttrServiceName  = "service.name"
	testServiceSrv1      = "srv-1"
	testServiceSrv2      = "srv-2"
	testStreamStdout     = "stdout"
	testStreamStderr     = "stderr"
	testAttrLogIOStream  = "log.iostream"
	testAttrKeyResAttr   = "key_res_attr"
	testServiceApache    = "apache_server"
	testContainerCtr1    = "ctr-1"
	testContainerCtr2    = "ctr-2"
	testRouteServiceName = "resource['service.name']"
	testFieldAdd         = "add"
	testFieldName        = "field"
	testFieldValue       = "value"
	testFieldKey         = "key"
	testFieldType        = "type"
	testFieldInclude     = "include"
	testFieldRegex       = "regex"
	testFieldParseFrom   = "parse_from"
	testFieldLayout      = "layout"

	testAttrHTTPMethod = "http.request.method"
	testAttrHTTPStatus = "http.response.status_code"
	testAttrDBSystem   = "db.system.name"
	testAttrMsgSystem  = "messaging.system"
	testMethodGET      = "GET"
	testAttrMsg        = "msg"
	testDBMySQL        = "mysql"
	testDBKafka        = "kafka"
	testDBRedis        = "redis"
	testDBMongoDB      = "mongodb"
	testMsgRabbitMQ    = "rabbitmq"

	testServiceNginx       = "nginx"
	testContainerNginx1    = "Nginx-1"
	testContainerIDNgx1    = "ngx-1"
	testServiceApacheHTTPD = "apache"
	testContainerApp1      = "app-1"

	testResourceKey      = "resource.key"
	testProp             = "prop"
	testFmtApacheAccess  = "apache_access"
	testFmtApacheError   = "apache_error"
	testFilterExclude    = "exclude"
	testFilterMatchType  = "match_type"
	testFilterBodies     = "bodies"
	testFilterLogRecord  = "log_record"
	testFmtPostgresql    = "postgresql"
	testFmtMariaDB       = "mariadb"
	testCustomSvc        = "custom-svc"
	testDyn              = "dyn"
	testCustomRes        = "custom-res"
	testAttrLogFileName  = "log.file.name"
	testAttrLogFilePath  = "log.file.path"
	testAttrHostName     = "host.name"
	testHostname         = "myhostname"
	testKeyAttrValue     = "key attribute value"
	testOpID1            = "op-1"
	testOpID2            = "op-2"
	testRegexParser      = "regex_parser"
	testRegexTimePattern = `^(?P<time>\[.*?\])$`
	testTimeParser       = "time_parser"
	testParseFromTime    = "attributes.time"
	testTimeFmtLayout    = "[%d/%b/%Y:%H:%M:%S %z]"
	testFmt1             = "fmt-1"
	testResServiceName   = "resource['service_name']"
	testFmt2             = "fmt-2"
	testOnErrorSend      = "send"
	testFollowName       = "--follow=name"
	testRegexp           = "regexp"
)
