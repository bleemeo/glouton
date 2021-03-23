package testutil

import "strings"

func Unindent(input string) string {
	lines := strings.Split(input, "\n")
	kept := make([]string, 0, len(lines))

	for _, l := range lines {
		l := strings.TrimLeft(l, " \t")
		if len(l) > 0 {
			kept = append(kept, l)
		}
	}

	return strings.Join(kept, "\n") + "\n"
}
