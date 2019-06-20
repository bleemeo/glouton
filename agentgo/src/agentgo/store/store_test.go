package store

import "testing"

func TestLabelsMatchNotExact(t *testing.T) {
	cases := []struct {
		labels, filter map[string]string
		want           bool
	}{
		{
			map[string]string{
				"__name__": "cpu_used",
			},
			map[string]string{
				"__name__": "cpu_used",
			},
			true,
		},
		{
			map[string]string{
				"__name__": "disk_used",
				"item":     "/home",
			},
			map[string]string{
				"__name__": "disk_used",
			},
			true,
		},
		{
			map[string]string{
				"__name__": "disk_used",
				"item":     "/home",
			},
			map[string]string{
				"__name__": "cpu_used",
			},
			false,
		},
		{
			map[string]string{
				"__name__": "disk_used",
				"item":     "/home",
			},
			map[string]string{
				"__name__": "disk_used",
				"item":     "/",
			},
			false,
		},
		{
			map[string]string{
				"__name__": "disk_used",
				"item":     "/home",
			},
			map[string]string{
				"__name__": "disk_used",
				"extra":    "label",
			},
			false,
		},
	}

	for _, c := range cases {
		got := labelsMatch(c.labels, c.filter, false)
		if got != c.want {
			t.Errorf("labelsMatch(%v, %v, false) == %v, want %v", c.labels, c.filter, got, c.want)
		}
	}
}

func TestLabelsMatchExact(t *testing.T) {
	cases := []struct {
		labels, filter map[string]string
		want           bool
	}{
		{
			map[string]string{
				"__name__": "cpu_used",
			},
			map[string]string{
				"__name__": "cpu_used",
			},
			true,
		},
		{
			map[string]string{
				"__name__": "disk_used",
				"item":     "/home",
			},
			map[string]string{
				"__name__": "disk_used",
			},
			false,
		},
		{
			map[string]string{
				"__name__": "disk_used",
				"item":     "/home",
			},
			map[string]string{
				"__name__": "cpu_used",
			},
			false,
		},
		{
			map[string]string{
				"__name__": "disk_used",
				"item":     "/home",
			},
			map[string]string{
				"__name__": "disk_used",
				"item":     "/",
			},
			false,
		},
		{
			map[string]string{
				"__name__": "disk_used",
				"item":     "/home",
			},
			map[string]string{
				"__name__": "disk_used",
				"item":     "/home",
			},
			true,
		},
		{
			map[string]string{
				"__name__": "disk_used",
				"item":     "/home",
			},
			map[string]string{
				"__name__": "disk_used",
				"item":     "/home",
				"extra":    "label",
			},
			false,
		},
	}

	for _, c := range cases {
		got := labelsMatch(c.labels, c.filter, true)
		if got != c.want {
			t.Errorf("labelsMatch(%v, %v, false) == %v, want %v", c.labels, c.filter, got, c.want)
		}
	}
}
