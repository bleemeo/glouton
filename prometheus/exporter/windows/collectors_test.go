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

package windows

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/bleemeo/glouton/config"
)

const (
	windowsExporterModule = "github.com/prometheus-community/windows_exporter"
	// registryFile holds the map of every collector windows_exporter provides,
	// which is what collector.Available() returns.
	registryFile = "pkg/collector/map.go"
	registryVar  = "BuildersWithFlags"
	// minExpectedCollectors guards against silently passing if the registry moved or
	// changed shape: windows_exporter ships about 50 collectors, far more than this.
	minExpectedCollectors = 20
)

// Test_defaultCollectorsExistUpstream is the non-Windows counterpart of
// Test_defaultCollectorsAreAvailable.
//
// It can't call collector.Available(): every windows_exporter package is behind a
// "//go:build windows" constraint, so it doesn't even compile on Linux/macOS, where
// our tests run. Instead it reads the collector names from the registry map in the
// windows_exporter source, in the module cache.
func Test_defaultCollectorsExistUpstream(t *testing.T) {
	available := upstreamCollectorNames(t)

	for _, name := range config.DefaultConfig().Agent.WindowsExporter.Collectors {
		if !slices.Contains(available, name) {
			t.Errorf(
				"default windows_exporter collector %q doesn't exist, available collectors are %v",
				name, available,
			)
		}
	}
}

// upstreamCollectorNames returns the collector names registered by the windows_exporter
// version we depend on.
//
// The registry maps a name to its builder, and each name is the "Name" constant of the
// collector package: pagefile.Name: NewBuilderWithFlags(pagefile.NewWithFlags). So we
// read the map keys, then the constant each key refers to.
func upstreamCollectorNames(t *testing.T) []string {
	t.Helper()

	moduleDir := moduleDir(t, windowsExporterModule)

	fset := token.NewFileSet()

	file, err := parser.ParseFile(fset, filepath.Join(moduleDir, filepath.FromSlash(registryFile)), nil, 0)
	if err != nil {
		t.Fatalf("can't parse %s of %s: %v", registryFile, windowsExporterModule, err)
	}

	// Package name (or alias) of each import, to resolve the map keys.
	importPaths := make(map[string]string, len(file.Imports))

	for _, spec := range file.Imports {
		importPath, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			t.Fatalf("can't parse import %s: %v", spec.Path.Value, err)
		}

		name := path.Base(importPath)
		if spec.Name != nil {
			name = spec.Name.Name
		}

		importPaths[name] = importPath
	}

	registry := findRegistry(t, file)

	names := make([]string, 0, len(registry.Elts))

	for _, element := range registry.Elts {
		keyValue, ok := element.(*ast.KeyValueExpr)
		if !ok {
			t.Fatalf("%s of %s contains a %T, want a *ast.KeyValueExpr", registryVar, windowsExporterModule, element)
		}

		// Both parts of the pagefile.Name key.
		selector, ok := keyValue.Key.(*ast.SelectorExpr)
		if !ok {
			t.Fatalf("%s key is a %T, want a *ast.SelectorExpr", registryVar, keyValue.Key)
		}

		collectorPkg, ok := selector.X.(*ast.Ident)
		if !ok {
			t.Fatalf("%s key is qualified by a %T, want an *ast.Ident", registryVar, selector.X)
		}

		importPath, ok := importPaths[collectorPkg.Name]
		if !ok {
			t.Fatalf("%s key %s.%s doesn't match any import", registryVar, collectorPkg.Name, selector.Sel.Name)
		}

		collectorDir := filepath.Join(moduleDir, filepath.FromSlash(strings.TrimPrefix(importPath, windowsExporterModule+"/")))
		names = append(names, constantString(t, collectorDir, selector.Sel.Name))
	}

	if len(names) < minExpectedCollectors {
		t.Fatalf(
			"only found %d collectors in %s of %s, the registry layout probably changed",
			len(names), registryFile, windowsExporterModule,
		)
	}

	slices.Sort(names)

	return names
}

// findRegistry returns the composite literal assigned to the registry variable.
func findRegistry(t *testing.T, file *ast.File) *ast.CompositeLit {
	t.Helper()

	for _, decl := range file.Decls {
		genDecl, ok := decl.(*ast.GenDecl)
		if !ok || genDecl.Tok != token.VAR {
			continue
		}

		for _, spec := range genDecl.Specs {
			valueSpec, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}

			for i, name := range valueSpec.Names {
				if name.Name != registryVar || i >= len(valueSpec.Values) {
					continue
				}

				literal, ok := valueSpec.Values[i].(*ast.CompositeLit)
				if !ok {
					t.Fatalf("%s is a %T, want a *ast.CompositeLit", registryVar, valueSpec.Values[i])
				}

				return literal
			}
		}
	}

	t.Fatalf("no %s variable in %s of %s", registryVar, registryFile, windowsExporterModule)

	return nil
}

// constantString returns the value of the string constant declared in the package at dir.
func constantString(t *testing.T, dir string, constName string) string {
	t.Helper()

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("can't read %s: %v", dir, err)
	}

	fset := token.NewFileSet()

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}

		file, err := parser.ParseFile(fset, filepath.Join(dir, entry.Name()), nil, 0)
		if err != nil {
			t.Fatalf("can't parse %s: %v", filepath.Join(dir, entry.Name()), err)
		}

		for _, decl := range file.Decls {
			genDecl, ok := decl.(*ast.GenDecl)
			if !ok || genDecl.Tok != token.CONST {
				continue
			}

			for _, spec := range genDecl.Specs {
				valueSpec, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}

				for i, name := range valueSpec.Names {
					if name.Name != constName || i >= len(valueSpec.Values) {
						continue
					}

					literal, ok := valueSpec.Values[i].(*ast.BasicLit)
					if !ok || literal.Kind != token.STRING {
						t.Fatalf("%s.%s isn't a string literal", dir, constName)
					}

					value, err := strconv.Unquote(literal.Value)
					if err != nil {
						t.Fatalf("can't parse %s.%s value %s: %v", dir, constName, literal.Value, err)
					}

					return value
				}
			}
		}
	}

	t.Fatalf("no %s constant in %s", constName, dir)

	return ""
}

// moduleDir returns the directory of a dependency in the module cache.
func moduleDir(t *testing.T, module string) string {
	t.Helper()

	output, err := exec.CommandContext(t.Context(), "go", "list", "-m", "-f", "{{.Dir}}", module).Output()
	if err != nil {
		t.Fatalf("can't locate the source of %s: %v", module, err)
	}

	dir := strings.TrimSpace(string(output))
	if dir == "" {
		t.Fatalf("the source of %s isn't in the module cache", module)
	}

	return dir
}
