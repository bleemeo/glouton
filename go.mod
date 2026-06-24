module github.com/bleemeo/glouton

go 1.26.3

require (
	dario.cat/mergo v1.0.2
	github.com/AstromechZA/etcpwdparse v0.0.0-20170319193008-f0e5f0779716
	github.com/alecthomas/kingpin/v2 v2.4.0
	github.com/bleemeo/bleemeo-go v0.9.0
	github.com/bmatcuk/doublestar/v4 v4.10.0
	github.com/cenkalti/backoff/v5 v5.0.3
	github.com/cespare/xxhash/v2 v2.3.0
	github.com/containerd/cgroups/v3 v3.1.3
	github.com/containerd/containerd v1.7.28
	github.com/containerd/containerd/api v1.11.1
	github.com/containerd/containerd/v2 v2.3.2
	github.com/containerd/errdefs v1.0.0
	github.com/containerd/platforms v1.0.0-rc.4
	github.com/containerd/typeurl/v2 v2.3.0
	github.com/eclipse/paho.mqtt.golang v1.5.1
	github.com/fsnotify/fsnotify v1.10.1
	github.com/getsentry/sentry-go v0.47.0
	github.com/go-chi/chi/v5 v5.3.0
	github.com/go-chi/render v1.0.3
	github.com/go-viper/mapstructure/v2 v2.5.0
	github.com/google/go-cmp v0.7.0
	github.com/google/shlex v0.0.0-20191202100458-e7afc7fbc510
	github.com/google/uuid v1.6.0
	github.com/grafana/regexp v0.0.0-20250905093917-f7b3be9d1853
	github.com/influxdata/telegraf v1.39.0
	github.com/influxdata/toml v0.0.0-20251106153700-c381e153d076
	github.com/json-iterator/go v1.1.12
	github.com/knadh/koanf v1.5.0
	github.com/knadh/koanf/v2 v2.3.5
	github.com/miekg/dns v1.1.72
	github.com/mitchellh/copystructure v1.2.0
	github.com/moby/moby/api v1.55.0
	github.com/moby/moby/client v0.5.0
	github.com/ncabatoff/process-exporter v0.8.7
	github.com/open-telemetry/opentelemetry-collector-contrib/pkg/stanza v0.154.0
	github.com/open-telemetry/opentelemetry-collector-contrib/processor/filterprocessor v0.154.0
	github.com/open-telemetry/opentelemetry-collector-contrib/receiver/filelogreceiver v0.154.0
	github.com/open-telemetry/opentelemetry-collector-contrib/receiver/journaldreceiver v0.154.0
	github.com/opencontainers/go-digest v1.0.0
	github.com/opencontainers/image-spec v1.1.1
	github.com/opencontainers/runtime-spec v1.3.0
	github.com/prometheus-community/windows_exporter v0.31.7
	github.com/prometheus/blackbox_exporter v0.28.0
	github.com/prometheus/client_golang v1.23.2
	github.com/prometheus/client_model v0.6.2
	github.com/prometheus/common v0.69.0
	github.com/prometheus/node_exporter v1.11.1
	github.com/prometheus/procfs v0.20.1
	github.com/prometheus/prometheus v0.312.0
	github.com/rs/cors v1.11.1
	github.com/shirou/gopsutil/v4 v4.26.5
	github.com/vishvananda/netlink v1.3.1
	github.com/vmware/govmomi v0.54.1
	github.com/yusufpapurcu/wmi v1.2.4
	go.opentelemetry.io/collector/component v1.61.0
	go.opentelemetry.io/collector/config/configgrpc v0.155.0
	go.opentelemetry.io/collector/config/confighttp v0.155.0
	go.opentelemetry.io/collector/config/confignet v1.61.0
	go.opentelemetry.io/collector/config/configoptional v1.61.0
	go.opentelemetry.io/collector/consumer v1.61.0
	go.opentelemetry.io/collector/exporter v1.61.0
	go.opentelemetry.io/collector/exporter/exporterhelper v0.155.0
	go.opentelemetry.io/collector/extension/xextension v0.155.0
	go.opentelemetry.io/collector/pdata v1.61.0
	go.opentelemetry.io/collector/processor v1.61.0
	go.opentelemetry.io/collector/processor/batchprocessor v0.155.0
	go.opentelemetry.io/collector/processor/processorhelper v0.155.0
	go.opentelemetry.io/collector/receiver v1.61.0
	go.opentelemetry.io/collector/receiver/otlpreceiver v0.155.0
	go.opentelemetry.io/otel v1.44.0
	go.opentelemetry.io/otel/metric v1.44.0
	go.opentelemetry.io/otel/trace v1.44.0
	go.uber.org/zap v1.28.0
	golang.org/x/oauth2 v0.36.0
	golang.org/x/sync v0.21.0
	golang.org/x/sys v0.46.0
	golang.org/x/text v0.38.0
	google.golang.org/protobuf v1.36.12-0.20260120151049-f2248ac996af
	gopkg.in/ini.v1 v1.67.3
	gopkg.in/yaml.v3 v3.0.1
	k8s.io/api v0.36.2
	k8s.io/apiextensions-apiserver v0.36.2
	k8s.io/apimachinery v0.36.2
	k8s.io/client-go v0.36.2
	sigs.k8s.io/yaml v1.6.0
)

