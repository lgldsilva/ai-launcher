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
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lgldsilva/ai-launcher/internal/config"
)

type archiveEntry struct {
	name     string
	body     string
	mode     int64
	typeflag byte
}

const hooksTarget = ".local/share/ai-memory/hooks"

// memoryArchive mirrors the ai-memory release layout: the binary, the hooks
// bundle `install-hooks` looks for, and files the launcher must not copy.
func memoryArchive(extra ...archiveEntry) []archiveEntry {
	entries := []archiveEntry{
		{name: "./", typeflag: tar.TypeDir, mode: 0o755},
		{name: "./ai-memory", body: "#!/bin/sh\necho mem\n", mode: 0o755},
		{name: "./README.md", body: "readme", mode: 0o644},
		{name: "./hooks/", typeflag: tar.TypeDir, mode: 0o755},
		{name: "./hooks/claude-code/", typeflag: tar.TypeDir, mode: 0o755},
		{name: "./hooks/claude-code/session-start.sh", body: "#!/bin/sh\n", mode: 0o755},
		{name: "./hooks/_lib.sh", body: "lib", mode: 0o644},
	}
	return append(entries, extra...)
}

func tarGzEntries(t *testing.T, entries []archiveEntry) []byte {
	t.Helper()
	var buffer bytes.Buffer
	gzipWriter := gzip.NewWriter(&buffer)
	tarWriter := tar.NewWriter(gzipWriter)
	for _, entry := range entries {
		header := &tar.Header{Name: entry.name, Mode: entry.mode, Size: int64(len(entry.body)), Typeflag: entry.typeflag}
		if entry.typeflag == 0 {
			header.Typeflag = tar.TypeReg
		}
		if entry.typeflag == tar.TypeSymlink {
			header.Linkname, header.Size = entry.body, 0
		}
		if err := tarWriter.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		if header.Typeflag == tar.TypeReg {
			if _, err := tarWriter.Write([]byte(entry.body)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

// memoryReleaseServer serves *archive as the only asset of acme/mem, with a
// checksums.txt computed per request so a test can swap the archive.
func memoryReleaseServer(t *testing.T, asset string, archive *[]byte, tag *string) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/repos/acme/mem/releases/latest":
			_ = json.NewEncoder(response).Encode(releaseResponse{TagName: *tag, Assets: []Asset{
				{Name: asset, BrowserDownloadURL: serverURL(request, "/download/archive")},
				{Name: "checksums.txt", BrowserDownloadURL: serverURL(request, "/download/sums")},
			}})
		case "/download/archive":
			_, _ = response.Write(*archive)
		case "/download/sums":
			digest := sha256.Sum256(*archive)
			_, _ = response.Write([]byte(hex.EncodeToString(digest[:]) + "  " + asset + "\n"))
		default:
			http.NotFound(response, request)
		}
	}))
	t.Cleanup(server.Close)
	return server
}

func memoryInstaller(t *testing.T, server *httptest.Server) (*Installer, string) {
	t.Helper()
	root := t.TempDir()
	client := New(root)
	client.APIBaseURL = server.URL
	client.GOOS, client.GOARCH = "linux", "amd64"
	client.StatePath = filepath.Join(root, "state.json")
	return client, root
}

func memoryRelease(asset string) *config.GitHubRelease {
	return &config.GitHubRelease{
		Repository:    "acme/mem",
		Assets:        map[string]string{"linux-amd64": asset},
		Binary:        "ai-memory",
		ChecksumAsset: "checksums.txt",
		Dirs:          []config.ArchiveDir{{Source: "hooks", Target: hooksTarget}},
	}
}

func installMemory(t *testing.T, client *Installer, root string, release *config.GitHubRelease) (Result, error) {
	t.Helper()
	return client.Install(context.Background(), "ai-memory", "ai-memory", filepath.Join(root, "bin", "ai-memory"), release, false)
}

// Regression: ai-launcher installed only the ai-memory executable, so every
// `ai-memory install-hooks` / `ai-memory setup` on a new machine failed with
// "could not locate hooks directory".
func TestInstallCopiesReleaseDirsNextToTheBinary(t *testing.T) {
	archive, tag := tarGzEntries(t, memoryArchive()), "v3.4.3"
	server := memoryReleaseServer(t, "mem.tar.gz", &archive, &tag)
	client, root := memoryInstaller(t, server)

	result, err := installMemory(t, client, root, memoryRelease("mem.tar.gz"))
	if err != nil || result.Status != "installed" {
		t.Fatalf("Install() = %#v, err=%v", result, err)
	}
	hooks := filepath.Join(root, filepath.FromSlash(hooksTarget))
	script := filepath.Join(hooks, "claude-code", "session-start.sh")
	info, err := os.Stat(script)
	if err != nil || info.Mode().Perm()&0o111 == 0 {
		t.Fatalf("hook script = %v, err=%v; want an executable file", info, err)
	}
	if info, err := os.Stat(filepath.Join(hooks, "_lib.sh")); err != nil || info.Mode().Perm()&0o111 != 0 {
		t.Fatalf("_lib.sh = %v, err=%v; want a plain, non-executable file", info, err)
	}
	if _, err := os.Stat(filepath.Join(hooks, "README.md")); !os.IsNotExist(err) {
		t.Fatalf("README.md copied into hooks dir: err=%v", err)
	}
}

