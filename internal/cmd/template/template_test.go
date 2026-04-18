package template

import (
	"strings"
	"testing"
	"time"

	"github.com/dibbla-agents/dibbla-cli/internal/templates"
)

func TestSourceMessage_Variants(t *testing.T) {
	cases := map[templates.Source]string{
		templates.SourceFreshCache: "from cache",
		templates.SourceNetwork:    "fresh from network",
		templates.SourceStaleCache: "stale cache",
		templates.SourceEmbedded:   "embedded fallback",
	}
	for src, wantSubstr := range cases {
		res := &templates.Resolution{Source: src, Age: 12 * time.Minute}
		got := sourceMessage(res)
		if !strings.Contains(got, wantSubstr) {
			t.Errorf("source %d: %q does not contain %q", src, got, wantSubstr)
		}
	}
}

func TestHumanDuration(t *testing.T) {
	cases := []struct {
		d    time.Duration
		want string
	}{
		{30 * time.Second, "30s"},
		{45 * time.Minute, "45m"},
		{3 * time.Hour, "3h"},
	}
	for _, c := range cases {
		if got := humanDuration(c.d); got != c.want {
			t.Errorf("humanDuration(%v) = %q, want %q", c.d, got, c.want)
		}
	}
}
