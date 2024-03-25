// Copyright 2015-2023 Bleemeo
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

package matcher

import (
	"github.com/prometheus/prometheus/promql/parser"
)

func MatchersFromQuery(query parser.Expr) []Matchers {
	switch node := query.(type) {
	case *parser.AggregateExpr:
		result := MatchersFromQuery(node.Expr)
		result = append(result, MatchersFromQuery(node.Param)...)

		return result
	case *parser.BinaryExpr:
		result := MatchersFromQuery(node.LHS)
		result = append(result, MatchersFromQuery(node.RHS)...)

		return result
	case *parser.Call:
		result := make([]Matchers, 0)
		for _, arg := range node.Args {
			result = append(result, MatchersFromQuery(arg)...)
		}

		return result
	case *parser.MatrixSelector:
		return MatchersFromQuery(node.VectorSelector)
	case *parser.SubqueryExpr:
		return MatchersFromQuery(node.Expr)
	case *parser.VectorSelector:
		return []Matchers{node.LabelMatchers}
	case *parser.NumberLiteral:
		return nil
	case *parser.ParenExpr:
		return MatchersFromQuery(node.Expr)
	case *parser.StringLiteral:
		return nil
	case *parser.UnaryExpr:
		return MatchersFromQuery(node.Expr)
	case *parser.StepInvariantExpr:
		return MatchersFromQuery(node.Expr)
	default:
		return nil
	}
}