// Machines installed before the fix have a current binary and no hooks; a
// plain (non-forced) install must notice and finish the job.
func TestInstallRepairsMissingDirsEvenWhenBinaryIsCurrent(t *testing.T) {
	archive, tag := tarGzEntries(t, memoryArchive()), "v3.4.3"
	server := memoryReleaseServer(t, "mem.tar.gz", &archive, &tag)
	client, root := memoryInstaller(t, server)
	release := memoryRelease("mem.tar.gz")
	if _, err := installMemory(t, client, root, release); err != nil {
		t.Fatal(err)
	}
	hooks := filepath.Join(root, filepath.FromSlash(hooksTarget))
	if err := os.RemoveAll(hooks); err != nil {
		t.Fatal(err)
	}

	result, err := installMemory(t, client, root, release)
	if err != nil || result.Status != "installed" {
		t.Fatalf("repair Install() = %#v, err=%v; want a reinstall", result, err)
	}
	if _, err := os.Stat(filepath.Join(hooks, "claude-code", "session-start.sh")); err != nil {
		t.Fatalf("hooks not restored: %v", err)
	}
	again, err := installMemory(t, client, root, release)
	if err != nil || again.Status != "current" {
		t.Fatalf("third Install() = %#v, err=%v; want current", again, err)
	}
}

func TestInstallReplacesDirsOnUpgradeWithoutStaleFiles(t *testing.T) {
	archive, tag := tarGzEntries(t, memoryArchive(archiveEntry{name: "./hooks/claude-code/old.sh", body: "old", mode: 0o755})), "v1"
	server := memoryReleaseServer(t, "mem.tar.gz", &archive, &tag)
	client, root := memoryInstaller(t, server)
	release := memoryRelease("mem.tar.gz")
	if _, err := installMemory(t, client, root, release); err != nil {
		t.Fatal(err)
	}
	archive, tag = tarGzEntries(t, memoryArchive()), "v2"

	if result, err := installMemory(t, client, root, release); err != nil || result.Version != "v2" {
		t.Fatalf("upgrade Install() = %#v, err=%v", result, err)
	}
	hooks := filepath.Join(root, filepath.FromSlash(hooksTarget))
	if _, err := os.Stat(filepath.Join(hooks, "claude-code", "old.sh")); !os.IsNotExist(err) {
		t.Fatalf("stale hook survived the upgrade: err=%v", err)
	}
	leftovers, _ := filepath.Glob(hooks + ".ai-launcher-*")
	if len(leftovers) != 0 {
		t.Fatalf("staging dirs left behind: %v", leftovers)
	}
}

func TestInstallCopiesDirsFromZipArchives(t *testing.T) {
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	for name, body := range map[string]string{"ai-memory.exe": "MZ", "hooks/claude-code/session-start.ps1": "ps1"} {
		file, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = file.Write([]byte(body))
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	archive, tag := buffer.Bytes(), "v1"
	server := memoryReleaseServer(t, "mem.zip", &archive, &tag)
	client, root := memoryInstaller(t, server)

	if _, err := installMemory(t, client, root, memoryRelease("mem.zip")); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(hooksTarget), "claude-code", "session-start.ps1")) // #nosec G304 -- test temp dir
	if err != nil || string(got) != "ps1" {
		t.Fatalf("zip hook = %q, err=%v", got, err)
	}
}

func TestInstallRefusesUnsafeReleaseDirs(t *testing.T) {
	cases := map[string]struct {
		entries []archiveEntry
		dir     config.ArchiveDir
		want    string
	}{
		"symlink in dir": {
			entries: memoryArchive(archiveEntry{name: "./hooks/evil", body: "/etc/passwd", typeflag: tar.TypeSymlink}),
			dir:     config.ArchiveDir{Source: "hooks", Target: hooksTarget},
			want:    "not a regular file",
		},
		"absolute target": {
			entries: memoryArchive(),
			dir:     config.ArchiveDir{Source: "hooks", Target: "/etc/ai-memory"},
			want:    "inside the home directory",
		},
		"escaping target": {
			entries: memoryArchive(),
			dir:     config.ArchiveDir{Source: "hooks", Target: "../outside"},
			want:    "inside the home directory",
		},
		"source missing": {
			entries: memoryArchive(),
			dir:     config.ArchiveDir{Source: "nope", Target: hooksTarget},
			want:    `"nope" not found`,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			archive, tag := tarGzEntries(t, tc.entries), "v1"
			server := memoryReleaseServer(t, "mem.tar.gz", &archive, &tag)
			client, root := memoryInstaller(t, server)
			release := memoryRelease("mem.tar.gz")
			release.Dirs = []config.ArchiveDir{tc.dir}

			_, err := installMemory(t, client, root, release)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Install() error = %v; want %q", err, tc.want)
			}
			if _, statErr := os.Stat(filepath.Join(root, "bin", "ai-memory")); !os.IsNotExist(statErr) {
				t.Fatalf("binary installed despite the refused dir: %v", statErr)
			}
		})
	}
}
