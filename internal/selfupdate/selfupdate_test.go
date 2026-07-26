package selfupdate

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

const testBinaryContents = "#!/bin/sh\necho fake ai-launcher\n"

// releaseServer builds an httptest server that serves the GitHub-style API
// and download endpoints the updater talks to. apiHandler answers
// /releases/latest and /releases; downloads serves the files map under
// /<tag>/<name>.
func releaseServer(t *testing.T, apiHandler http.HandlerFunc, downloads map[string][]byte) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/releases") {
			apiHandler(w, r)
			return
		}
		if data, ok := downloads[r.URL.Path]; ok {
			_, _ = w.Write(data)
			return
		}
		http.NotFound(w, r)
	}))
}

func latestJSON(tag string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/releases/latest" {
			http.NotFound(w, r)
			return
		}
		_, _ = fmt.Fprintf(w, `{"tag_name":%q}`, tag)
	}
}

func TestLatestTagFromLatestEndpoint(t *testing.T) {
	server := releaseServer(t, latestJSON("v0.2.0"), nil)
	defer server.Close()
	updater := &Updater{APIBaseURL: server.URL, HTTPClient: server.Client()}
	tag, err := updater.LatestTag(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if tag != "v0.2.0" {
		t.Fatalf("tag = %q, want v0.2.0", tag)
	}
}

func TestLatestTagFallsBackToListOn404(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/releases/latest" {
			http.NotFound(w, r)
			return
		}
		if !strings.HasPrefix(r.URL.Path, "/releases") || r.URL.Query().Get("per_page") != "50" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`[
			{"tag_name":"v0.1.0","draft":false,"prerelease":false},
			{"tag_name":"v0.10.0","draft":false,"prerelease":false},
			{"tag_name":"v0.2.0","draft":false,"prerelease":false},
			{"tag_name":"v9.9.9","draft":true,"prerelease":false},
			{"tag_name":"v0.11.0-rc.1","draft":false,"prerelease":true}
		]`))
	}
	server := releaseServer(t, handler, nil)
	defer server.Close()
	updater := &Updater{APIBaseURL: server.URL, HTTPClient: server.Client()}
	tag, err := updater.LatestTag(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	// Semver ordering (v0.10.0 > v0.2.0), drafts skipped, prereleases skipped
	// while a stable release exists.
	if tag != "v0.10.0" {
		t.Fatalf("tag = %q, want v0.10.0", tag)
	}
}

