package installer

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lgldsilva/ai-launcher/internal/config"
)

// http.DefaultClient has no Timeout, and it is process-global: an Installer
// holding it could not be given one without changing the deadline of every
// other HTTP user in the process. `ai-launcher --install` against a host that
// accepts the connection and then never answers blocked forever, with no way
// out but Ctrl+C — while internal/selfupdate, doing the same job against the
// same host, has always carried a deadline.
func TestNewInstallerCarriesItsOwnClientWithATimeout(t *testing.T) {
	client := New(t.TempDir())
	if client.HTTPClient == nil {
		t.Fatal("HTTPClient = nil")
	}
	if client.HTTPClient == http.DefaultClient {
		t.Fatal("HTTPClient = http.DefaultClient; it is shared process-wide and has no timeout")
	}
	if client.HTTPClient.Timeout <= 0 {
		t.Fatalf("HTTPClient.Timeout = %v; every network call needs a deadline", client.HTTPClient.Timeout)
	}
}

// The deadline has to actually abort the request, not merely be configured.
func TestInstallGivesUpOnAServerThatNeverResponds(t *testing.T) {
	blocked := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		<-blocked
	}))
	// LIFO: release the handler before Close, which waits for outstanding
	// requests and would otherwise deadlock against its own blocked handler.
	defer server.Close()
	defer close(blocked)

	client := New(t.TempDir())
	client.APIBaseURL = server.URL
	client.GOOS, client.GOARCH = "linux", "amd64"
	client.HTTPClient = &http.Client{Timeout: 200 * time.Millisecond}

	release := &config.GitHubRelease{
		Repository: "owner/repo",
		Assets:     map[string]string{"linux-amd64": "tool-linux-amd64.tar.gz"},
		Binary:     "tool",
	}

	done := make(chan error, 1)
	go func() {
		_, err := client.Install(context.Background(), "tool", "tool", "", release, false)
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("Install() = nil; a server that never answers must surface as an error")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Install() never returned; the request has no deadline")
	}
}

// The context passed in still has to win when it is stricter than the client
// deadline, so a caller can bound the whole operation.
func TestInstallHonorsACancelledContext(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := New(t.TempDir())
	client.APIBaseURL = server.URL
	client.GOOS, client.GOARCH = "linux", "amd64"

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := client.Install(ctx, "tool", "tool", "", &config.GitHubRelease{
		Repository: "owner/repo",
		Assets:     map[string]string{"linux-amd64": "tool-linux-amd64.tar.gz"},
		Binary:     "tool",
	}, false)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Install() error = %v; want the caller's cancellation to propagate", err)
	}
}
