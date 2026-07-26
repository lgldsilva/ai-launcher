package cmd

import (
	"testing"

	"github.com/lgldsilva/ai-launcher/internal/config"
)

// A recipe that publishes release assets must install from them, because that
// is the only path with SHA-256 verification. Routing to source_url whenever it
// is declared meant ai-memory — which declares both — was fetched from a
// mutable branch with no checksum, contradicting the documented invariant.
func TestReleaseAssetsWinOverTheSourceURL(t *testing.T) {
	release := &config.GitHubRelease{Repository: "akitaonrails/ai-memory"}
	cases := []struct {
		name   string
		target installTarget
		want   bool
	}{
		{
			name:   "release and source both declared",
			target: installTarget{Name: "ai-memory", Release: release, SourceURL: "https://example.test/bin/ai-memory"},
			want:   true,
		},
		{
			name:   "release only",
			target: installTarget{Name: "ai-jail", Release: release},
			want:   true,
		},
		{
			name:   "source only stays on the source path",
			target: installTarget{Name: "wrapper", SourceURL: "https://example.test/bin/wrapper"},
			want:   false,
		},
		{
			name:   "no recipe at all",
			target: installTarget{Name: "bare"},
			want:   false,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := preferReleaseInstall(testCase.target); got != testCase.want {
				t.Fatalf("preferReleaseInstall() = %t; want %t", got, testCase.want)
			}
		})
	}
}
