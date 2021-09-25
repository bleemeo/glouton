package delay

import (
	"testing"
	"time"
)

func TestExponential(t *testing.T) {
	type args struct {
		base        time.Duration
		powerFactor float64
		max         time.Duration
	}

	tests := []struct {
		name  string
		args  args
		wants []time.Duration
	}{
		{
			name: "60-seconds-1.55",
			args: args{
				base:        60 * time.Second,
				powerFactor: 1.55,
				max:         time.Hour,
			},
			wants: []time.Duration{
				60 * time.Second,
				93 * time.Second,
				144 * time.Second,
				3*time.Minute + 43*time.Second,
				5*time.Minute + 46*time.Second,
				8*time.Minute + 56*time.Second,
				13*time.Minute + 52*time.Second,
				21*time.Minute + 29*time.Second,
				33*time.Minute + 18*time.Second,
				51*time.Minute + 38*time.Second,
				time.Hour,
				time.Hour,
			},
		},
		{
			name: "2-seconds-1.55",
			args: args{
				base:        2 * time.Second,
				powerFactor: 1.55,
				max:         time.Hour,
			},
			wants: []time.Duration{
				2 * time.Second,
				3 * time.Second,
				4 * time.Second,
				7 * time.Second,
			},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			for n, want := range tt.wants {
				if got := Exponential(tt.args.base, tt.args.powerFactor, n+1, tt.args.max); got != want {
					t.Errorf("Exponential(%d) = %v, want %v", n, got, want)
				}
			}
		})
	}
}
