package testutil_test

import (
	"glouton/facts/container-runtime/internal/testutil"
	"testing"
)

// TestUnindent check that helper function unindent works as intended.
func TestUnindent(t *testing.T) {
	indented := `
		12:cpuset:/docker/72cf779c7429b33b04f296a98fc9be928c82c5537e333589bec734a884cbb2d2
		11:memory:/docker/72cf779c7429b33b04f296a98fc9be928c82c5537e333589bec734a884cbb2d2/init.scope
		10:pids:/docker/72cf779c7429b33b04f296a98fc9be928c82c5537e333589bec734a884cbb2d2/init.scope`

	want := `12:cpuset:/docker/72cf779c7429b33b04f296a98fc9be928c82c5537e333589bec734a884cbb2d2
11:memory:/docker/72cf779c7429b33b04f296a98fc9be928c82c5537e333589bec734a884cbb2d2/init.scope
10:pids:/docker/72cf779c7429b33b04f296a98fc9be928c82c5537e333589bec734a884cbb2d2/init.scope
`

	got := testutil.Unindent(indented)
	if got != want {
		t.Errorf("got = %#v, want %#v", got, want)
	}
}
