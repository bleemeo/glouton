package agent

import (
	"testing"
)

func Test_isValidConfigFile(t *testing.T) {
	tests := []struct {
		fileName string
		want     bool
	}{
		{
			fileName: "glouton.conf",
			want:     true,
		},
		{
			fileName: "90-local.conf",
			want:     true,
		},
		{
			fileName: "..2022_02_16_13_08_02.846905773",
			want:     true,
		},
		{
			fileName: "glouton.conf.swp",
			want:     false,
		},
		{
			fileName: "glouton.conf.swo",
			want:     false,
		},
		{
			fileName: "glouton.conf.swn",
			want:     false,
		},
		{
			fileName: "glouton.conf~",
			want:     false,
		},
		{
			fileName: "glouton.save",
			want:     false,
		},
	}

	for _, test := range tests {
		t.Run(test.fileName, func(t *testing.T) {
			if got := isValidConfigFile(test.fileName); got != test.want {
				t.Errorf("isValidConfigFile(%s) is %v, expected %v", test.fileName, got, test.want)
			}
		})
	}
}
