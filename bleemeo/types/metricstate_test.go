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

package types

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

// constNamesOfType returns the names declared in the const block of the given type in the
// given file. Those blocks use iota, so only the first spec carries the type and the rest
// inherit it.
func constNamesOfType(t *testing.T, fileName string, typeName string) []string {
	t.Helper()

	file, err := parser.ParseFile(token.NewFileSet(), fileName, nil, 0)
	if err != nil {
		t.Fatalf("parsing %s: %v", fileName, err)
	}

	var names []string

	for _, decl := range file.Decls {
		genDecl, ok := decl.(*ast.GenDecl)
		if !ok || genDecl.Tok != token.CONST {
			continue
		}

		inBlock := false

		for _, spec := range genDecl.Specs {
			valueSpec, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}

			if ident, ok := valueSpec.Type.(*ast.Ident); ok {
				inBlock = ident.Name == typeName
			}

			if !inBlock {
				continue
			}

			for _, name := range valueSpec.Names {
				names = append(names, name.Name)
			}
		}
	}

	if len(names) == 0 {
		t.Fatalf("no constant of type %s found in %s", typeName, fileName)
	}

	return names
}

// failureKindReasons is every FailureKind, by constant name, and the reason it maps to.
//
// Keyed by name rather than by value so that renaming a constant, or removing one and
// adding another in the same change, cannot keep this table in agreement with the source
// by accident.
func failureKindReasons() map[string]MetricStateReason {
	return map[string]MetricStateReason{
		"FailureUnknown":                ReasonUnknown,
		"FailureAllowList":              ReasonNotAllowed,
		"FailureTooManyCustomMetrics":   ReasonTooManyCustomMetrics,
		"FailureTooManyStandardMetrics": ReasonTooManyStandardMetrics,
	}
}

// denyReasonReasons is every DenyReason, by constant name, and the reason it maps to.
func denyReasonReasons() map[string]MetricStateReason {
	return map[string]MetricStateReason{
		"NotDenied":                     ReasonUnknown,
		"DenyNotAvailableInCurrentPlan": ReasonNotAvailableInPlan,
		"DenyErrorOccurred":             ReasonErrorOccurred,
		"DenyNoDockerIntegration":       ReasonNoDockerIntegration,
		"DenyItemTooLong":               ReasonItemTooLong,
		"DenyMissingContainerID":        ReasonMissingContainerID,
	}
}

// These const blocks use iota, so a constant's value is its index in the block.

func TestFailureKindMetricStateReason(t *testing.T) {
	table := failureKindReasons()

	for i, name := range constNamesOfType(t, "bleemeo.go", "FailureKind") {
		want, listed := table[name]
		if !listed {
			t.Errorf("%s is declared but missing from failureKindReasons", name)

			continue
		}

		if got := FailureKind(i).MetricStateReason(); got != want {
			t.Errorf("%s.MetricStateReason() = %q, want %q", name, got, want)
		}
	}
}

func TestDenyReasonMetricStateReason(t *testing.T) {
	table := denyReasonReasons()

	for i, name := range constNamesOfType(t, "types.go", "DenyReason") {
		want, listed := table[name]
		if !listed {
			t.Errorf("%s is declared but missing from denyReasonReasons", name)

			continue
		}

		if got := DenyReason(i).MetricStateReason(); got != want {
			t.Errorf("%s.MetricStateReason() = %q, want %q", name, got, want)
		}
	}
}

// TestFailureKindReasonMatchesString pins that FailureKind.String() and
// MetricStateReason() keep returning the same text for the same kind. They are two
// switches with identical arms: the diagnostic archive prints one and the report sends the
// other, so letting them drift would describe one failure two different ways.
func TestFailureKindReasonMatchesString(t *testing.T) {
	for i := range constNamesOfType(t, "bleemeo.go", "FailureKind") {
		kind := FailureKind(i)

		if got, want := string(kind.MetricStateReason()), kind.String(); got != want {
			t.Errorf("FailureKind(%d): MetricStateReason() = %q but String() = %q", i, got, want)
		}
	}
}

// TestMetricStateReasonsDoNotCollide checks the two vocabularies stay distinct once
// merged. If a FailureKind and a DenyReason mapped to the same reason, two different
// problems would reach the user as one message, which is what unifying them must not do.
func TestMetricStateReasonsDoNotCollide(t *testing.T) {
	seen := make(map[MetricStateReason]string)

	for name, reason := range failureKindReasons() {
		if reason == ReasonUnknown {
			continue
		}

		if other, ok := seen[reason]; ok {
			t.Errorf("reason %q is used by both %s and %s", reason, other, name)
		}

		seen[reason] = name
	}

	for name, reason := range denyReasonReasons() {
		if reason == ReasonUnknown {
			continue
		}

		if other, ok := seen[reason]; ok {
			t.Errorf("reason %q is used by both %s and %s", reason, other, name)
		}

		seen[reason] = name
	}
}

func TestRoundMetricCount(t *testing.T) {
	cases := map[int]int{
		-5:    0,
		0:     0,
		1:     1,
		2:     2,
		3:     5,
		5:     5,
		6:     10,
		47:    50,
		48:    50,
		50:    50,
		51:    100,
		1500:  2000,
		5000:  5000,
		5001:  10000,
		12345: 15000,
	}

	for input, want := range cases {
		if got := RoundMetricCount(input); got != want {
			t.Errorf("RoundMetricCount(%d) = %d, want %d", input, got, want)
		}
	}
}

// TestRoundMetricCountIsStable is the point of rounding: a count that drifts by a few
// metrics must keep reporting the same value, otherwise the list is re-sent to the API on
// every single collection.
func TestRoundMetricCountIsStable(t *testing.T) {
	first := RoundMetricCount(40)

	for count := 40; count <= 50; count++ {
		if got := RoundMetricCount(count); got != first {
			t.Errorf("RoundMetricCount(%d) = %d, want %d: a small drift changed the value", count, got, first)
		}
	}
}

// TestRoundMetricCountNeverUnderReports checks rounding is always up, so a user is never
// told there are fewer problems than there are.
func TestRoundMetricCountNeverUnderReports(t *testing.T) {
	for count := 1; count < 3000; count++ {
		if got := RoundMetricCount(count); got < count {
			t.Fatalf("RoundMetricCount(%d) = %d, which under-reports", count, got)
		}
	}
}
