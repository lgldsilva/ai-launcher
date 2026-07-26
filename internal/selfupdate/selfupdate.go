// Package selfupdate implements `ai-launcher upgrade`, the self-update flow
// that replaces the RUNNING executable with a release built by GoReleaser from
// this repository. It complements internal/installer (which manages
// third-party tools such as ai-jail and ai-memory) and reuses that package's
// traversal-guarded archive extraction and checksum parsing.
//
// Checksum verification is STRICT by design: the archive's SHA-256 must match
// the release checksums.txt, and a missing checksums.txt or a missing entry
// is a hard error. Unlike the semidx upgrader (which skips verification
// silently when checksums are absent), this flow overwrites the running
// binary, so an unverifiable download must never be installed.
package selfupdate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/lgldsilva/ai-launcher/internal/installer"
)

// Default endpoints. Both are overridable through the environment variables
// documented on the upgrade command (AI_LAUNCHER_UPDATE_API and
// AI_LAUNCHER_UPDATE_URL) so tests and future mirrors can redirect them.
const (
	// DefaultAPIBaseURL is the GitHub API base for this repository.
	DefaultAPIBaseURL = "https://api.github.com/repos/lgldsilva/ai-launcher"
	// DefaultDownloadBaseURL is the release download base for this repository.
	DefaultDownloadBaseURL = "https://github.com/lgldsilva/ai-launcher/releases/download"
)

const (
	checksumsAsset  = "checksums.txt"
	maxDownloadSize = 512 << 20
)

// HTTPError represents a non-2xx response from the release host.
type HTTPError struct {
	StatusCode int
	Status     string
	URL        string
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("GET %s: %s", e.URL, e.Status)
}

// Updater carries everything the self-update flow needs. Token authenticates
// against private release hosts (forks/mirrors); it is sent as a Bearer
// header and is never printed or logged.
type Updater struct {
	CurrentVersion  string
	APIBaseURL      string
	DownloadBaseURL string
	Token           string
	HTTPClient      *http.Client
	GOOS            string
	GOARCH          string
	// ExecutablePath overrides the resolved os.Executable() target; tests use
	// it to point the replacement at a fake binary in a temp directory.
	ExecutablePath string
}

func (u *Updater) httpClient() *http.Client {
	if u.HTTPClient != nil {
		return u.HTTPClient
	}
	return &http.Client{Timeout: 120 * time.Second}
}

func (u *Updater) apiBase() string {
	if strings.TrimSpace(u.APIBaseURL) != "" {
		return strings.TrimRight(u.APIBaseURL, "/")
	}
	return DefaultAPIBaseURL
}

func (u *Updater) downloadBase() string {
	if strings.TrimSpace(u.DownloadBaseURL) != "" {
		return strings.TrimRight(u.DownloadBaseURL, "/")
	}
	return DefaultDownloadBaseURL
}

func (u *Updater) goos() string {
	if u.GOOS != "" {
		return u.GOOS
	}
	return runtime.GOOS
}

func (u *Updater) goarch() string {
	if u.GOARCH != "" {
		return u.GOARCH
	}
	return runtime.GOARCH
}

// SameVersion reports whether current already matches tag, ignoring a leading
// "v". A "dev" or empty current version is never current, so an upgrade from
// a development build always proceeds.
func SameVersion(current, tag string) bool {
	if current == "" || current == "dev" {
		return false
	}
	return strings.TrimPrefix(current, "v") == strings.TrimPrefix(tag, "v")
}

// AssetName returns the GoReleaser archive name for tag on goos/goarch
// (ai-launcher_<version>_<os>_<arch>.tar.gz, or .zip on Windows).
func AssetName(tag, goos, goarch string) string {
	ext := "tar.gz"
	if goos == "windows" {
		ext = "zip"
	}
	return fmt.Sprintf("ai-launcher_%s_%s_%s.%s", strings.TrimPrefix(tag, "v"), goos, goarch, ext)
}

// BinaryName returns the executable name inside the archive.
func BinaryName(goos string) string {
	if goos == "windows" {
		return "ai-launcher.exe"
	}
	return "ai-launcher"
}

type releaseInfo struct {
	TagName    string `json:"tag_name"`
	Draft      bool   `json:"draft"`
	Prerelease bool   `json:"prerelease"`
}

// LatestTag resolves the newest published release tag: /releases/latest
// first, falling back on 404 to the release list, where the highest semver
// tag wins. Drafts are always skipped; prereleases are skipped unless no
// stable release exists.
func (u *Updater) LatestTag(ctx context.Context) (string, error) {
	base := u.apiBase()
	if tag, err := u.fetchLatestFromEndpoint(ctx, base+"/releases/latest"); err == nil {
		return tag, nil
	} else if !isNotFound(err) {
		return "", fmt.Errorf("resolve latest release: %w", err)
	}
	tag, err := u.fetchLatestFromList(ctx, base+"/releases?per_page=50")
	if err != nil {
		return "", fmt.Errorf("resolve latest release: %w", err)
	}
	return tag, nil
}

