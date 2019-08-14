package mqtt

import (
	"encoding/json"
	"testing"
)

func TestForceDecimalFloat(t *testing.T) {
	cases := []struct {
		in   float64
		want string
	}{
		{in: 0, want: "0.0"},
		{in: 4.2, want: "4.2"},
		{in: -4.2, want: "-4.2"},
		{in: 4.0, want: "4.0"},
		{in: 87984687654.0, want: "87984687654.0"},
		{in: 0.0000001, want: "1e-7"},
		{in: 1e20, want: "100000000000000000000.0"},
		{in: 1e22, want: "1e+22"},
	}
	for _, c := range cases {
		gotByte, err := forceDecimalFloat(c.in).MarshalJSON()
		if err != nil {
			t.Errorf("Error with case %v: %v", c, err)
		} else {
			got := string(gotByte)
			if got != c.want {
				t.Errorf("forceDecimalFloat(%v).MarshalJSON() == %#v, want %#v", c.in, got, c.want)
			}
			var f float64
			_ = json.Unmarshal(gotByte, &f)
			if f != c.in {
				t.Errorf("Unmarshal(%#v) == %v, want %v", got, f, c.in)
			}
		}
	}
}
