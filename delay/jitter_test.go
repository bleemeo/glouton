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
		{
			name: "1-hour-1.75",
			args: args{
				base:        time.Hour,
				powerFactor: 1.75,
				max:         12 * time.Hour,
			},
			wants: []time.Duration{
				time.Hour,
				time.Hour + 45*time.Minute,
				3*time.Hour + 3*time.Minute + 45*time.Second,
				5*time.Hour + 21*time.Minute + 33*time.Second,
				9*time.Hour + 22*time.Minute + 44*time.Second,
				12 * time.Hour,
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

func TestExponentialMax(t *testing.T) {
	type args struct {
		base        time.Duration
		powerFactor float64
		max         time.Duration
	}

	const maxIter = 100000

	tests := []struct {
		name         string
		args         args
		wantLessThan time.Duration
		wantMoreThan time.Duration
	}{
		{
			name: "60-seconds-1.55",
			args: args{
				base:        60 * time.Second,
				powerFactor: 1.55,
				max:         time.Hour,
			},
			wantLessThan: time.Hour,
			wantMoreThan: 60 * time.Second,
		},
		{
			name: "2-seconds-1.55",
			args: args{
				base:        2 * time.Second,
				powerFactor: 1.55,
				max:         time.Hour,
			},
			wantLessThan: time.Hour,
			wantMoreThan: 2 * time.Second,
		},
		{
			name: "1-hour-1.75",
			args: args{
				base:        time.Hour,
				powerFactor: 1.75,
				max:         12 * time.Hour,
			},
			wantLessThan: 12 * time.Hour,
			wantMoreThan: time.Hour,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			for n := 0; n < maxIter; n++ {
				got := Exponential(tt.args.base, tt.args.powerFactor, n, tt.args.max)

				if got > tt.wantLessThan {
					t.Fatalf("Exponential(%d) = %v, want less than %v", n, got, tt.wantLessThan)
				}

				if got < tt.wantMoreThan {
					t.Fatalf("Exponential(%d) = %v, want more than %v", n, got, tt.wantMoreThan)
				}
			}
		})
	}
}
