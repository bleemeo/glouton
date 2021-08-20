//nolint:scopelint
package logger

import (
	"testing"
	"time"
)

func Test_buffer(t *testing.T) {
	now := time.Date(2020, 6, 24, 16, 15, 0, 0, time.UTC)

	tests := []struct {
		name    string
		maxHead int
		maxTail int
		writes  []string
		want    string
	}{
		{
			name:    "few write",
			maxHead: 10,
			maxTail: 10,
			writes: []string{
				"first line\n",
				"second line\n",
				"third line\n",
			},
			want: "2020/06/24 16:15:00 first line\n2020/06/24 16:15:00 second line\n2020/06/24 16:15:00 third line\n",
		},
		{
			name: "few write default max-size",
			writes: []string{
				"first line\n",
				"second line\n",
				"third line\n",
			},
			want: "2020/06/24 16:15:00 first line\n2020/06/24 16:15:00 second line\n2020/06/24 16:15:00 third line\n",
		},
		{
			name:    "tail not full",
			maxHead: 2,
			maxTail: 2,
			writes: []string{
				"in head\n",
				"head also\n",
				"in tail\n",
			},
			want: "2020/06/24 16:15:00 in head\n2020/06/24 16:15:00 head also\n2020/06/24 16:15:00 in tail\n",
		},
		{
			name:    "full",
			maxHead: 2,
			maxTail: 2,
			writes: []string{
				"in head\n",
				"head also\n",
				"in tail\n",
				"last of tail\n",
			},
			want: "2020/06/24 16:15:00 in head\n2020/06/24 16:15:00 head also\n[...]\n2020/06/24 16:15:00 in tail\n2020/06/24 16:15:00 last of tail\n",
		},
		{
			name:    "overflow 1",
			maxHead: 2,
			maxTail: 2,
			writes: []string{
				"in head\n",
				"head also\n",
				"tail1\n",
				"tail2\n",
				"tail3\n",
			},
			want: "2020/06/24 16:15:00 in head\n2020/06/24 16:15:00 head also\n[...]\n2020/06/24 16:15:00 tail2\n2020/06/24 16:15:00 tail3\n",
		},
		{
			name:    "overflow 2",
			maxHead: 2,
			maxTail: 2,
			writes: []string{
				"in head\n",
				"head also\n",
				"tail1\n",
				"tail2\n",
				"tail3\n",
				"tail4\n",
			},
			want: "2020/06/24 16:15:00 in head\n2020/06/24 16:15:00 head also\n[...]\n2020/06/24 16:15:00 tail3\n2020/06/24 16:15:00 tail4\n",
		},
		{
			name:    "overflow 4",
			maxHead: 2,
			maxTail: 2,
			writes: []string{
				"in head\n",
				"head also\n",
				"tail1\n",
				"tail2\n",
				"tail3\n",
				"tail4\n",
				"tail5\n",
			},
			want: "2020/06/24 16:15:00 in head\n2020/06/24 16:15:00 head also\n[...]\n2020/06/24 16:15:00 tail4\n2020/06/24 16:15:00 tail5\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := &buffer{
				headMaxSize: tt.maxHead,
				tailMaxSize: tt.maxTail,
			}

			for _, line := range tt.writes {
				n, err := b.write(now, []byte(line))
				if err != nil {
					t.Fatal(err)
				}
				if n != len([]byte(line)) {
					t.Errorf("Write() = %d, want %d", n, len([]byte(line)))
				}
			}

			if got := b.Content(); string(got) != tt.want {
				t.Errorf("buffer.Content() = %v, want %v", string(got), tt.want)
			}
		})
	}
}
