module glouton

go 1.12

require (
	github.com/99designs/gqlgen v0.11.3
	github.com/AstromechZA/etcpwdparse v0.0.0-20170319193008-f0e5f0779716
	github.com/Azure/go-ansiterm v0.0.0-20170929234023-d6e3b3328b78 // indirect
	github.com/Microsoft/go-winio v0.4.15-0.20190919025122-fc70bd9a86b5 // indirect
	github.com/StackExchange/wmi v0.0.0-20190523213315-cbe66965904d
	github.com/containerd/containerd v1.3.4 // indirect
	github.com/docker/distribution v2.7.1+incompatible // indirect
	github.com/docker/docker v1.4.2-0.20200228174753-40b2b4b08306
	github.com/docker/go-connections v0.4.0 // indirect
	github.com/docker/go-units v0.4.0 // indirect
	github.com/eclipse/paho.mqtt.golang v1.2.1-0.20200121105743-0d940dd29fd2
	github.com/go-chi/chi v4.1.1+incompatible
	github.com/go-kit/kit v0.10.0
	github.com/go-ole/go-ole v1.2.4 // indirect
	github.com/go-redis/redis v6.15.8+incompatible // indirect
	github.com/go-sql-driver/mysql v1.5.0 // indirect
	github.com/gobuffalo/packr/v2 v2.7.0 // Keep v2.7.0 because go run -race is broken with v2.7.1
	github.com/godbus/dbus v0.0.0-20190422162347-ade71ed3457e // indirect
	github.com/gofrs/uuid v3.3.0+incompatible // indirect
	github.com/gogo/protobuf v1.3.1
	github.com/golang/protobuf v1.4.2 // indirect
	github.com/google/go-cmp v0.4.0
	github.com/google/shlex v0.0.0-20191202100458-e7afc7fbc510
	github.com/googleapis/gnostic v0.3.1 // indirect
	github.com/gorilla/websocket v1.4.2 // indirect
	github.com/grobie/gomemcache v0.0.0-20180201122607-1f779c573665
	github.com/hashicorp/golang-lru v0.5.4 // indirect
	github.com/imdario/mergo v0.3.9 // indirect
	github.com/influxdata/influxdb1-client v0.0.0-20200515024757-02f0bf5dbca3
	github.com/influxdata/telegraf v1.14.3
	github.com/influxdata/toml v0.0.0-20190415235208-270119a8ce65
	github.com/jackc/pgx v3.6.2+incompatible // indirect
	github.com/karrick/godirwalk v1.15.6 // indirect
	github.com/mdlayher/wifi v0.0.0-20200527114002-84f0b9457fdd // indirect
	github.com/mitchellh/mapstructure v1.3.1 // indirect
	github.com/morikuni/aec v1.0.0 // indirect
	github.com/ncabatoff/process-exporter v0.7.1
	github.com/opencontainers/go-digest v1.0.0 // indirect
	github.com/prometheus-community/windows_exporter v0.13.0
	github.com/prometheus/blackbox_exporter v0.17.0
	github.com/prometheus/client_golang v1.6.0
	github.com/prometheus/client_model v0.2.0
	github.com/prometheus/common v0.10.0
	github.com/prometheus/node_exporter v1.0.0
	github.com/prometheus/procfs v0.1.1
	github.com/prometheus/prometheus v1.8.2-0.20200213233353-b90be6f32a33
	github.com/rs/cors v1.7.0
	github.com/shirou/gopsutil v2.20.7-0.20200724130941-7e94bb8bcde0+incompatible
	github.com/shopspring/decimal v1.2.0 // indirect
	github.com/sirupsen/logrus v1.6.0 // indirect
	github.com/stretchr/testify v1.6.0 // indirect
	github.com/tidwall/gjson v1.6.0 // indirect
	github.com/tidwall/pretty v1.0.1 // indirect
	github.com/vektah/gqlparser v1.3.1
	github.com/vektah/gqlparser/v2 v2.0.1
	github.com/vishvananda/netlink v1.1.0
	github.com/vishvananda/netns v0.0.0-20200520041808-52d707b772fe // indirect
	golang.org/x/sys v0.0.0-20200523222454-059865788121
	golang.org/x/time v0.0.0-20200416051211-89c76fbcd5d1 // indirect
	google.golang.org/appengine v1.6.6 // indirect
	google.golang.org/genproto v0.0.0-20200528191852-705c0b31589b // indirect
	google.golang.org/grpc v1.29.1 // indirect
	gopkg.in/alecthomas/kingpin.v2 v2.2.6
	gopkg.in/ini.v1 v1.57.0
	gopkg.in/mgo.v2 v2.0.0-20190816093944-a6b53ec6cb22 // indirect
	gopkg.in/yaml.v3 v3.0.0-20200615113413-eeeca48fe776
	gotest.tools/v3 v3.0.2 // indirect
	k8s.io/api v0.18.3
	k8s.io/apimachinery v0.18.3
	k8s.io/client-go v0.18.3
	k8s.io/utils v0.0.0-20200529193333-24a76e807f40 // indirect
	sigs.k8s.io/yaml v1.2.0
)

replace github.com/ncabatoff/process-exporter => github.com/PierreF/process-exporter v0.6.1-0.20200610182819-0fb770c071f3
