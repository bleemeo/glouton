package blackbox

import (
	"glouton/config"
	"glouton/logger"
	"reflect"
)

// blackbox_exporter options, see https://github.com/prometheus/blackbox_exporter/blob/master/config/config.go
type Options struct {
	Targets            []ConfigTarget
	BlackboxConfigFile string
}

type ConfigTarget struct {
	URL        string
	ModuleName string
	Timeout    int
}

// castStringMap assigns values from a map to a list of pointers.
// The user provides a map and a list of intertwined field names and pointers, like ["field1", ptr1, "field2", ptr2].
// This function returns the list of invalid or missing fields, and a boolean indicating wether this function call is unsound
// (e.g. an odd number of arguments in the 'fields' variable).
// A missing field is not considered as an error, as the program may have default values for it, but you can detect it by checking the return value.
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
func genConfigTarget(conf map[string]interface{}) (opts ConfigTarget, ok bool) {
	target := ConfigTarget{}
	// honestly the easiest (and probably the cleanest too) way would be to use the 'yaml.v3' package
	invalidFields, ok := castStringMap(conf, "module", &target.ModuleName, "url", &target.URL, "timeout", &target.Timeout)
	if len(invalidFields) != 0 {
		logger.Printf("The following fields are missing or invalid on the target probe %v: %v", conf, invalidFields)
	}
	return target, ok && len(invalidFields) == 0
}

// GenConfig generates a config we can ingest into glouton.prometheus.exporter.blackbox.
func GenConfig(conf *config.Configuration) (opts *Options, ok bool) {
	// the prober feature is enabled if we have configured some targets in the configuration
	proberTargetsConf, proberEnabled := conf.Get("agent.prober.targets")
	if !proberEnabled {
		logger.V(1).Println("'agent.prober.targets' not defined your config, will not start blackbox_exporter.")
		return nil, false
	}

	proberTargets, ok := proberTargetsConf.([]interface{})
	if !ok {
		logger.Printf("Invalid configuration for 'agent.prober.targets', will not attempt to start probes.")
		return nil, false
	}
	// no targets configured -> no probes -> no reason to enable this subsystem
	if len(proberTargets) == 0 {
		logger.V(1).Println("Empty probe target list, will not start blackbox_exporter.")
		return nil, false
	}

	// NOTE: consider embedding the blackbox_exporter config file inside glouton.conf ?
	targets := []ConfigTarget{}
	for _, val := range proberTargets {
		target, ok := val.(map[string]interface{})
		if !ok {
			logger.Printf("Invalid configuration for the probe target '%v', will not attempt to start probes.", val)
			return nil, false
		}
		configuredTarget, ok := genConfigTarget(target)
		if !ok {
			logger.Printf("Invalid configuration for the probe target '%v', will not attempt to start probes.", target)
			return nil, false
		}
		targets = append(targets, configuredTarget)
	}

	configFile := conf.String("agent.prober.config_file")
	// TODO: default conf
	if configFile == "" {
		logger.Printf("Probes are configured but you haven't supplied the path to your blackbox_exporter configuration in 'agent.prober.config_file'.")
		return nil, false
	}

	blackboxOptions := &Options{
		Targets:            targets,
		BlackboxConfigFile: configFile,
	}

	logger.V(2).Println("Probes configuration successfully parsed.")

	return blackboxOptions, true
}
