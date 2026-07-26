// Package installer downloads configured executables from GitHub releases.
package installer

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
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
	"strings"
	"time"

	"github.com/lgldsilva/ai-launcher/internal/config"
)

const maxDownloadSize int64 = 512 << 20

// Installer downloads and verifies executables from GitHub releases and
// trusted HTTPS sources, tracking installed versions in a state file.
type Installer struct {
	HTTPClient *http.Client
	APIBaseURL string
	HomeDir    string
	StatePath  string
	GOOS       string
	GOARCH     string
	CurrentDir string
}

// Result reports the outcome of an install attempt: Status is one of
// "installed", "current", or "unconfigured".
type Result struct {
	Name    string
	Version string
	Path    string
	Status  string
}

type releaseResponse struct {
	TagName string  `json:"tag_name"`
	Body    string  `json:"body"`
	Assets  []Asset `json:"assets"`
}

// Asset is a GitHub release asset as returned by the GitHub API.
type Asset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
	URL                string `json:"url"`
	Digest             string `json:"digest"`
}

type state struct {
	Installs map[string]stateEntry `json:"installs"`
}

type stateEntry struct {
	Repository string `json:"repository"`
	Tag        string `json:"tag"`
	Asset      string `json:"asset"`
	Path       string `json:"path"`
	UpdatedAt  string `json:"updated_at"`
}

// New returns an Installer rooted at homeDir using the public GitHub API.
func New(homeDir string) *Installer {
	return &Installer{
		HTTPClient: http.DefaultClient,
		APIBaseURL: "https://api.github.com",
		HomeDir:    homeDir,
		GOOS:       runtime.GOOS,
		GOARCH:     runtime.GOARCH,
		CurrentDir: mustCurrentDir(),
	}
}

// Install downloads, checksum-verifies, and installs the release asset for
// the current platform. When force is false and the recorded install already
// matches the latest release it is a no-op returning Status "current".
func (i *Installer) Install(ctx context.Context, name, command, configuredPath string, release *config.GitHubRelease, force bool) (Result, error) {
	if release == nil {
		return Result{Name: name, Status: "unconfigured"}, fmt.Errorf("%s has no GitHub release recipe", name)
	}
	if strings.TrimSpace(release.Repository) == "" {
		return Result{Name: name}, errors.New("release repository is empty")
	}
	if strings.TrimSpace(command) == "" {
		return Result{Name: name}, errors.New("release command is empty")
	}
	platform := i.platform()
	assetPattern := strings.TrimSpace(release.Assets[platform])
	if assetPattern == "" {
		return Result{Name: name}, fmt.Errorf("%s has no release asset for %s", name, platform)
	}

	latest, err := i.latestRelease(ctx, release.Repository)
	if err != nil {
		return Result{Name: name}, err
	}
	asset, err := selectAsset(latest.Assets, assetPattern)
	if err != nil {
		return Result{Name: name}, fmt.Errorf("%s: %w", name, err)
	}
	target, err := i.targetPath(configuredPath, command)
	if err != nil {
		return Result{Name: name}, fmt.Errorf("%s: %w", name, err)
	}

	currentState, err := i.loadState()
	if err != nil {
		return Result{Name: name}, err
	}
	if !force && isExecutable(target) {
		if saved, ok := currentState.Installs[target]; ok && saved.Repository == release.Repository && saved.Tag == latest.TagName && saved.Asset == asset.Name {
			return Result{Name: name, Version: latest.TagName, Path: target, Status: "current"}, nil
		}
	}

	archiveBytes, err := i.download(ctx, asset)
	if err != nil {
		return Result{Name: name}, fmt.Errorf("download %s: %w", asset.Name, err)
	}
	if err := i.verifyChecksum(ctx, latest.Assets, asset, archiveBytes, release, latest.Body); err != nil {
		return Result{Name: name}, fmt.Errorf("verify %s: %w", asset.Name, err)
	}
	binaryBytes, err := extractBinary(archiveBytes, asset.Name, release.Binary, command)
	if err != nil {
		return Result{Name: name}, fmt.Errorf("extract %s: %w", asset.Name, err)
	}
	if err := i.installFile(target, binaryBytes); err != nil {
		return Result{Name: name}, fmt.Errorf("install %s: %w", name, err)
	}
	currentState.Installs[target] = stateEntry{
		Repository: release.Repository,
		Tag:        latest.TagName,
		Asset:      asset.Name,
		Path:       target,
		UpdatedAt:  time.Now().UTC().Format(time.RFC3339),
	}
	if err := i.saveState(currentState); err != nil {
		return Result{Name: name, Version: latest.TagName, Path: target}, err
	}
	return Result{Name: name, Version: latest.TagName, Path: target, Status: "installed"}, nil
}

