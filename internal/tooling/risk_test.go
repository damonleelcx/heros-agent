package tooling

import "testing"

func TestClassifyCLI(t *testing.T) {
	cases := []struct {
		cmd  string
		want RiskTier
	}{
		{"ls -la", RiskLow},
		{"git status", RiskLow},
		{"curl https://x.com", RiskMedium},
		{"rm -rf /", RiskHigh},
	}
	for _, c := range cases {
		if got := ClassifyCLI(c.cmd); got != c.want {
			t.Fatalf("%q: got %s want %s", c.cmd, got, c.want)
		}
	}
}
