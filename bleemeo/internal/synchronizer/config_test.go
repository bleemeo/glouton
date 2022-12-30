package synchronizer

import (
	"glouton/bleemeo/types"
	"testing"
)

func TestItemTypeFromConfigValue(t *testing.T) {
	t.Parallel()

	tests := []struct {
		Name         string
		Value        interface{}
		ExpectedType types.ConfigItemType
	}{
		{
			Name:         "int",
			Value:        3,
			ExpectedType: types.TypeInt,
		},
		{
			Name:         "bool",
			Value:        false,
			ExpectedType: types.TypeBool,
		},
		{
			Name:         "string",
			Value:        "a",
			ExpectedType: types.TypeString,
		},
		{
			Name:         "list-string",
			Value:        []string{"a", "b"},
			ExpectedType: types.TypeList,
		},
		{
			Name:         "list-int",
			Value:        []int{1, 2},
			ExpectedType: types.TypeList,
		},
		{
			Name: "list-struct",
			Value: []struct {
				a bool
				b float64
			}{{a: false, b: 5.0}},
			ExpectedType: types.TypeList,
		},
		{
			Name:         "map-string-string",
			Value:        map[string]string{"a": "b"},
			ExpectedType: types.TypeMap,
		},
		{
			Name:         "map-string-float",
			Value:        map[string]float64{"a": 7.0},
			ExpectedType: types.TypeMap,
		},
		{
			Name: "map-string-struct",
			Value: map[string]struct {
				a bool
				b float64
			}{"a": {a: false, b: 5.0}},
			ExpectedType: types.TypeMap,
		},
		{
			Name:         "nil",
			Value:        nil,
			ExpectedType: types.TypeUnknown,
		},
	}

	for _, test := range tests {
		test := test

		t.Run(test.Name, func(t *testing.T) {
			if gotType := bleemeoItemTypeFromConfigValue(test.Value); gotType != test.ExpectedType {
				t.Fatalf("Expected %d, got %d", test.ExpectedType, gotType)
			}
		})
	}
}