// InstallSource installs a versionless wrapper or script from a trusted HTTPS
// source. It is intentionally separate from releases: ai-memory's command is
// a shell wrapper while its native runner is managed by the wrapper itself.
func (i *Installer) InstallSource(ctx context.Context, name, command, configuredPath, sourceURL string, force bool) (Result, error) {
	if strings.TrimSpace(sourceURL) == "" {
		return Result{Name: name}, errors.New("source URL is empty")
	}
	if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(sourceURL)), "https://") {
		return Result{Name: name}, errors.New("source URL must use HTTPS")
	}
	target, err := i.targetPath(configuredPath, command)
	if err != nil {
		return Result{Name: name}, fmt.Errorf("%s: %w", name, err)
	}
	data, err := i.download(ctx, Asset{Name: sourceURL, BrowserDownloadURL: sourceURL})
	if err != nil {
		return Result{Name: name}, fmt.Errorf("download %s: %w", name, err)
	}
	if len(data) == 0 || !bytes.HasPrefix(data, []byte("#!")) {
		return Result{Name: name}, errors.New("source does not look like an executable script")
	}
	if !force {
		if existing, readErr := os.ReadFile(target); readErr == nil && bytes.Equal(existing, data) && isExecutable(target) { // #nosec G304 -- target is the install path derived from the user's own configuration
			return Result{Name: name, Path: target, Status: "current"}, nil
		}
	}
	if err := i.installFile(target, data); err != nil {
		return Result{Name: name}, fmt.Errorf("install %s: %w", name, err)
	}
	currentState, err := i.loadState()
	if err != nil {
		return Result{Name: name}, err
	}
	currentState.Installs[target] = stateEntry{Repository: sourceURL, Tag: "source", Asset: sourceURL, Path: target, UpdatedAt: time.Now().UTC().Format(time.RFC3339)}
	if err := i.saveState(currentState); err != nil {
		return Result{Name: name, Path: target}, err
	}
	return Result{Name: name, Path: target, Status: "installed"}, nil
}

func (i *Installer) platform() string {
	return i.goos() + "-" + i.goarch()
}

func (i *Installer) goos() string {
	if i.GOOS != "" {
		return i.GOOS
	}
	return runtime.GOOS
}

func (i *Installer) goarch() string {
	if i.GOARCH != "" {
		return i.GOARCH
	}
	return runtime.GOARCH
}