require (
	cel.dev/expr v0.25.2 // indirect
	cloud.google.com/go/auth v0.20.0 // indirect
	cloud.google.com/go/auth/oauth2adapt v0.2.8 // indirect
	cloud.google.com/go/compute/metadata v0.9.0 // indirect
	cyphar.com/go-pathrs v0.2.5 // indirect
	filippo.io/edwards25519 v1.2.0 // indirect
	github.com/Azure/azure-sdk-for-go/sdk/azcore v1.22.0 // indirect
	github.com/Azure/azure-sdk-for-go/sdk/azidentity v1.14.0 // indirect
	github.com/Azure/azure-sdk-for-go/sdk/internal v1.12.0 // indirect
	github.com/Azure/go-ntlmssp v0.1.1 // indirect
	github.com/AzureAD/microsoft-authentication-library-for-go v1.7.2 // indirect
	github.com/Masterminds/semver/v3 v3.5.0 // indirect
	github.com/Microsoft/go-winio v0.6.3-0.20251027160822-ad3df93bed29 // indirect
	github.com/Microsoft/hcsshim v0.15.0-rc.2 // indirect
	github.com/ajg/form v1.7.1 // indirect
	github.com/alecthomas/participle/v2 v2.1.4 // indirect
	github.com/alecthomas/units v0.0.0-20240927000941-0f3dac36c52b // indirect
	github.com/andybalholm/brotli v1.2.1 // indirect
	github.com/antchfx/xmlquery v1.5.1 // indirect
	github.com/antchfx/xpath v1.3.6 // indirect
	github.com/antithesishq/antithesis-sdk-go v0.7.0 // indirect
	github.com/antlr4-go/antlr/v4 v4.13.1 // indirect
	github.com/awnumar/memcall v0.5.0 // indirect
	github.com/awnumar/memguard v0.23.0 // indirect
	github.com/aws/aws-sdk-go-v2 v1.42.0 // indirect
	github.com/aws/aws-sdk-go-v2/config v1.32.25 // indirect
	github.com/aws/aws-sdk-go-v2/credentials v1.19.24 // indirect
	github.com/aws/aws-sdk-go-v2/feature/ec2/imds v1.18.29 // indirect
	github.com/aws/aws-sdk-go-v2/internal/configsources v1.4.29 // indirect
	github.com/aws/aws-sdk-go-v2/internal/endpoints/v2 v2.7.29 // indirect
	github.com/aws/aws-sdk-go-v2/internal/v4a v1.4.30 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/accept-encoding v1.13.12 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/presigned-url v1.13.29 // indirect
	github.com/aws/aws-sdk-go-v2/service/signin v1.2.0 // indirect
	github.com/aws/aws-sdk-go-v2/service/sso v1.31.3 // indirect
	github.com/aws/aws-sdk-go-v2/service/ssooidc v1.36.6 // indirect
	github.com/aws/aws-sdk-go-v2/service/sts v1.43.3 // indirect
	github.com/aws/smithy-go v1.27.2 // indirect
	github.com/bboreham/go-loser v0.0.0-20230920113527-fcc2c21820a3 // indirect
	github.com/beevik/ntp v1.5.0 // indirect
	github.com/benbjohnson/clock v1.3.5 // indirect
	github.com/beorn7/perks v1.0.1 // indirect
	github.com/bmatcuk/doublestar/v3 v3.0.0 // indirect
	github.com/cenkalti/backoff/v4 v4.3.0 // indirect
	github.com/cenkalti/backoff/v6 v6.0.1 // indirect
	github.com/clipperhouse/uax29/v2 v2.7.0 // indirect
	github.com/compose-spec/compose-go v1.20.2 // indirect
	github.com/containerd/continuity v0.5.0 // indirect
	github.com/containerd/errdefs/pkg v0.3.0 // indirect
	github.com/containerd/fifo v1.1.0 // indirect
	github.com/containerd/log v0.1.0 // indirect
	github.com/containerd/plugin v1.1.0 // indirect
	github.com/containerd/ttrpc v1.2.8 // indirect
	github.com/coreos/go-semver v0.3.1 // indirect
	github.com/coreos/go-systemd/v22 v22.7.0 // indirect
	github.com/cyphar/filepath-securejoin v0.7.0 // indirect
	github.com/davecgh/go-spew v1.1.2-0.20180830191138-d8f796af33cc // indirect
	github.com/dennwc/btrfs v0.0.0-20260222081608-edfb8b9e4f55 // indirect
	github.com/dennwc/ioctl v1.0.1 // indirect
	github.com/dennwc/varint v1.0.0 // indirect
	github.com/dgryski/go-rendezvous v0.0.0-20200823014737-9f7001d12a5f // indirect
	github.com/dimchansky/utfbom v1.1.1 // indirect
	github.com/distribution/reference v0.6.0 // indirect
	github.com/docker/go-connections v0.7.0 // indirect
	github.com/docker/go-units v0.5.0 // indirect
	github.com/ebitengine/purego v0.10.1 // indirect
	github.com/edsrzf/mmap-go v1.2.1-0.20241212181136-fad1cd13edbd // indirect
	github.com/elastic/go-grok v0.3.1 // indirect
	github.com/elastic/lunes v0.2.2 // indirect
	github.com/ema/qdisc v1.0.0 // indirect
	github.com/expr-lang/expr v1.17.8 // indirect
	github.com/facette/natsort v0.0.0-20181210072756-2cd4dd1e2dcb // indirect
	github.com/fatih/color v1.19.0 // indirect
	github.com/fatih/structs v1.1.0 // indirect
	github.com/felixge/httpsnoop v1.1.0 // indirect
	github.com/foxboron/go-tpm-keyfiles v0.0.0-20260427185012-515ba073c4c1 // indirect
	github.com/fxamacker/cbor/v2 v2.9.2 // indirect
	github.com/go-asn1-ber/asn1-ber v1.5.8-0.20250403174932-29230038a667 // indirect
	github.com/go-ldap/ldap/v3 v3.4.13 // indirect
	github.com/go-logr/logr v1.4.3 // indirect
	github.com/go-logr/stdr v1.2.2 // indirect
	github.com/go-ole/go-ole v1.3.0 // indirect
	github.com/go-openapi/analysis v0.25.2 // indirect
	github.com/go-openapi/errors v0.22.8 // indirect
	github.com/go-openapi/jsonpointer v0.23.1 // indirect
	github.com/go-openapi/jsonreference v0.21.6 // indirect
	github.com/go-openapi/loads v0.24.0 // indirect
	github.com/go-openapi/spec v0.22.6 // indirect
	github.com/go-openapi/strfmt v0.26.3 // indirect
	github.com/go-openapi/swag v0.26.1 // indirect
	github.com/go-openapi/swag/cmdutils v0.26.1 // indirect
	github.com/go-openapi/swag/conv v0.26.1 // indirect
	github.com/go-openapi/swag/fileutils v0.26.1 // indirect
	github.com/go-openapi/swag/jsonname v0.26.1 // indirect
	github.com/go-openapi/swag/jsonutils v0.26.1 // indirect
	github.com/go-openapi/swag/loading v0.26.1 // indirect
	github.com/go-openapi/swag/mangling v0.26.1 // indirect
	github.com/go-openapi/swag/netutils v0.26.1 // indirect
	github.com/go-openapi/swag/stringutils v0.26.1 // indirect
	github.com/go-openapi/swag/typeutils v0.26.1 // indirect
	github.com/go-openapi/swag/yamlutils v0.26.1 // indirect
	github.com/go-openapi/validate v0.26.0 // indirect
	github.com/go-redis/redis/v8 v8.11.5 // indirect
	github.com/go-sql-driver/mysql v1.10.0 // indirect
	github.com/gobwas/glob v0.2.3 // indirect
	github.com/goccy/go-json v0.10.6 // indirect
	github.com/godbus/dbus/v5 v5.2.2 // indirect
	github.com/gofrs/uuid/v5 v5.4.0 // indirect
	github.com/gogo/protobuf v1.3.2 // indirect
	github.com/golang-jwt/jwt/v5 v5.3.1 // indirect
	github.com/golang/groupcache v0.0.0-20241129210726-2c02b8208cf8 // indirect
	github.com/golang/snappy v1.0.0 // indirect
	github.com/google/cel-go v0.28.1 // indirect
	github.com/google/go-tpm v0.9.9-0.20260124013517-8f8f42cba0de // indirect
	github.com/google/s2a-go v0.1.9 // indirect
	github.com/googleapis/enterprise-certificate-proxy v0.3.17 // indirect
	github.com/googleapis/gax-go/v2 v2.22.0 // indirect
	github.com/gorilla/websocket v1.5.4-0.20250319132907-e064f32e3674 // indirect
	github.com/hashicorp/go-envparse v0.1.0 // indirect
	github.com/hashicorp/go-version v1.9.0 // indirect
	github.com/hashicorp/golang-lru v1.0.2 // indirect
	github.com/hashicorp/golang-lru/v2 v2.0.7 // indirect
	github.com/hodgesds/perf-utils v0.7.0 // indirect
	github.com/iancoleman/strcase v0.3.0 // indirect
	github.com/illumos/go-kstat v0.0.0-20210513183136-173c9b0a9973 // indirect
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/pgx/v5 v5.10.0 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	github.com/jaypipes/ghw v0.24.0 // indirect
	github.com/jaypipes/pcidb v1.1.1 // indirect
	github.com/jedib0t/go-pretty/v6 v6.8.1 // indirect
	github.com/jonboulle/clockwork v0.5.0 // indirect
	github.com/jpillora/backoff v1.0.0 // indirect
	github.com/jsimonetti/rtnetlink/v2 v2.2.0 // indirect
	github.com/kballard/go-shellquote v0.0.0-20180428030007-95032a82bc51 // indirect
	github.com/klauspost/compress v1.18.6 // indirect
	github.com/klauspost/cpuid/v2 v2.3.0 // indirect
	github.com/klauspost/pgzip v1.2.6 // indirect
	github.com/kylelemons/godebug v1.1.0 // indirect
	github.com/leodido/go-syslog/v4 v4.5.0 // indirect
	github.com/leodido/ragel-machinery v0.0.0-20190525184631-5f46317e436b // indirect
	github.com/lufia/iostat v1.2.1 // indirect
	github.com/lufia/plan9stats v0.0.0-20260330125221-c963978e514e // indirect
	github.com/magefile/mage v1.17.2 // indirect
	github.com/mattn/go-colorable v0.1.15 // indirect
	github.com/mattn/go-isatty v0.0.22 // indirect
	github.com/mattn/go-runewidth v0.0.24 // indirect
	github.com/mattn/go-xmlrpc v0.0.3 // indirect
	github.com/mdlayher/ethtool v0.6.1 // indirect
	github.com/mdlayher/genetlink v1.4.0 // indirect
	github.com/mdlayher/netlink v1.11.2 // indirect
	github.com/mdlayher/socket v0.6.1 // indirect
	github.com/mdlayher/wifi v0.8.0 // indirect
	github.com/minio/highwayhash v1.0.4 // indirect
	github.com/mitchellh/reflectwalk v1.0.2 // indirect
	github.com/moby/docker-image-spec v1.3.1 // indirect
	github.com/moby/locker v1.0.1 // indirect
	github.com/moby/sys/mountinfo v0.7.2 // indirect
	github.com/moby/sys/sequential v0.7.0 // indirect
	github.com/moby/sys/signal v0.7.1 // indirect
	github.com/moby/sys/user v0.4.0 // indirect
	github.com/moby/sys/userns v0.1.0 // indirect
	github.com/modern-go/concurrent v0.0.0-20180306012644-bacd9c7ef1dd // indirect
	github.com/modern-go/reflect2 v1.0.3-0.20250322232337-35a7c28c31ee // indirect
	github.com/montanaflynn/stats v0.9.0 // indirect
	github.com/munnerz/goautoneg v0.0.0-20191010083416-a7dc8b61c822 // indirect
	github.com/mwitkow/go-conntrack v0.0.0-20190716064945-2f068394615f // indirect
	github.com/naoina/go-stringutil v0.1.0 // indirect
	github.com/nats-io/jwt/v2 v2.8.2 // indirect
	github.com/nats-io/nats-server/v2 v2.14.2 // indirect
	github.com/nats-io/nkeys v0.4.16 // indirect
	github.com/nats-io/nuid v1.0.1 // indirect
	github.com/ncabatoff/go-seq v0.0.0-20180805175032-b08ef85ed833 // indirect
	github.com/oklog/ulid/v2 v2.1.1 // indirect
	github.com/open-telemetry/opentelemetry-collector-contrib/internal/coreinternal v0.154.0 // indirect
	github.com/open-telemetry/opentelemetry-collector-contrib/internal/filter v0.154.0 // indirect
	github.com/open-telemetry/opentelemetry-collector-contrib/pkg/ottl v0.154.0 // indirect
	github.com/open-telemetry/opentelemetry-collector-contrib/pkg/pdatautil v0.154.0 // indirect
	github.com/opencontainers/selinux v1.15.1 // indirect
	github.com/peterbourgon/unixtransport v0.0.7 // indirect
	github.com/pierrec/lz4/v4 v4.1.27 // indirect
	github.com/pkg/browser v0.0.0-20240102092130-5ac0b6a4141c // indirect
	github.com/pkg/errors v0.9.1 // indirect
	github.com/pmezard/go-difflib v1.0.1-0.20181226105442-5d4384ee4fb2 // indirect
	github.com/power-devops/perfstat v0.0.0-20240221224432-82ca36839d55 // indirect
	github.com/prometheus-community/go-runit v0.1.0 // indirect
	github.com/prometheus/alertmanager v0.33.0 // indirect
	github.com/prometheus/client_golang/exp v0.0.0-20260624042014-28914d017fba // indirect
	github.com/prometheus/otlptranslator v1.0.0 // indirect
	github.com/prometheus/sigv4 v0.4.1 // indirect
	github.com/quic-go/qpack v0.6.0 // indirect
	github.com/quic-go/quic-go v0.60.0 // indirect
	github.com/robbiet480/go.nut v0.0.0-20240622015809-60e196249c53 // indirect
	github.com/safchain/ethtool v0.7.0 // indirect
	github.com/sirupsen/logrus v1.9.4 // indirect
	github.com/spf13/pflag v1.0.10 // indirect
	github.com/stretchr/objx v0.5.3 // indirect
	github.com/stretchr/testify v1.11.1 // indirect
	github.com/tidwall/gjson v1.19.0 // indirect
	github.com/tidwall/match v1.2.0 // indirect
	github.com/tidwall/pretty v1.2.1 // indirect
	github.com/tidwall/tinylru v1.2.1 // indirect
	github.com/tidwall/wal v1.2.1 // indirect
	github.com/tklauser/go-sysconf v0.4.0 // indirect
	github.com/tklauser/numcpus v0.12.0 // indirect
	github.com/twmb/murmur3 v1.1.8 // indirect
	github.com/ua-parser/uap-go v0.0.0-20260529044130-17c35e68e58c // indirect
	github.com/valyala/fastjson v1.6.10 // indirect
	github.com/vishvananda/netns v0.0.5 // indirect
	github.com/x448/float16 v0.8.4 // indirect
	github.com/xdg-go/pbkdf2 v1.0.0 // indirect
	github.com/xdg-go/scram v1.2.0 // indirect
	github.com/xdg-go/stringprep v1.0.4 // indirect
	github.com/xhit/go-str2duration/v2 v2.1.0 // indirect
	github.com/youmark/pkcs8 v0.0.0-20240726163527-a2c0da244d78 // indirect
	github.com/zeebo/xxh3 v1.1.0 // indirect
	go.mongodb.org/mongo-driver v1.17.9 // indirect
	go.opencensus.io v0.24.0 // indirect
	go.opentelemetry.io/auto/sdk v1.2.1 // indirect
	go.opentelemetry.io/collector v0.155.0 // indirect
	go.opentelemetry.io/collector/client v1.61.0 // indirect
	go.opentelemetry.io/collector/component/componentstatus v0.155.0 // indirect
	go.opentelemetry.io/collector/config/configauth v1.61.0 // indirect
	go.opentelemetry.io/collector/config/configcompression v1.61.0 // indirect
	go.opentelemetry.io/collector/config/configmiddleware v1.61.0 // indirect
	go.opentelemetry.io/collector/config/configopaque v1.61.0 // indirect
	go.opentelemetry.io/collector/config/configretry v1.61.0 // indirect
	go.opentelemetry.io/collector/config/configtls v1.61.0 // indirect
	go.opentelemetry.io/collector/confmap v1.61.0 // indirect
	go.opentelemetry.io/collector/confmap/xconfmap v0.155.0 // indirect
	go.opentelemetry.io/collector/consumer/consumererror v0.155.0 // indirect
	go.opentelemetry.io/collector/consumer/consumertest v0.155.0 // indirect
	go.opentelemetry.io/collector/consumer/xconsumer v0.155.0 // indirect
	go.opentelemetry.io/collector/extension v1.61.0 // indirect
	go.opentelemetry.io/collector/extension/extensionauth v1.61.0 // indirect
	go.opentelemetry.io/collector/extension/extensionmiddleware v0.155.0 // indirect
	go.opentelemetry.io/collector/featuregate v1.61.0 // indirect
	go.opentelemetry.io/collector/internal/componentalias v0.155.0 // indirect
	go.opentelemetry.io/collector/internal/sharedcomponent v0.155.0 // indirect
	go.opentelemetry.io/collector/internal/telemetry v0.155.0 // indirect
	go.opentelemetry.io/collector/pdata/pprofile v0.155.0 // indirect
	go.opentelemetry.io/collector/pdata/xpdata v0.155.0 // indirect
	go.opentelemetry.io/collector/pipeline v1.61.0 // indirect
	go.opentelemetry.io/collector/pipeline/xpipeline v0.155.0 // indirect
	go.opentelemetry.io/collector/processor/processorhelper/xprocessorhelper v0.155.0 // indirect
	go.opentelemetry.io/collector/processor/xprocessor v0.155.0 // indirect
	go.opentelemetry.io/collector/receiver/receiverhelper v0.155.0 // indirect
	go.opentelemetry.io/collector/receiver/xreceiver v0.155.0 // indirect
	go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc v0.69.0 // indirect
	go.opentelemetry.io/contrib/instrumentation/net/http/httptrace/otelhttptrace v0.69.0 // indirect
	go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp v0.69.0 // indirect
	go.step.sm/crypto v0.83.0 // indirect
	go.uber.org/atomic v1.11.0 // indirect
	go.uber.org/goleak v1.3.0 // indirect
	go.uber.org/multierr v1.11.0 // indirect
	go.yaml.in/yaml/v2 v2.4.4 // indirect
	go.yaml.in/yaml/v3 v3.0.4 // indirect
	golang.org/x/crypto v0.53.0 // indirect
	golang.org/x/exp v0.0.0-20260611194520-c48552f49976 // indirect
	golang.org/x/mod v0.37.0 // indirect
	golang.org/x/net v0.56.0 // indirect
	golang.org/x/term v0.44.0 // indirect
	golang.org/x/time v0.15.0 // indirect
	golang.org/x/tools v0.46.0 // indirect
	gonum.org/v1/gonum v0.17.0 // indirect
	google.golang.org/api v0.286.0 // indirect
	google.golang.org/genproto/googleapis/api v0.0.0-20260622175928-b703f567277d // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260622175928-b703f567277d // indirect
	google.golang.org/grpc v1.81.1 // indirect
	gopkg.in/inf.v0 v0.9.1 // indirect
	howett.net/plist v1.0.2-0.20250314012144-ee69052608d9 // indirect
	k8s.io/klog/v2 v2.140.0 // indirect
	k8s.io/kube-openapi v0.0.0-20260624041617-8f3fa4921821 // indirect
	k8s.io/utils v0.0.0-20260617174310-a95e086a2553 // indirect
	sigs.k8s.io/json v0.0.0-20250730193827-2d320260d730 // indirect
	sigs.k8s.io/randfill v1.0.0 // indirect
	sigs.k8s.io/structured-merge-diff/v6 v6.4.0 // indirect
)
