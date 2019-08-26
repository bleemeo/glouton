package config

import (
	"io/ioutil"
	"reflect"
	"testing"
)

const (
	simpleYaml = `
agent:
    facts_file: facts.yaml
    installation_format: installation_format

logging:
    level: INFO

nested:
    key:
        also:
            work: yes
`
	mergeOne = `
d1: 1
remplaced: 1
sub_dict:
  d1: 1
  remplaced: 1
nested:
  sub_dict:
    d1: 1
    remplaced: 1
`
	mergeTwo = `
d2: 2
remplaced: 2
sub_dict:
  d2: 2
  remplaced: 2
nested:
  sub_dict:
    d2: 2
    remplaced: 2
`
)

func TestString(t *testing.T) {
	cfg := Configuration{}
	err := cfg.LoadByte([]byte(simpleYaml))
	if err != nil {
		t.Error(err)
	}

	cases := []struct {
		Key  string
		Want string
	}{
		{Key: "agent.facts_file", Want: "facts.yaml"},
		{Key: "agent.installation_format", Want: "installation_format"},
		{Key: "logging.level", Want: "INFO"},
		{Key: "nested.key.also.work", Want: "yes"},
		{Key: "not.found", Want: ""},
		{Key: "logging.notfound", Want: ""},
	}
	for _, c := range cases {
		got := cfg.String(c.Key)
		if c.Want != got {
			t.Errorf("String(%#v) = %#v, want %#v", c.Key, got, c.Want)
		}
	}
}

func TestMerge(t *testing.T) {
	cfg := Configuration{}
	err := cfg.LoadByte([]byte(mergeOne))
	if err != nil {
		t.Error(err)
	}
	err = cfg.LoadByte([]byte(mergeTwo))
	if err != nil {
		t.Error(err)
	}

	cases := []struct {
		Key  string
		Want string
	}{
		{Key: "d1", Want: "1"},
		{Key: "d2", Want: "2"},
		{Key: "remplaced", Want: "2"},
		{Key: "sub_dict.d1", Want: "1"},
		{Key: "sub_dict.d2", Want: "2"},
		{Key: "sub_dict.remplaced", Want: "2"},
		{Key: "nested.sub_dict.d1", Want: "1"},
		{Key: "nested.sub_dict.d2", Want: "2"},
		{Key: "nested.sub_dict.remplaced", Want: "2"},
	}
	for _, c := range cases {
		got := cfg.String(c.Key)
		if c.Want != got {
			t.Errorf("String(%#v) = %#v, want %#v", c.Key, got, c.Want)
		}
	}
}

func TestData(t *testing.T) {
	cfg := Configuration{}
	data, err := ioutil.ReadFile("testdata/main.conf")
	if err != nil {
		t.Error(err)
	}
	err = cfg.LoadByte(data)
	if err != nil {
		t.Error(err)
	}
	err = cfg.LoadDirectory("testdata/conf.d")
	if err != nil {
		t.Error(err)
	}

	cases := []struct {
		Key  string
		Want string
	}{
		{Key: "main_conf_loaded", Want: "yes"},
		{Key: "first_conf_loaded", Want: "yes"},
		{Key: "second_conf_loaded", Want: "yes"},
		{Key: "overridden_value", Want: "second"},
		{Key: "merged_dict.main", Want: "1"},
		{Key: "merged_dict.first", Want: "yes"},
		{Key: "sub_section.nested", Want: "<nil>"},
		{Key: "telegraf.statsd.enabled", Want: "<nil>"},
	}
	for _, c := range cases {
		got := cfg.String(c.Key)
		if c.Want != got {
			t.Errorf("String(%#v) = %#v, want %#v", c.Key, got, c.Want)
		}
	}
	got, err := cfg.Get("merged_list")
	if err != nil {
		t.Error(err)
	}
	want := []interface{}{
		"duplicated between main.conf & second.conf",
		"item from main.conf",
		"item from first.conf",
		"item from second.conf",
		"duplicated between main.conf & second.conf",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("merged_list = %v, want %v", got, want)
	}
	// assert len(warnings) == 1
}