func (i *Installer) latestRelease(ctx context.Context, repository string) (releaseResponse, error) {
	parts := strings.Split(strings.Trim(repository, "/"), "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return releaseResponse{}, fmt.Errorf("invalid GitHub repository %q", repository)
	}
	base := strings.TrimRight(i.APIBaseURL, "/")
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/repos/"+parts[0]+"/"+parts[1]+"/releases/latest", nil)
	if err != nil {
		return releaseResponse{}, fmt.Errorf("create GitHub request: %w", err)
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("User-Agent", "ai-launcher")
	if token := os.Getenv("GITHUB_TOKEN"); token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	client := i.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	response, err := client.Do(request)
	if err != nil {
		return releaseResponse{}, fmt.Errorf("query GitHub release: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return releaseResponse{}, fmt.Errorf("GitHub release request returned %s: %s", response.Status, strings.TrimSpace(string(body)))
	}
	var latest releaseResponse
	if err := json.NewDecoder(io.LimitReader(response.Body, maxDownloadSize)).Decode(&latest); err != nil {
		return releaseResponse{}, fmt.Errorf("decode GitHub release: %w", err)
	}
	if latest.TagName == "" {
		return releaseResponse{}, errors.New("GitHub latest release has no tag")
	}
	return latest, nil
}

func selectAsset(assets []Asset, pattern string) (Asset, error) {
	var matches []Asset
	for _, asset := range assets {
		matched, err := filepath.Match(pattern, asset.Name)
		if err != nil {
			return Asset{}, fmt.Errorf("invalid asset pattern %q: %w", pattern, err)
		}
		if matched || asset.Name == pattern {
			matches = append(matches, asset)
		}
	}
	if len(matches) == 0 {
		return Asset{}, fmt.Errorf("release asset %q not found", pattern)
	}
	sort.Slice(matches, func(a, b int) bool { return matches[a].Name < matches[b].Name })
	return matches[0], nil
}

func (i *Installer) download(ctx context.Context, asset Asset) ([]byte, error) {
	assetURL := asset.BrowserDownloadURL
	if assetURL == "" {
		assetURL = asset.URL
	}
	if assetURL == "" {
		return nil, errors.New("release asset has no download URL")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, assetURL, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "application/octet-stream")
	request.Header.Set("User-Agent", "ai-launcher")
	client := i.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("download returned %s", response.Status)
	}
	if response.ContentLength > maxDownloadSize {
		return nil, fmt.Errorf("download exceeds %d bytes", maxDownloadSize)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxDownloadSize+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxDownloadSize {
		return nil, fmt.Errorf("download exceeds %d bytes", maxDownloadSize)
	}
	return data, nil
}

func (i *Installer) verifyChecksum(ctx context.Context, assets []Asset, archive Asset, data []byte, release *config.GitHubRelease, releaseBody string) error {
	if digest := strings.TrimSpace(archive.Digest); digest != "" {
		const prefix = "sha256:"
		expected := strings.TrimPrefix(strings.ToLower(digest), prefix)
		if !isSHA256(expected) {
			return fmt.Errorf("unsupported GitHub asset digest %q", digest)
		}
		actual := sha256.Sum256(data)
		if !strings.EqualFold(expected, hex.EncodeToString(actual[:])) {
			return fmt.Errorf("sha256 mismatch: expected %s, got %s", expected, hex.EncodeToString(actual[:]))
		}
		return nil
	}
	checksumAsset, ok := findChecksumAsset(assets, archive.Name, release.ChecksumAsset)
	if !ok {
		if expected, err := checksumFor([]byte(releaseBody), archive.Name); err == nil {
			digest := sha256.Sum256(data)
			actual := hex.EncodeToString(digest[:])
			if !strings.EqualFold(expected, actual) {
				return fmt.Errorf("sha256 mismatch: expected %s, got %s", expected, actual)
			}
			return nil
		}
		if release.AllowUnverified {
			return nil
		}
		return errors.New("no checksum asset found; set allow_unverified only when the release provides another trusted integrity mechanism")
	}
	checksumData, err := i.download(ctx, checksumAsset)
	if err != nil {
		return fmt.Errorf("download checksum %s: %w", checksumAsset.Name, err)
	}
	expected, err := checksumFor(checksumData, archive.Name)
	if err != nil {
		return err
	}
	digest := sha256.Sum256(data)
	actual := hex.EncodeToString(digest[:])
	if !strings.EqualFold(expected, actual) {
		return fmt.Errorf("sha256 mismatch: expected %s, got %s", expected, actual)
	}
	return nil
}

func findChecksumAsset(assets []Asset, archiveName, configured string) (Asset, bool) {
	candidates := []string{}
	if strings.TrimSpace(configured) != "" {
		candidates = append(candidates, configured)
	} else {
		candidates = append(candidates, archiveName+".sha256", archiveName+".sha256sum", "checksums.txt", "SHA256SUMS", "sha256sums.txt")
	}
	for _, candidate := range candidates {
		for _, asset := range assets {
			matched, err := filepath.Match(candidate, asset.Name)
			if (err == nil && matched) || asset.Name == candidate {
				return asset, true
			}
		}
	}
	return Asset{}, false
}

func checksumFor(data []byte, filename string) (string, error) {
	base := filepath.Base(filename)
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.Trim(strings.TrimSpace(strings.ReplaceAll(line, "`", "")), "- ")
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if hash, ok := checksumFromBSDLine(line, filename, base); ok {
			return hash, nil
		}
		fields := strings.Fields(line)
		if hash, ok := checksumFromFields(fields, filename, base); ok {
			return hash, nil
		}
	}
	// No fallback: a lone hash or a key=<sha256> pair anchors to nothing, so a
	// checksums file (or release body) mentioning the hash of a DIFFERENT
	// asset would satisfy verification. Only filename-anchored lines count.
	return "", fmt.Errorf("checksum for %s not found", filename)
}

// checksumFromBSDLine parses the BSD shasum format
// "SHA256 (<name>) = <hash>" and reports the hash when the parenthesized
// name matches the wanted filename.
func checksumFromBSDLine(line, filename, base string) (string, bool) {
	if !strings.HasPrefix(line, "SHA256 (") {
		return "", false
	}
	name, rest, found := strings.Cut(strings.TrimPrefix(line, "SHA256 ("), ")")
	if !found {
		return "", false
	}
	hash := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(rest), "="))
	if !isSHA256(hash) {
		return "", false
	}
	if name == filename || filepath.Base(name) == base {
		return strings.ToLower(hash), true
	}
	return "", false
}