func (u *Updater) fetchLatestFromEndpoint(ctx context.Context, url string) (string, error) {
	body, err := u.get(ctx, url, "application/vnd.github+json")
	if err != nil {
		return "", err
	}
	var rel releaseInfo
	if err := json.Unmarshal(body, &rel); err != nil {
		return "", fmt.Errorf("parse release: %w", err)
	}
	if rel.TagName == "" {
		return "", errors.New("latest release has no tag_name")
	}
	return rel.TagName, nil
}

func (u *Updater) fetchLatestFromList(ctx context.Context, url string) (string, error) {
	body, err := u.get(ctx, url, "application/vnd.github+json")
	if err != nil {
		return "", err
	}
	var releases []releaseInfo
	if err := json.Unmarshal(body, &releases); err != nil {
		return "", fmt.Errorf("parse release list: %w", err)
	}
	var stable, prerelease []string
	for _, rel := range releases {
		if rel.Draft || rel.TagName == "" {
			continue
		}
		if rel.Prerelease {
			prerelease = append(prerelease, rel.TagName)
		} else {
			stable = append(stable, rel.TagName)
		}
	}
	tags := stable
	if len(tags) == 0 {
		tags = prerelease
	}
	if len(tags) == 0 {
		return "", errors.New("no published releases found")
	}
	sort.Slice(tags, func(i, j int) bool { return compareTags(tags[i], tags[j]) < 0 })
	return tags[len(tags)-1], nil
}

// DownloadVerifiedBinary downloads the release archive for tag and this
// platform, verifies its SHA-256 against checksums.txt (strictly), and
// returns the extracted executable bytes. When the updater carries a token
// the downloads go through the GitHub assets API (release-by-tag → asset by
// name → octet-stream GET): on PRIVATE repositories the pretty
// github.com/.../releases/download/<tag>/<asset> URLs return 404 even with a
// valid token, while the API asset URL works. Without a token the public
// download URLs are used directly.
func (u *Updater) DownloadVerifiedBinary(ctx context.Context, tag string) ([]byte, error) {
	archive := AssetName(tag, u.goos(), u.goarch())
	data, err := u.downloadAsset(ctx, tag, archive)
	if err != nil {
		return nil, fmt.Errorf("download %s: %w", archive, err)
	}
	sums, err := u.downloadAsset(ctx, tag, checksumsAsset)
	if err != nil {
		return nil, fmt.Errorf("download %s: %w (checksum verification is mandatory for self-update)", checksumsAsset, err)
	}
	want, err := installer.ChecksumFor(sums, archive)
	if err != nil {
		return nil, fmt.Errorf("verify %s: %w", archive, err)
	}
	digest := sha256.Sum256(data)
	if got := hex.EncodeToString(digest[:]); !strings.EqualFold(want, got) {
		return nil, fmt.Errorf("sha256 mismatch for %s: expected %s, got %s", archive, want, got)
	}
	binary, err := installer.ExtractArchiveBinary(data, archive, BinaryName(u.goos()))
	if err != nil {
		return nil, fmt.Errorf("extract %s: %w", archive, err)
	}
	return binary, nil
}

// downloadAsset fetches one named release file for tag. With a token it goes
// through the assets API; without one it uses the public download URL.
func (u *Updater) downloadAsset(ctx context.Context, tag, name string) ([]byte, error) {
	if u.Token == "" {
		return u.get(ctx, u.downloadBase()+"/"+tag+"/"+name, "application/octet-stream")
	}
	return u.assetBytesViaAPI(ctx, tag, name)
}

type releaseAsset struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

// assetBytesViaAPI resolves the release for tag through the API, finds the
// named asset, and downloads it from its API URL (which accepts Bearer auth
// and redirects to a signed URL that the HTTP client follows).
func (u *Updater) assetBytesViaAPI(ctx context.Context, tag, name string) ([]byte, error) {
	body, err := u.get(ctx, u.apiBase()+"/releases/tags/"+tag, "application/vnd.github+json")
	if err != nil {
		return nil, err
	}
	var release struct {
		Assets []releaseAsset `json:"assets"`
	}
	if err := json.Unmarshal(body, &release); err != nil {
		return nil, fmt.Errorf("parse release %s: %w", tag, err)
	}
	for _, asset := range release.Assets {
		if asset.Name == name && asset.URL != "" {
			return u.get(ctx, asset.URL, "application/octet-stream")
		}
	}
	return nil, fmt.Errorf("asset %s not found in release %s", name, tag)
}