func TestSet(t *testing.T) {
	cfg := Configuration{}
	cfg.Set("test-int", 5)
	cfg.Set("test-str", "string")
	cfg.Set("test-change-type", "string")
	cfg.Set("test-int", 42)
	cfg.Set("test-change-type", 9)
	cfg.Set("test.sub.list", []int{})
	cfg.Set("test.sub.dict", map[string]interface{}{"temp": 28.5})
	cfg.Set("test.sub.int", 5)
	cfg.Set("test.sub.nil", nil)

	cases := []struct {
		key  string
		want interface{}
	}{
		{
			key:  "test-int",
			want: 42,
		},
		{
			key:  "test-str",
			want: "string",
		},
		{
			key:  "test-change-type",
			want: 9,
		},
		{
			key:  "test.sub.list",
			want: []int{},
		},
		{
			key:  "test.sub.dict",
			want: map[string]interface{}{"temp": 28.5},
		},
		{
			key:  "test.sub.dict.temp",
			want: 28.5,
		},
		{
			key: "test.sub",
			want: map[string]interface{}{
				"dict": map[string]interface{}{"temp": 28.5},
				"list": []int{},
				"int":  5,
				"nil":  nil,
			},
		},
	}
	for _, c := range cases {
		got, err := cfg.Get(c.key)
		if err != nil {
			t.Errorf("cfg.Get(%#v) failed: %v", c.key, err)
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("cfg.Get(%#v) == %v, want %v", c.key, got, c.want)
		}
	}
}

func TestLoadEnv(t *testing.T) {
	envs := map[string]string{
		"ENV_NAME_1":        "something",
		"AGENT_API_PORT":    "8015",
		"AGENT_API_ENABLED": "yes",
		"API_ENABLED":       "false",
		"EXTRA_ENV":         "not-used",
	}
	lookupEnv := func(envName string) (string, bool) {
		value, ok := envs[envName]
		return value, ok
	}
	cfg := Configuration{lookupEnv: lookupEnv}

	loadCases := []struct {
		envName   string
		varType   ValueType
		key       string
		wantFound bool
	}{
		{
			envName:   "ENV_NAME_1",
			varType:   TypeString,
			key:       "name1",
			wantFound: true,
		},
		{
			envName:   "AGENT_API_PORT",
			varType:   TypeInteger,
			key:       "api.port",
			wantFound: true,
		},
		{
			envName:   "AGENT_API_ADDRESS",
			varType:   TypeString,
			key:       "api.address",
			wantFound: false,
		},
		{
			envName:   "API_ENABLED",
			varType:   TypeBoolean,
			key:       "api.enabled",
			wantFound: true,
		},
		{
			envName:   "AGENT_API_ENABLED",
			varType:   TypeBoolean,
			key:       "api.enabled",
			wantFound: true,
		},
		{
			envName:   "AGENT_API_PORT2",
			varType:   TypeString,
			key:       "api.port",
			wantFound: false,
		},
	}
	cases := []struct {
		key       string
		wantFound bool
		want      interface{}
	}{
		{
			key:       "name1",
			wantFound: true,
			want:      "something",
		},
		{
			key:       "api.port",
			wantFound: true,
			want:      8015,
		},
		{
			key:       "api.address",
			wantFound: false,
		},
		{
			key:       "api.enabled",
			wantFound: true,
			want:      true,
		},
	}

	for _, c := range loadCases {
		found, err := cfg.LoadEnv(c.key, c.varType, c.envName)
		if err != nil {
			t.Errorf("LoadEnv(%v) failed: %v", c.envName, err)
		}
		if found != c.wantFound {
			t.Errorf("LoadEnv(%v) == %v, want %v", c.envName, found, c.wantFound)
		}
	}
	for _, c := range cases {
		got, err := cfg.Get(c.key)
		if c.wantFound {
			if err != nil {
				t.Errorf("Get(%v) failed: %v", c.key, err)
			}
			if got != c.want {
				t.Errorf("Get(%v) == %#v, want %#v", c.key, got, c.want)
			}
		} else {
			if err == nil {
				t.Errorf("Get(%v) == %v, want IsNotFound error", c.key, got)
			} else if !IsNotFound(err) {
				t.Errorf("Get(%v) failed: %v", c.key, err)
			}

		}
	}
}
