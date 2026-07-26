package cmd

import (
	"io"
	"strings"
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

// Invariant 4: without a verifiable checksum the install fails, unless an
// explicit allow_unverified: true in the recipe. The source_url path fetches
// from a mutable URL whose only checks are an HTTPS scheme and a shebang, so
// it must refuse unless the recipe opted in.
func TestInstallSourceRequiresAllowUnverified(t *testing.T) {
	trace, err := newInstallLog("")
	if err != nil {
		t.Fatal(err)
	}
	target := installTarget{Name: "wrapper", Command: "wrapper", SourceURL: "https://example.test/bin/wrapper"}
	// A nil client is safe: the refusal must happen before any download.
	if _, err := installWithoutRecipe(nil, target, "", false, io.Discard, io.Discard, trace); err == nil || !strings.Contains(err.Error(), "allow_unverified") {
		t.Fatalf("installWithoutRecipe() err = %v; want a refusal naming allow_unverified", err)
	}
}