// checksumFromFields locates a sha256 token on a checksum-file line and
// reports it when the accompanying name matches the wanted filename. It
// accepts both "hash name" and "name hash" column orders.
func checksumFromFields(fields []string, filename, base string) (string, bool) {
	if len(fields) < 2 {
		return "", false
	}
	for _, index := range []int{0, 1} {
		if index >= len(fields) || !isSHA256(fields[index]) {
			continue
		}
		var candidateName string
		if index == 0 {
			candidateName = strings.Join(fields[1:], " ")
		} else {
			candidateName = strings.Join(fields[:index], " ")
		}
		candidateName = strings.TrimSpace(strings.TrimPrefix(candidateName, "*"))
		if candidateName == filename || filepath.Base(candidateName) == base {
			return strings.ToLower(fields[index]), true
		}
	}
	return "", false
}

func isSHA256(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

// ExtractArchiveBinary extracts the named binary from a downloaded release
// archive (tar.gz, tgz, or zip, detected from the asset name), applying the
// same path-traversal guard used for third-party installs. It is exported for
// the self-update flow, which consumes the same GoReleaser archive layout but
// replaces the running executable instead of installing another tool.
func ExtractArchiveBinary(data []byte, assetName, binaryName string) ([]byte, error) {
	return extractBinary(data, assetName, binaryName, binaryName)
}

// ChecksumFor returns the SHA-256 hex recorded for filename inside a checksum
// file body such as GoReleaser's checksums.txt. It is exported for the
// self-update flow, which verifies the release archive against checksums.txt
// before replacing the running executable.
func ChecksumFor(data []byte, filename string) (string, error) {
	return checksumFor(data, filename)
}

func extractBinary(data []byte, assetName, binary, command string) ([]byte, error) {
	name := strings.TrimSpace(binary)
	if name == "" {
		name = command
	}
	lower := strings.ToLower(assetName)
	if strings.HasSuffix(lower, ".zip") {
		return extractZip(data, name)
	}
	if strings.HasSuffix(lower, ".tar.gz") || strings.HasSuffix(lower, ".tgz") {
		return extractTarGz(data, name)
	}
	return data, nil
}

func extractTarGz(data []byte, binary string) ([]byte, error) {
	reader, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	defer func() { _ = reader.Close() }()
	tarReader := tar.NewReader(reader)
	for {
		header, err := tarReader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		if err := validateArchivePath(header.Name); err != nil {
			return nil, err
		}
		if header.Typeflag != tar.TypeReg || !archiveNameMatches(header.Name, binary) {
			continue
		}
		return io.ReadAll(io.LimitReader(tarReader, maxDownloadSize))
	}
	return nil, fmt.Errorf("binary %q not found in archive", binary)
}

func extractZip(data []byte, binary string) ([]byte, error) {
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, err
	}
	for _, file := range reader.File {
		if err := validateArchivePath(file.Name); err != nil {
			return nil, err
		}
		if file.FileInfo().IsDir() || !archiveNameMatches(file.Name, binary) {
			continue
		}
		opened, err := file.Open()
		if err != nil {
			return nil, err
		}
		contents, readErr := io.ReadAll(io.LimitReader(opened, maxDownloadSize))
		closeErr := opened.Close()
		if readErr != nil {
			return nil, readErr
		}
		if closeErr != nil {
			return nil, closeErr
		}
		return contents, nil
	}
	return nil, fmt.Errorf("binary %q not found in archive", binary)
}