// Apply downloads tag and atomically replaces the running executable with it.
func (u *Updater) Apply(ctx context.Context, tag string) error {
	binary, err := u.DownloadVerifiedBinary(ctx, tag)
	if err != nil {
		return err
	}
	exe, err := u.executablePath()
	if err != nil {
		return err
	}
	return replaceExecutableAt(exe, binary, u.goos() == "windows")
}

// executablePath resolves the running executable through symlinks so the
// swap replaces the real binary rather than a link to it.
func (u *Updater) executablePath() (string, error) {
	if u.ExecutablePath != "" {
		return u.ExecutablePath, nil
	}
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("locate running executable: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	return exe, nil
}

// replaceExecutableAt writes the new bytes next to exePath and atomically
// renames them into place. On POSIX a rename over the running binary is safe
// (the process keeps the old inode; the next launch uses the new file). On
// Windows a running .exe cannot be overwritten, so the old one is moved aside
// first with a best-effort rollback. The project never uses sudo: when the
// install directory is not writable the error says so and suggests a
// user-writable location.
func replaceExecutableAt(exePath string, binary []byte, windows bool) error {
	dir := filepath.Dir(exePath)
	tmp, err := os.CreateTemp(dir, ".ai-launcher-upgrade-*")
	if err != nil {
		return fmt.Errorf("write to %s: %w — install ai-launcher to a user-writable location (e.g. ~/.local/bin) instead of a system directory", dir, err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if _, err := tmp.Write(binary); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write %s: %w", tmpName, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("write %s: %w", tmpName, err)
	}
	if err := os.Chmod(tmpName, 0o755); err != nil { // #nosec G302 -- the file is the executable being installed
		return fmt.Errorf("chmod %s: %w", tmpName, err)
	}
	if windows {
		return replaceExecutableWindows(exePath, tmpName)
	}
	if err := os.Rename(tmpName, exePath); err != nil {
		return fmt.Errorf("replace %s: %w", exePath, err)
	}
	return nil
}

func replaceExecutableWindows(exePath, tmpName string) error {
	old := exePath + ".old"
	_ = os.Remove(old)
	if err := os.Rename(exePath, old); err != nil {
		return fmt.Errorf("move current executable aside: %w", err)
	}
	if err := os.Rename(tmpName, exePath); err != nil {
		_ = os.Rename(old, exePath) // best-effort rollback
		return fmt.Errorf("replace %s: %w", exePath, err)
	}
	return nil
}

// get fetches a URL and returns the body, erroring on non-2xx. When the
// updater carries a token it is sent as a Bearer header; the token never
// appears in errors or output.
func (u *Updater) get(ctx context.Context, url, accept string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", accept)
	req.Header.Set("User-Agent", "ai-launcher")
	if u.Token != "" {
		req.Header.Set("Authorization", "Bearer "+u.Token)
	}
	resp, err := u.httpClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode/100 != 2 {
		return nil, &HTTPError{StatusCode: resp.StatusCode, Status: resp.Status, URL: url}
	}
	return io.ReadAll(io.LimitReader(resp.Body, maxDownloadSize))
}

func isNotFound(err error) bool {
	var httpErr *HTTPError
	return errors.As(err, &httpErr) && httpErr.StatusCode == http.StatusNotFound
}

type tagVersion struct{ major, minor, patch int }

func (v tagVersion) less(o tagVersion) bool {
	if v.major != o.major {
		return v.major < o.major
	}
	if v.minor != o.minor {
		return v.minor < o.minor
	}
	return v.patch < o.patch
}

// parseTagVersion extracts the leading X.Y.Z from a tag ("v0.2.0", "0.2.0",
// "v0.2.0-rc.1"), reporting false when the tag does not start with semver.
func parseTagVersion(tag string) (tagVersion, bool) {
	core := strings.TrimPrefix(tag, "v")
	if idx := strings.IndexAny(core, "-+"); idx >= 0 {
		core = core[:idx]
	}
	parts := strings.Split(core, ".")
	if len(parts) < 3 {
		return tagVersion{}, false
	}
	nums := make([]int, 3)
	for i := 0; i < 3; i++ {
		n, err := strconv.Atoi(parts[i])
		if err != nil {
			return tagVersion{}, false
		}
		nums[i] = n
	}
	return tagVersion{major: nums[0], minor: nums[1], patch: nums[2]}, true
}

// compareTags orders release tags by their leading semver, falling back to
// plain string comparison for non-semver tags.
func compareTags(a, b string) int {
	av, aok := parseTagVersion(a)
	bv, bok := parseTagVersion(b)
	if aok && bok {
		if av.less(bv) {
			return -1
		}
		if bv.less(av) {
			return 1
		}
		return 0
	}
	return strings.Compare(a, b)
}