func TestLatestTagListFallsBackToPrereleases(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/releases/latest" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`[
			{"tag_name":"v0.1.0-rc.1","draft":false,"prerelease":true},
			{"tag_name":"v0.2.0-beta","draft":false,"prerelease":true}
		]`))
	}
	server := releaseServer(t, handler, nil)
	defer server.Close()
	updater := &Updater{APIBaseURL: server.URL, HTTPClient: server.Client()}
	tag, err := updater.LatestTag(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if tag != "v0.2.0-beta" {
		t.Fatalf("tag = %q, want v0.2.0-beta", tag)
	}
}

func TestLatestTagListEmpty(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/releases/latest" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`[{"tag_name":"v0.1.0","draft":true}]`))
	}
	server := releaseServer(t, handler, nil)
	defer server.Close()
	updater := &Updater{APIBaseURL: server.URL, HTTPClient: server.Client()}
	if _, err := updater.LatestTag(context.Background()); err == nil || !strings.Contains(err.Error(), "no published releases") {
		t.Fatalf("err = %v, want no published releases", err)
	}
}

func TestLatestTagPropagatesNon404Errors(t *testing.T) {
	handler := func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}
	server := releaseServer(t, handler, nil)
	defer server.Close()
	updater := &Updater{APIBaseURL: server.URL, HTTPClient: server.Client()}
	if _, err := updater.LatestTag(context.Background()); err == nil || !strings.Contains(err.Error(), "500") {
		t.Fatalf("err = %v, want a 500 propagation", err)
	}
}

func TestAssetName(t *testing.T) {
	cases := []struct {
		tag, goos, goarch, want string
	}{
		{"v0.2.0", "linux", "amd64", "ai-launcher_0.2.0_linux_amd64.tar.gz"},
		{"0.2.0", "darwin", "arm64", "ai-launcher_0.2.0_darwin_arm64.tar.gz"},
		{"v1.0.0", "windows", "amd64", "ai-launcher_1.0.0_windows_amd64.zip"},
	}
	for _, tc := range cases {
		if got := AssetName(tc.tag, tc.goos, tc.goarch); got != tc.want {
			t.Errorf("AssetName(%q, %q, %q) = %q, want %q", tc.tag, tc.goos, tc.goarch, got, tc.want)
		}
	}
}

func TestBinaryName(t *testing.T) {
	if got := BinaryName("windows"); got != "ai-launcher.exe" {
		t.Fatalf("BinaryName(windows) = %q", got)
	}
	if got := BinaryName("linux"); got != "ai-launcher" {
		t.Fatalf("BinaryName(linux) = %q", got)
	}
}

func TestSameVersion(t *testing.T) {
	cases := []struct {
		current, tag string
		want         bool
	}{
		{"dev", "v0.2.0", false}, // dev builds are always outdated
		{"", "v0.2.0", false},    // empty is always outdated
		{"v0.2.0", "v0.2.0", true},
		{"0.2.0", "v0.2.0", true}, // leading v ignored
		{"v0.1.0", "v0.2.0", false},
	}
	for _, tc := range cases {
		if got := SameVersion(tc.current, tc.tag); got != tc.want {
			t.Errorf("SameVersion(%q, %q) = %v, want %v", tc.current, tc.tag, got, tc.want)
		}
	}
}

func tarGzArchive(t *testing.T, name, contents string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(contents)), Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte(contents)); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func zipArchive(t *testing.T, name, contents string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	file, err := zw.Create(name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write([]byte(contents)); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func checksumsBody(archiveName string, archive []byte) []byte {
	digest := sha256.Sum256(archive)
	return []byte(fmt.Sprintf("%s  %s\n", hex.EncodeToString(digest[:]), archiveName))
}

// downloadFixture returns an updater pointed at a server hosting archive as
// the linux/amd64 release for v0.2.0, plus checksums.txt when sums is non-nil.
func downloadFixture(t *testing.T, archiveName string, archive, sums []byte) *Updater {
	t.Helper()
	downloads := map[string][]byte{"/v0.2.0/" + archiveName: archive}
	if sums != nil {
		downloads["/v0.2.0/checksums.txt"] = sums
	}
	server := releaseServer(t, latestJSON("v0.2.0"), downloads)
	t.Cleanup(server.Close)
	return &Updater{
		DownloadBaseURL: server.URL,
		HTTPClient:      server.Client(),
		GOOS:            "linux",
		GOARCH:          "amd64",
	}
}

func TestDownloadVerifiedBinaryHappyPath(t *testing.T) {
	name := "ai-launcher_0.2.0_linux_amd64.tar.gz"
	archive := tarGzArchive(t, "ai-launcher", testBinaryContents)
	updater := downloadFixture(t, name, archive, checksumsBody(name, archive))
	binary, err := updater.DownloadVerifiedBinary(context.Background(), "v0.2.0")
	if err != nil {
		t.Fatal(err)
	}
	if string(binary) != testBinaryContents {
		t.Fatalf("binary = %q, want %q", binary, testBinaryContents)
	}
}

func TestDownloadVerifiedBinaryWindowsZip(t *testing.T) {
	name := "ai-launcher_0.2.0_windows_amd64.zip"
	archive := zipArchive(t, "ai-launcher.exe", testBinaryContents)
	updater := downloadFixture(t, name, archive, checksumsBody(name, archive))
	updater.GOOS = "windows"
	binary, err := updater.DownloadVerifiedBinary(context.Background(), "v0.2.0")
	if err != nil {
		t.Fatal(err)
	}
	if string(binary) != testBinaryContents {
		t.Fatalf("binary = %q, want %q", binary, testBinaryContents)
	}
}

func TestDownloadVerifiedBinaryChecksumMismatch(t *testing.T) {
	name := "ai-launcher_0.2.0_linux_amd64.tar.gz"
	archive := tarGzArchive(t, "ai-launcher", testBinaryContents)
	sums := strings.Repeat("0", 64) + "  " + name + "\n"
	updater := downloadFixture(t, name, archive, []byte(sums))
	_, err := updater.DownloadVerifiedBinary(context.Background(), "v0.2.0")
	if err == nil || !strings.Contains(err.Error(), "sha256 mismatch") {
		t.Fatalf("err = %v, want sha256 mismatch", err)
	}
}

func TestDownloadVerifiedBinaryMissingChecksumsIsHardError(t *testing.T) {
	name := "ai-launcher_0.2.0_linux_amd64.tar.gz"
	archive := tarGzArchive(t, "ai-launcher", testBinaryContents)
	updater := downloadFixture(t, name, archive, nil)
	_, err := updater.DownloadVerifiedBinary(context.Background(), "v0.2.0")
	if err == nil || !strings.Contains(err.Error(), "checksums.txt") {
		t.Fatalf("err = %v, want a checksums.txt hard error", err)
	}
}

func TestDownloadVerifiedBinaryMissingChecksumEntry(t *testing.T) {
	name := "ai-launcher_0.2.0_linux_amd64.tar.gz"
	archive := tarGzArchive(t, "ai-launcher", testBinaryContents)
	updater := downloadFixture(t, name, archive, []byte("deadbeef  other.tar.gz\n"))
	if _, err := updater.DownloadVerifiedBinary(context.Background(), "v0.2.0"); err == nil {
		t.Fatal("expected an error for a checksums.txt without the archive entry")
	}
}

type recordedRequest struct {
	path   string
	accept string
	auth   string
}

// privateReleaseServer simulates a PRIVATE GitHub repository: the pretty
// /releases/download-style URLs 404 even with a valid token, while the assets
// API (release-by-tag → asset URL, Bearer + Accept: application/octet-stream)
// serves the bytes. files maps asset name → contents.
func privateReleaseServer(t *testing.T, token, tag string, files map[string][]byte) (*httptest.Server, *[]recordedRequest) {
	t.Helper()
	fixture := &privateReleaseFixture{
		token:    token,
		tag:      tag,
		files:    files,
		requests: &[]recordedRequest{},
	}
	for name := range files {
		fixture.names = append(fixture.names, name)
	}
	sort.Strings(fixture.names)
	server := httptest.NewServer(http.HandlerFunc(fixture.handler))
	t.Cleanup(server.Close)
	return server, fixture.requests
}

// privateReleaseFixture holds the state behind the private-release test
// server: the expected token/tag, the sorted asset names, their contents and
// the recorded requests.
type privateReleaseFixture struct {
	token    string
	tag      string
	names    []string
	files    map[string][]byte
	requests *[]recordedRequest
}

func (f *privateReleaseFixture) handler(w http.ResponseWriter, r *http.Request) {
	*f.requests = append(*f.requests, recordedRequest{path: r.URL.Path, accept: r.Header.Get("Accept"), auth: r.Header.Get("Authorization")})
	if r.Header.Get("Authorization") != "Bearer "+f.token {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	if r.URL.Path == "/releases/tags/"+f.tag {
		f.serveRelease(w, r)
		return
	}
	if f.serveAsset(w, r) {
		return
	}
	// Pretty download URLs 404 on private repos, even with a token.
	http.NotFound(w, r)
}

// serveRelease answers the release-by-tag API call with the asset list.
func (f *privateReleaseFixture) serveRelease(w http.ResponseWriter, r *http.Request) {
	var assets []string
	for id, name := range f.names {
		assets = append(assets, fmt.Sprintf(`{"name":%q,"url":"http://%s/assets/%d"}`, name, r.Host, id+1))
	}
	_, _ = fmt.Fprintf(w, `{"tag_name":%q,"assets":[%s]}`, f.tag, strings.Join(assets, ",")) // #nosec G705 -- test fixture JSON served by the local httptest server
}

// serveAsset writes the asset bytes for /assets/<id> URLs, reporting whether
// the request was handled as an asset download.
func (f *privateReleaseFixture) serveAsset(w http.ResponseWriter, r *http.Request) bool {
	if !strings.HasPrefix(r.URL.Path, "/assets/") {
		return false
	}
	for id, name := range f.names {
		if r.URL.Path != fmt.Sprintf("/assets/%d", id+1) {
			continue
		}
		if r.Header.Get("Accept") != "application/octet-stream" {
			w.WriteHeader(http.StatusNotAcceptable)
			return true
		}
		_, _ = w.Write(f.files[name])
		return true
	}
	return false
}

func privateUpdater(server *httptest.Server, token string) *Updater {
	return &Updater{
		APIBaseURL: server.URL,
		HTTPClient: server.Client(),
		GOOS:       "linux",
		GOARCH:     "amd64",
		Token:      token,
	}
}

func TestDownloadVerifiedBinaryPrivateViaAssetsAPI(t *testing.T) {
	const token = "secret-token-123"
	name := "ai-launcher_0.2.0_linux_amd64.tar.gz"
	archive := tarGzArchive(t, "ai-launcher", testBinaryContents)
	server, requests := privateReleaseServer(t, token, "v0.2.0", map[string][]byte{
		name:            archive,
		"checksums.txt": checksumsBody(name, archive),
	})
	binary, err := privateUpdater(server, token).DownloadVerifiedBinary(context.Background(), "v0.2.0")
	if err != nil {
		t.Fatal(err)
	}
	if string(binary) != testBinaryContents {
		t.Fatalf("binary = %q, want %q", binary, testBinaryContents)
	}
	var sawReleaseByTag, sawOctetAssets int
	for _, req := range *requests {
		if req.auth != "Bearer "+token {
			t.Fatalf("request %s missing the Bearer token: %q", req.path, req.auth)
		}
		switch {
		case req.path == "/releases/tags/v0.2.0":
			sawReleaseByTag++
		case strings.HasPrefix(req.path, "/assets/"):
			if req.accept != "application/octet-stream" {
				t.Fatalf("asset request accept = %q, want application/octet-stream", req.accept)
			}
			sawOctetAssets++
		case strings.HasPrefix(req.path, "/v0.2.0/"):
			t.Fatalf("pretty download URL %s must not be used with a token", req.path)
		}
	}
	if sawReleaseByTag == 0 || sawOctetAssets != 2 {
		t.Fatalf("requests = %+v, want release-by-tag plus two asset downloads", *requests)
	}
}

func TestDownloadVerifiedBinaryPrivateMissingAsset(t *testing.T) {
	const token = "secret-token-123"
	server, _ := privateReleaseServer(t, token, "v0.2.0", map[string][]byte{
		"checksums.txt": []byte("deadbeef  other.tar.gz\n"),
	})
	_, err := privateUpdater(server, token).DownloadVerifiedBinary(context.Background(), "v0.2.0")
	if err == nil || !strings.Contains(err.Error(), "not found in release v0.2.0") {
		t.Fatalf("err = %v, want a missing-asset error", err)
	}
}

func TestDownloadVerifiedBinaryNoTokenKeepsPrettyURL(t *testing.T) {
	const token = "secret-token-123"
	server, requests := privateReleaseServer(t, token, "v0.2.0", nil)
	// Without a token the updater must keep using the pretty download URL
	// (and fail here, as this fake private repo 404s it without auth).
	updater := &Updater{DownloadBaseURL: server.URL, HTTPClient: server.Client(), GOOS: "linux", GOARCH: "amd64"}
	_, err := updater.DownloadVerifiedBinary(context.Background(), "v0.2.0")
	if err == nil {
		t.Fatal("expected the unauthenticated pretty-URL download to fail")
	}
	if len(*requests) == 0 || !strings.HasPrefix((*requests)[0].path, "/v0.2.0/ai-launcher_") {
		t.Fatalf("requests = %+v, want the pretty download path", *requests)
	}
	for _, req := range *requests {
		if strings.HasPrefix(req.path, "/releases/tags/") {
			t.Fatalf("no-token flow must not use the assets API: %s", req.path)
		}
	}
}

func TestApplyPrivateEndToEnd(t *testing.T) {
	const token = "secret-token-123"
	name := "ai-launcher_0.2.0_linux_amd64.tar.gz"
	archive := tarGzArchive(t, "ai-launcher", testBinaryContents)
	server, _ := privateReleaseServer(t, token, "v0.2.0", map[string][]byte{
		name:            archive,
		"checksums.txt": checksumsBody(name, archive),
	})
	updater := privateUpdater(server, token)
	dir := t.TempDir()
	exe := filepath.Join(dir, "ai-launcher")
	if err := os.WriteFile(exe, []byte("old"), 0o755); err != nil { // #nosec G306 -- the fixture must look like an installed executable
		t.Fatal(err)
	}
	updater.ExecutablePath = exe
	if err := updater.Apply(context.Background(), "v0.2.0"); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(exe); string(data) != testBinaryContents { // #nosec G304 -- exe is the fixture path created by the test itself
		t.Fatalf("exe = %q, want the new binary", data)
	}
}

func TestTokenSentAsBearerAndNeverLeaked(t *testing.T) {
	const token = "secret-token-123"
	name := "ai-launcher_0.2.0_linux_amd64.tar.gz"
	archive := tarGzArchive(t, "ai-launcher", testBinaryContents)
	server, requests := privateReleaseServer(t, token, "v0.2.0", map[string][]byte{
		name:            archive,
		"checksums.txt": checksumsBody(name, archive),
	})
	if _, err := privateUpdater(server, token).DownloadVerifiedBinary(context.Background(), "v0.2.0"); err != nil {
		t.Fatal(err)
	}
	if len(*requests) == 0 {
		t.Fatal("server saw no requests")
	}
	for _, req := range *requests {
		if req.auth != "Bearer "+token {
			t.Fatalf("Authorization = %q, want Bearer token", req.auth)
		}
	}

	// A failing request must not leak the token into the error text.
	bad := privateUpdater(server, "wrong")
	_, err := bad.DownloadVerifiedBinary(context.Background(), "v0.2.0")
	if err == nil {
		t.Fatal("expected an error with a wrong token")
	}
	if strings.Contains(err.Error(), "wrong") {
		t.Fatalf("error leaked the token: %v", err)
	}
}

func TestReplaceExecutablePosixSwap(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "ai-launcher")
	if err := os.WriteFile(exe, []byte("old"), 0o755); err != nil { // #nosec G306 -- the fixture must look like an installed executable
		t.Fatal(err)
	}
	if err := replaceExecutableAt(exe, []byte("new-binary"), false); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(exe) // #nosec G304 -- exe is the fixture path created by the test itself
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "new-binary" {
		t.Fatalf("exe = %q, want new-binary", data)
	}
	info, err := os.Stat(exe)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o755 {
		t.Fatalf("mode = %o, want 755", info.Mode().Perm())
	}
	// No temp files left behind.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("dir entries = %d, want 1: %v", len(entries), entries)
	}
}

func TestReplaceExecutableWindowsMovesOldAside(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "ai-launcher.exe")
	if err := os.WriteFile(exe, []byte("old"), 0o755); err != nil { // #nosec G306 -- the fixture must look like an installed executable
		t.Fatal(err)
	}
	if err := replaceExecutableAt(exe, []byte("new-binary"), true); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(exe); string(data) != "new-binary" { // #nosec G304 -- exe is the fixture path created by the test itself
		t.Fatalf("exe = %q, want new-binary", data)
	}
	if data, err := os.ReadFile(exe + ".old"); err != nil || string(data) != "old" { // #nosec G304 -- the .old sibling of the test fixture
		t.Fatalf(".old = %q, %v; want the previous binary kept", data, err)
	}
}

func TestReplaceExecutableUnwritableDirHasClearMessage(t *testing.T) {
	// Point the target inside a path whose parent is a regular file so the
	// temp-file creation fails deterministically, even when tests run as root.
	dir := t.TempDir()
	blocker := filepath.Join(dir, "not-a-dir")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(blocker, "ai-launcher")
	err := replaceExecutableAt(exe, []byte("new"), false)
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "user-writable") {
		t.Fatalf("err = %v, want a user-writable location suggestion", err)
	}
}

func TestApplyEndToEnd(t *testing.T) {
	name := "ai-launcher_0.2.0_linux_amd64.tar.gz"
	archive := tarGzArchive(t, "ai-launcher", testBinaryContents)
	updater := downloadFixture(t, name, archive, checksumsBody(name, archive))
	dir := t.TempDir()
	exe := filepath.Join(dir, "ai-launcher")
	if err := os.WriteFile(exe, []byte("old"), 0o755); err != nil { // #nosec G306 -- the fixture must look like an installed executable
		t.Fatal(err)
	}
	updater.ExecutablePath = exe
	if err := updater.Apply(context.Background(), "v0.2.0"); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(exe); string(data) != testBinaryContents { // #nosec G304 -- exe is the fixture path created by the test itself
		t.Fatalf("exe = %q, want the new binary", data)
	}
}

func TestLatestTagBadJSON(t *testing.T) {
	server := releaseServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("not json"))
	}, nil)
	defer server.Close()
	updater := &Updater{APIBaseURL: server.URL, HTTPClient: server.Client()}
	if _, err := updater.LatestTag(context.Background()); err == nil || !strings.Contains(err.Error(), "parse release") {
		t.Fatalf("err = %v, want a parse error", err)
	}
}

func TestLatestTagNetworkError(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	url := server.URL
	server.Close() // nothing is listening anymore
	updater := &Updater{APIBaseURL: url}
	if _, err := updater.LatestTag(context.Background()); err == nil || !strings.Contains(err.Error(), "resolve latest release") {
		t.Fatalf("err = %v, want a resolve error", err)
	}
}

func TestLatestTagEmptyTagName(t *testing.T) {
	server := releaseServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/releases/latest" {
			_, _ = w.Write([]byte(`{"tag_name":""}`))
			return
		}
		http.NotFound(w, r)
	}, nil)
	defer server.Close()
	updater := &Updater{APIBaseURL: server.URL, HTTPClient: server.Client()}
	if _, err := updater.LatestTag(context.Background()); err == nil || !strings.Contains(err.Error(), "tag_name") {
		t.Fatalf("err = %v, want a tag_name error", err)
	}
}

func TestParseTagVersion(t *testing.T) {
	cases := []struct {
		tag  string
		want tagVersion
		ok   bool
	}{
		{"v1.2.3", tagVersion{major: 1, minor: 2, patch: 3}, true},
		{"0.10.4", tagVersion{major: 0, minor: 10, patch: 4}, true},
		{"v0.2.0-rc.1", tagVersion{major: 0, minor: 2, patch: 0}, true},
		{"v1.2", tagVersion{}, false},
		{"nightly", tagVersion{}, false},
		{"v1.2.x", tagVersion{}, false},
	}
	for _, tc := range cases {
		got, ok := parseTagVersion(tc.tag)
		if ok != tc.ok || got != tc.want {
			t.Errorf("parseTagVersion(%q) = %v, %v; want %v, %v", tc.tag, got, ok, tc.want, tc.ok)
		}
	}
}

func TestExecutablePathResolution(t *testing.T) {
	override := &Updater{ExecutablePath: "/tmp/custom-ai-launcher"}
	if path, err := override.executablePath(); err != nil || path != "/tmp/custom-ai-launcher" {
		t.Fatalf("override path = %q, %v", path, err)
	}
	// The default resolves the running test binary through symlinks.
	path, err := (&Updater{}).executablePath()
	if err != nil || path == "" {
		t.Fatalf("default path = %q, %v", path, err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("resolved path %q: %v", path, err)
	}
}

func TestDownloadVerifiedBinaryArchiveMissing(t *testing.T) {
	server := releaseServer(t, latestJSON("v0.2.0"), nil)
	defer server.Close()
	updater := &Updater{DownloadBaseURL: server.URL, HTTPClient: server.Client(), GOOS: "linux", GOARCH: "amd64"}
	_, err := updater.DownloadVerifiedBinary(context.Background(), "v0.2.0")
	if err == nil || !strings.Contains(err.Error(), "download ai-launcher_0.2.0_linux_amd64.tar.gz") {
		t.Fatalf("err = %v, want an archive download error", err)
	}
}

func TestDownloadVerifiedBinaryExtractionFails(t *testing.T) {
	name := "ai-launcher_0.2.0_linux_amd64.tar.gz"
	archive := tarGzArchive(t, "README.md", "docs only")
	updater := downloadFixture(t, name, archive, checksumsBody(name, archive))
	_, err := updater.DownloadVerifiedBinary(context.Background(), "v0.2.0")
	if err == nil || !strings.Contains(err.Error(), "extract") {
		t.Fatalf("err = %v, want an extraction error", err)
	}
}

func TestDefaultsAndGetters(t *testing.T) {
	updater := &Updater{}
	if updater.apiBase() != DefaultAPIBaseURL {
		t.Fatalf("apiBase = %q", updater.apiBase())
	}
	if updater.downloadBase() != DefaultDownloadBaseURL {
		t.Fatalf("downloadBase = %q", updater.downloadBase())
	}
	if updater.httpClient() == nil {
		t.Fatal("httpClient must never be nil")
	}
	custom := &Updater{APIBaseURL: "https://example.test/api/", DownloadBaseURL: "https://example.test/dl/", GOOS: "plan9", GOARCH: "mips"}
	if custom.apiBase() != "https://example.test/api" || custom.downloadBase() != "https://example.test/dl" {
		t.Fatalf("trailing slashes not trimmed: %q %q", custom.apiBase(), custom.downloadBase())
	}
	if custom.goos() != "plan9" || custom.goarch() != "mips" {
		t.Fatalf("platform = %s/%s", custom.goos(), custom.goarch())
	}
}

func TestCompareTags(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"v0.2.0", "v0.10.0", -1},
		{"v1.0.0", "v0.9.9", 1},
		{"v0.2.0", "0.2.0", 0},
		{"v0.2.0-rc.1", "v0.2.0-rc.2", 0}, // suffix ignored
		{"nightly", "v0.1.0", -1},         // non-semver falls back to strings
	}
	for _, tc := range cases {
		if got := compareTags(tc.a, tc.b); got != tc.want {
			t.Errorf("compareTags(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}
