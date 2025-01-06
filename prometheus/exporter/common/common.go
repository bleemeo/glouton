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

package common

import (
	"regexp"
	"regexp/syntax"
)

// MergeREs merges together a list of regexps (this will match any pattern that matches at least one of
// the input regexps).
// the good news is that '^(^value$)$' will match 'value', so we can use the output of this function and give
// it to the collectors of windows_exporter and node_exporter (had this property been false, it would have
// been difficult to merge them, given the way the regexps are processed in windows_exporter:
// https://github.com/prometheus-community/windows_exporter/blob/21a02c4fbec4304f883ed7957bd81045d2f0c133/collector/logical_disk.go#L153)
func MergeREs(regexps []*regexp.Regexp) (string, error) {
	tmp := make([]string, 0, len(regexps))

	for _, re := range regexps {
		tmp = append(tmp, re.String())
	}

	return ReFromREs(tmp)
}

// ReFromPrefix returns a regular expression matching any prefixes in the input.
// e.g. ReFromPrefixes("eth"}) will match eth, eth0, ...
func ReFromPrefix(prefix string) (string, error) {
	re, err := syntax.Parse(prefix, syntax.Literal)

	return "^" + re.String(), err
}

// ReFromPathPrefix returns a regular expression matching any path prefix in the input.
// By path-prefix, we means that the path-prefix "/mnt" will match "/mnt", "/mnt/disk" but not "/mnt-disk"
// Only "/" is supported (e.g. only unix).
func ReFromPathPrefix(prefix string) (string, error) {
	re, err := syntax.Parse(prefix, syntax.Literal)

	return "^" + re.String() + "($|/)", err
}

// ReFromREs return an RE matching any of the input REs.
func ReFromREs(input []string) (string, error) {
	var err error

	res := make([]*syntax.Regexp, len(input))

	for i, v := range input {
		res[i], err = syntax.Parse(v, syntax.Perl)
		if err != nil {
			return "", err
		}
	}

	re := syntax.Regexp{
		Op:    syntax.OpAlternate,
		Flags: syntax.Perl,
		Sub:   res,
	}

	return re.String(), nil
}

// CompileREs compiles a list of regexps, returning an error and the empty list in case one of the regexps fails.
func CompileREs(regexps []string) (out []*regexp.Regexp, err error) {
	out = make([]*regexp.Regexp, len(regexps))

	for index, v := range regexps {
		out[index], err = regexp.Compile(v)
		if err != nil {
			return
		}
	}

	return
}
