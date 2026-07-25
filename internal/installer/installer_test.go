package installer

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/lgldsilva/ai-launcher/internal/config"
)

func TestInstallDownloadsChecksummedArchiveAndSkipsCurrentVersion(t *testing.T) {
	archive := tarGz(t, "release/tool", []byte("#!/bin/sh\necho tool\n"))
	digest := sha256.Sum256(archive)
	checksum := hex.EncodeToString(digest[:]) + "  tool-linux-amd64.tar.gz\n"
	var downloads atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/repos/acme/tool/releases/latest":
			_ = json.NewEncoder(response).Encode(releaseResponse{
				TagName: "v1.2.3",
				Assets: []Asset{
					{Name: "tool-linux-amd64.tar.gz", BrowserDownloadURL: serverURL(request, "/download/tool")},
					{Name: "checksums.txt", BrowserDownloadURL: serverURL(request, "/download/checksum")},
				},
			})
		case "/download/tool":
			downloads.Add(1)
			response.Write(archive)
		case "/download/checksum":
			downloads.Add(1)
			response.Write([]byte(checksum))
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()

	root := t.TempDir()
	client := New(root)
	client.APIBaseURL = server.URL
	client.GOOS = "linux"
	client.GOARCH = "amd64"
	client.StatePath = filepath.Join(root, "state.json")
	release := &config.GitHubRelease{
		Repository:    "acme/tool",
		Assets:        map[string]string{"linux-amd64": "tool-linux-amd64.tar.gz"},
		Binary:        "tool",
		ChecksumAsset: "checksums.txt",
	}
	target := filepath.Join(root, "bin", "tool")

	first, err := client.Install(context.Background(), "Tool", "tool", target, release, false)
	if err != nil {
		t.Fatalf("first Install() error = %v", err)
	}
	if first.Status != "installed" || first.Version != "v1.2.3" || first.Path != target {
		t.Fatalf("first result = %#v", first)
	}
	contents, err := os.ReadFile(target)
	if err != nil || string(contents) != "#!/bin/sh\necho tool\n" {
		t.Fatalf("installed executable = %q, err=%v", contents, err)
	}
	info, err := os.Stat(target)
	if err != nil || info.Mode().Perm()&0o111 == 0 {
		t.Fatalf("installed executable mode = %v, err=%v", info, err)
	}

	second, err := client.Install(context.Background(), "Tool", "tool", target, release, false)
	if err != nil {
		t.Fatalf("second Install() error = %v", err)
	}
	if second.Status != "current" || downloads.Load() != 2 {
		t.Fatalf("second result = %#v, downloads = %d; want current and two initial downloads", second, downloads.Load())
	}
	forced, err := client.Install(context.Background(), "Tool", "tool", target, release, true)
	if err != nil || forced.Status != "installed" || downloads.Load() != 4 {
		t.Fatalf("forced Install() = %#v, err=%v, downloads=%d; want reinstall", forced, err, downloads.Load())
	}
}

func TestInstallRejectsMissingOrInvalidChecksumUnlessExplicitlyAllowed(t *testing.T) {
	data := []byte("#!/bin/sh\nexit 0\n")
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/repos/acme/raw/releases/latest" {
			_ = json.NewEncoder(response).Encode(releaseResponse{TagName: "v1", Assets: []Asset{{Name: "raw", BrowserDownloadURL: serverURL(request, "/raw")}}})
			return
		}
		response.Write(data)
	}))
	defer server.Close()
	root := t.TempDir()
	client := New(root)
	client.APIBaseURL = server.URL
	client.GOOS = "linux"
	client.GOARCH = "amd64"
	client.StatePath = filepath.Join(root, "state.json")
	release := &config.GitHubRelease{Repository: "acme/raw", Assets: map[string]string{"linux-amd64": "raw"}, Binary: "raw"}
	_, err := client.Install(context.Background(), "Raw", "raw", filepath.Join(root, "raw"), release, false)
	if err == nil || !strings.Contains(err.Error(), "no checksum asset") {
		t.Fatalf("missing checksum error = %v", err)
	}
	release.AllowUnverified = true
	result, err := client.Install(context.Background(), "Raw", "raw", filepath.Join(root, "raw"), release, false)
	if err != nil || result.Status != "installed" {
		t.Fatalf("allow_unverified Install() = %#v, err=%v", result, err)
	}
}

func TestInstallAcceptsGitHubAssetDigest(t *testing.T) {
	archive := tarGz(t, "release/tool", []byte("#!/bin/sh\nexit 0\n"))
	digest := sha256.Sum256(archive)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/repos/acme/digest/releases/latest":
			_ = json.NewEncoder(response).Encode(releaseResponse{
				TagName: "v1",
				Assets:  []Asset{{Name: "tool.tar.gz", Digest: "sha256:" + hex.EncodeToString(digest[:]), BrowserDownloadURL: serverURL(request, "/tool")}},
			})
		case "/tool":
			response.Write(archive)
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()

	root := t.TempDir()
	client := New(root)
	client.APIBaseURL = server.URL
	client.GOOS = "linux"
	client.GOARCH = "amd64"
	release := &config.GitHubRelease{Repository: "acme/digest", Assets: map[string]string{"linux-amd64": "tool.tar.gz"}, Binary: "tool"}
	result, err := client.Install(context.Background(), "Tool", "tool", filepath.Join(root, "tool"), release, false)
	if err != nil || result.Status != "installed" {
		t.Fatalf("GitHub digest Install() = %#v, err=%v", result, err)
	}
}

