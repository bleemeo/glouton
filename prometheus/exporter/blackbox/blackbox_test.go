package blackbox

import (
	"glouton/config"
	"glouton/logger"
	"reflect"
	"testing"
)

func TestConfigParsing(t *testing.T) {
	cfg := &config.Configuration{}
	logger.SetLevel(2)

	conf := `
agent:
  prober:
    config_file: "/home/nightmared/dev/test/example.yml"
    targets:
      - {url: "https://google.com", module: "http_2xx"}
      - {url: "https://inpt.fr", module: "dns", timeout: 5}
      - url: "http://neverssl.com"
        module: "http_2xx"
        timeout: 2`

	err := cfg.LoadByte([]byte(conf))

	if err != nil {
		t.Fatal(err)
	}

	blackboxConf, ok := GenConfig(cfg)
	if !ok {
		t.Fatalf("Couldn't parse the config")
	}

	expectedValue := &Options{
		BlackboxConfigFile: "/home/nightmared/dev/test/example.yml",
		// we assume parsing preserves the order, which seems to be the case
		Targets: []configTarget{
			{URL: "https://google.com", ModuleName: "http_2xx", Timeout: 0},
			{URL: "https://inpt.fr", ModuleName: "dns", Timeout: 5},
			{URL: "http://neverssl.com", ModuleName: "http_2xx", Timeout: 2},
		},
	}

	if !reflect.DeepEqual(blackboxConf, expectedValue) {
		t.Fatalf("Invalid config, expected '%+v', got '%+v'", expectedValue, blackboxConf)
	}
}
