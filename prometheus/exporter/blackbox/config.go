package blackbox

import (
	"glouton/config"
	"glouton/logger"
	"reflect"
)

// Options is the subset of glouton config that deals with probes.
type Options struct {
	Targets            []configTarget
	BlackboxConfigFile string
}

type configTarget struct {
	URL        string
	ModuleName string
	Timeout    int
}

// castStringMap assigns values from a map to a list of pointers.
// The user provides a map and a list of intertwined field names and pointers, like ["field1", ptr1, "field2", ptr2].
// This function returns the list of invalid or missing fields, and a boolean indicating wether this function call is unsound
// (e.g. an odd number of arguments was supplied in the 'fields' variable).
// A missing field is not considered as an error, as the program may have default values for it, but you can detect it by checking
// the return value.
// However, it is not possible to distinguish missing values from invalid values with this design.
func castStringMap(conf map[string]interface{}, fields ...interface{}) (invalidFields []string, ok bool) {
	if len(fields)%2 == 1 {
		logger.Printf("Invalid code in glouton: odd number of arguments supplied for reading probe targets")
		return invalidFields, false
	}

	for i := 0; i < len(fields)/2; i++ {
		val, present := conf[fields[i*2].(string)]
		var convertible bool
		if present {
			convertible = reflect.TypeOf(val).ConvertibleTo(reflect.TypeOf(fields[i*2+1]).Elem())
		}
		if !present || !convertible {
			invalidFields = append(invalidFields, fields[2*i].(string))
			continue
		}
		reflect.ValueOf(fields[i*2+1]).Elem().Set(reflect.ValueOf(val).Convert(reflect.TypeOf(fields[i*2+1]).Elem()))
	}
	return invalidFields, true
}

// genConfigTarget read the configuration for a sole target, extracted for glouton's config, and parse it.
// Honestly the easiest (and probably the cleanest too) way would be to use the 'yaml.v3' package, but I have yet to look into it.
func genConfigTarget(conf map[string]interface{}) (opts configTarget, ok bool) {
	target := configTarget{}
	invalidFields, ok := castStringMap(conf, "module", &target.ModuleName, "url", &target.URL, "timeout", &target.Timeout)
	if !ok {
		return target, false
	}
	// list of fields that the user can omit in the configuration
	acceptableMissingFields := []string{"timeout"}
	missingFields := []string{}
OuterLoop:
	for _, v := range invalidFields {
		for _, e := range acceptableMissingFields {
			if e == v {
				continue OuterLoop
			}
		}
		missingFields = append(missingFields, v)
	}
	if len(missingFields) != 0 {
		logger.Printf("The following fields are missing or invalid on the target probe %v: %v", conf, missingFields)
		return target, false
	}
	return target, true
}

// GenConfig generates a config we can ingest into glouton.prometheus.exporter.blackbox.
func GenConfig(conf *config.Configuration) (opts *Options, ok bool) {
	// the prober feature is enabled if we have configured some targets in the configuration
	proberTargetsConf, proberEnabled := conf.Get("agent.prober.targets")
	if !proberEnabled {
		logger.V(1).Println("blackbox_exporter: 'agent.prober.targets' not defined your config.")
		return nil, false
	}

	proberTargets, ok := proberTargetsConf.([]interface{})
	if !ok {
		logger.Printf("blackbox_exporter: Invalid configuration for 'agent.prober.targets'.")
		return nil, false
	}
	// no targets configured -> no probes -> no reason to enable this subsystem
	if len(proberTargets) == 0 {
		logger.V(1).Println("blackbox_exporter: Empty probe target list.")
		return nil, false
	}

	// NOTE: consider embedding the blackbox_exporter config file inside glouton.conf ?
	targets := []configTarget{}
	for _, val := range proberTargets {
		target, ok := val.(map[string]interface{})
		if !ok {
			logger.Printf("blackbox_exporter: Invalid configuration for the probe target '%v'.", val)
			return nil, false
		}
		configuredTarget, ok := genConfigTarget(target)
		if !ok {
			logger.Printf("blackbox_exporter: Invalid configuration for the probe target '%v'.", target)
			return nil, false
		}
		targets = append(targets, configuredTarget)
	}

	configFile := conf.String("agent.prober.config_file")
	// TODO: default conf file
	if configFile == "" {
		logger.Printf("blackbox_exporter: Probes are configured but you haven't supplied the path to your blackbox_exporter configuration in 'agent.prober.config_file'.")
		return nil, false
	}

	blackboxOptions := &Options{
		Targets:            targets,
		BlackboxConfigFile: configFile,
	}

	logger.V(2).Println("blackbox_exporter: Probes configuration successfully parsed.")

	return blackboxOptions, true
}