func TestInstallSourceUpdatesHTTPSWrapper(t *testing.T) {
	var version atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if version.Load() == 0 {
			response.Write([]byte("#!/usr/bin/env bash\necho v1\n"))
			return
		}
		response.Write([]byte("#!/usr/bin/env bash\necho v2\n"))
	}))
	defer server.Close()
	root := t.TempDir()
	client := New(root)
	client.HTTPClient = server.Client()
	client.StatePath = filepath.Join(root, "state.json")
	target := filepath.Join(root, "bin", "wrapper")
	first, err := client.InstallSource(context.Background(), "Wrapper", "wrapper", target, server.URL, false)
	if err != nil || first.Status != "installed" {
		t.Fatalf("first source install = %#v, err=%v", first, err)
	}
	second, err := client.InstallSource(context.Background(), "Wrapper", "wrapper", target, server.URL, false)
	if err != nil || second.Status != "current" {
		t.Fatalf("second source install = %#v, err=%v", second, err)
	}
	version.Store(1)
	third, err := client.InstallSource(context.Background(), "Wrapper", "wrapper", target, server.URL, false)
	if err != nil || third.Status != "installed" {
		t.Fatalf("updated source install = %#v, err=%v", third, err)
	}
	contents, err := os.ReadFile(target)
	if err != nil || !strings.Contains(string(contents), "v2") {
		t.Fatalf("updated source contents = %q, err=%v", contents, err)
	}
}

func TestArchiveExtractionRejectsTraversalAndFindsBasename(t *testing.T) {
	unsafe := tarGz(t, "../../outside", []byte("bad"))
	if _, err := extractBinary(unsafe, "tool.tgz", "outside", "tool"); err == nil || !strings.Contains(err.Error(), "unsafe archive path") {
		t.Fatalf("unsafe archive error = %v", err)
	}
	archive := tarGz(t, "nested/tool", []byte("good"))
	got, err := extractBinary(archive, "tool.tar.gz", "tool", "tool")
	if err != nil || string(got) != "good" {
		t.Fatalf("basename extraction = %q, err=%v", got, err)
	}
}

func TestChecksumFormatsAndAssetSelection(t *testing.T) {
	hash := strings.Repeat("a", 64)
	for _, value := range []string{
		fmt.Sprintf("%s  tool.tar.gz\n", hash),
		fmt.Sprintf("tool.tar.gz %s\n", hash),
		fmt.Sprintf("SHA256 (tool.tar.gz) = %s\n", hash),
		fmt.Sprintf("%s\n", hash),
	} {
		got, err := checksumFor([]byte(value), "tool.tar.gz")
		if err != nil || got != hash {
			t.Errorf("checksumFor(%q) = %q, %v", value, got, err)
		}
	}
	asset, err := selectAsset([]Asset{{Name: "tool-v2.tar.gz"}, {Name: "tool-v1.tar.gz"}}, "tool-*.tar.gz")
	if err != nil || asset.Name != "tool-v1.tar.gz" {
		t.Fatalf("selectAsset() = %#v, %v", asset, err)
	}
	if _, err := selectAsset(nil, "missing"); err == nil {
		t.Fatal("selectAsset(missing) = nil error")
	}
}

func TestInstallerRejectsInvalidConfiguration(t *testing.T) {
	client := New(t.TempDir())
	client.GOOS = "plan9"
	client.GOARCH = "amd64"
	if _, err := client.Install(context.Background(), "Tool", "tool", "", nil, false); err == nil {
		t.Fatal("nil release = nil error")
	}
	if _, err := client.Install(context.Background(), "Tool", "tool", "", &config.GitHubRelease{Repository: "bad"}, false); err == nil || !strings.Contains(err.Error(), "no release asset") {
		t.Fatalf("missing platform asset error = %v", err)
	}
	if _, err := client.latestRelease(context.Background(), "bad/repo/extra"); err == nil {
		t.Fatal("invalid repository = nil error")
	}
}

func tarGz(t *testing.T, name string, contents []byte) []byte {
	t.Helper()
	var buffer bytes.Buffer
	gzipWriter := gzip.NewWriter(&buffer)
	tarWriter := tar.NewWriter(gzipWriter)
	if err := tarWriter.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(contents))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tarWriter.Write(contents); err != nil {
		t.Fatal(err)
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func serverURL(request *http.Request, path string) string {
	return "http://" + request.Host + path
}
