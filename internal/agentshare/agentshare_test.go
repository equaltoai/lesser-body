package agentshare

import "testing"

func TestNormalizePrincipalUsername(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"alice", "alice"},
		{"@alice", "alice"},
		{" ALICE ", "alice"},
		{" @Alice ", "alice"},
		{"", ""},
		{"   ", ""},
		{"@", ""},
	}
	for _, tc := range cases {
		if got := NormalizePrincipalUsername(tc.in); got != tc.want {
			t.Fatalf("NormalizePrincipalUsername(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