func archiveNameMatches(path, wanted string) bool {
	base := filepath.Base(path)
	wantedBase := filepath.Base(wanted)
	// Windows release archives ship the binary with the .exe suffix.
	return path == wanted || base == wantedBase || base == wantedBase+".exe"
}

func validateArchivePath(path string) error {
	clean := filepath.Clean(filepath.FromSlash(path))
	if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return fmt.Errorf("unsafe archive path %q", path)
	}
	return nil
}

func (i *Installer) targetPath(configured, command string) (string, error) {
	target := strings.TrimSpace(configured)
	if target == "" {
		if i.HomeDir == "" {
			return "", errors.New("home directory is unavailable and no install path was configured")
		}
		target = filepath.Join(i.HomeDir, ".local", "bin", command)
	} else if strings.HasPrefix(target, "~/") && i.HomeDir != "" {
		target = filepath.Join(i.HomeDir, strings.TrimPrefix(target, "~/"))
	}
	if !filepath.IsAbs(target) {
		base := i.CurrentDir
		if base == "" {
			base = "."
		}
		var err error
		target, err = filepath.Abs(filepath.Join(base, target))
		if err != nil {
			return "", err
		}
	}
	target = filepath.Clean(target)
	// Windows executables need the .exe suffix to be runnable and discoverable.
	if i.goos() == "windows" && !strings.HasSuffix(strings.ToLower(target), ".exe") {
		target += ".exe"
	}
	return target, nil
}

func isExecutable(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0o111 != 0
}

func (i *Installer) installFile(target string, data []byte) error {
	if len(data) == 0 {
		return errors.New("downloaded executable is empty")
	}
	if info, err := os.Stat(target); err == nil && info.IsDir() {
		return errors.New("install target is a directory")
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(target), ".ai-launcher-install-*")
	if err != nil {
		return err
	}
	temporaryName := temporary.Name()
	defer func() { _ = os.Remove(temporaryName) }()
	if err := temporary.Chmod(0o755); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryName, target)
}

func (i *Installer) loadState() (state, error) {
	path := i.statePath()
	result := state{Installs: map[string]stateEntry{}}
	data, err := os.ReadFile(path) // #nosec G304 -- path is the installer's own state file location derived from configuration
	if errors.Is(err, os.ErrNotExist) {
		return result, nil
	}
	if err != nil {
		return result, fmt.Errorf("read install state: %w", err)
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return state{}, fmt.Errorf("parse install state: %w", err)
	}
	if result.Installs == nil {
		result.Installs = map[string]stateEntry{}
	}
	return result, nil
}

func (i *Installer) saveState(value state) error {
	path := i.statePath()
	if path == "" {
		return nil
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("encode install state: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return fmt.Errorf("create install state directory: %w", err)
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".ai-launcher-state-*")
	if err != nil {
		return fmt.Errorf("create install state: %w", err)
	}
	temporaryName := temporary.Name()
	defer func() { _ = os.Remove(temporaryName) }()
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryName, path); err != nil {
		return fmt.Errorf("replace install state: %w", err)
	}
	return nil
}

func (i *Installer) statePath() string {
	if i.StatePath != "" {
		return i.StatePath
	}
	if i.HomeDir == "" {
		return ""
	}
	return filepath.Join(i.HomeDir, ".config", "ai-launch", "install-state.json")
}

func mustCurrentDir() string {
	path, _ := os.Getwd()
	return path
}
