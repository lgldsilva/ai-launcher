package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/lgldsilva/ai-launcher/internal/config"
)

// buildLauncherLinux cross-compiles this binary for linux/amd64 into a temp
// file and returns its path. The image build runs the launcher's installer
// inside the container, so the binary must be a linux executable.
func buildLauncherLinux(out, errOut io.Writer) (string, error) {
	explicitSource := strings.TrimSpace(os.Getenv("GO_SRC_PATH"))
	src := launcherSourceRoot(explicitSource)
	if src == "" && explicitSource == "" {
		src = embeddedLauncherSourceRoot()
	}
	if src == "" && explicitSource == "" {
		if wd, err := os.Getwd(); err == nil {
			src = launcherSourceRoot(wd)
		}
	}
	if src == "" {
		if runtime.GOOS == "linux" {
			executable, err := os.Executable()
			if err == nil {
				return copyLauncherExecutable(executable)
			}
		}
		return downloadReleaseLauncher(out, errOut)
	}
	tmp, err := os.CreateTemp("", launcherTempPrefix)
	if err != nil {
		return "", err
	}
	path := tmp.Name()
	_ = tmp.Close()
	goToolchain, err := exec.LookPath("go")
	if err != nil {
		_ = os.Remove(path)
		return "", fmt.Errorf("go toolchain not found in PATH: %w", err)
	}
	cmd := exec.Command(goToolchain, "build", "-o", path, filepath.Join(src, "cmd", "ai-launcher")) // #nosec G204 G702 -- fixed argv, no shell, absolute path from LookPath
	cmd.Dir = src
	// Match the image architecture: an arm64 docker daemon (Raspberry Pi,
	// homelab) pulls an arm64 ubuntu base, so the COPY'd launcher must be
	// arm64 too — an amd64 binary would fail with "exec format error" (M6).
	// The host's own GOARCH is the best signal we have before inspecting the
	// daemon.
	cmd.Env = append(os.Environ(), "GOOS=linux", "GOARCH="+runtime.GOARCH)
	cmd.Stdout = out
	cmd.Stderr = errOut
	if err := cmd.Run(); err != nil {
		_ = os.Remove(path)
		return "", fmt.Errorf("cross-compile launcher for the image: %w", err)
	}
	return path, nil
}

// embeddedLauncherSourceRoot recovers the source checkout used to build a
// local development binary. Go keeps the source filename in the runtime
// metadata unless the binary was deliberately built with -trimpath; this lets
// a locally installed launcher keep working when invoked from another project
// directory, while release binaries still use the verified release fallback.
func embeddedLauncherSourceRoot() string {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return ""
	}
	return launcherSourceRoot(filepath.Dir(file))
}

const launcherModulePath = "github.com/lgldsilva/ai-launcher"

func launcherSourceRoot(start string) string {
	start = strings.TrimSpace(start)
	if start == "" {
		return ""
	}
	if info, err := os.Stat(start); err != nil || !info.IsDir() { // #nosec G703 -- start is a deliberate checkout path from GO_SRC_PATH or cwd.
		return ""
	}
	for dir := start; ; dir = filepath.Dir(dir) {
		data, err := os.ReadFile(filepath.Join(dir, "go.mod")) // #nosec G304,G703 -- dir stays within the deliberate checkout path.
		if err == nil && moduleDirective(data) == launcherModulePath {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
	}
}

func moduleDirective(data []byte) string {
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[0] == "module" {
			return fields[1]
		}
	}
	return ""
}

func copyLauncherExecutable(source string) (string, error) {
	data, err := os.ReadFile(source) // #nosec G304 -- source is os.Executable(), not user input.
	if err != nil {
		return "", fmt.Errorf("read current launcher: %w", err)
	}
	tmp, err := os.CreateTemp("", launcherTempPrefix)
	if err != nil {
		return "", fmt.Errorf("create launcher binary: %w", err)
	}
	path := tmp.Name()
	if err := tmp.Chmod(0o755); err != nil {
		_ = tmp.Close()
		_ = os.Remove(path)
		return "", fmt.Errorf("protect launcher binary: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(path)
		return "", fmt.Errorf("copy launcher binary: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(path)
		return "", fmt.Errorf("close launcher binary: %w", err)
	}
	return path, nil
}

func downloadReleaseLauncher(out, errOut io.Writer) (string, error) {
	release := strings.TrimPrefix(strings.TrimSpace(version), "v")
	if release == "" || release == "dev" || strings.ContainsAny(release, "/\\") {
		return "", errors.New("cannot cross-compile launcher: source checkout is unavailable and this binary has no published release version")
	}
	arch := runtime.GOARCH
	if arch != "amd64" && arch != "arm64" {
		return "", fmt.Errorf("cannot download Linux launcher for unsupported architecture %s", arch)
	}
	archiveName := fmt.Sprintf("ai-launcher_%s_linux_%s.tar.gz", release, arch)
	baseURL := "https://github.com/lgldsilva/ai-launcher/releases/download/v" + release
	client := &http.Client{Timeout: 2 * time.Minute}
	checksums, err := downloadReleaseBytes(client, baseURL+"/checksums.txt", 2<<20)
	if err != nil {
		return "", fmt.Errorf("download launcher checksums: %w", err)
	}
	wantHash, ok := releaseChecksum(string(checksums), archiveName)
	if !ok {
		return "", fmt.Errorf("launcher archive %s has no checksum in the release", archiveName)
	}
	archive, err := downloadReleaseBytes(client, baseURL+"/"+archiveName, 128<<20)
	if err != nil {
		return "", fmt.Errorf("download Linux launcher: %w", err)
	}
	actualHash := sha256.Sum256(archive)
	if !strings.EqualFold(hex.EncodeToString(actualHash[:]), wantHash) {
		return "", fmt.Errorf("launcher checksum mismatch: got %s, want %s", hex.EncodeToString(actualHash[:]), wantHash)
	}
	path, err := extractLauncherArchive(archive)
	if err != nil {
		return "", err
	}
	_, _ = fmt.Fprintf(out, "downloaded Linux launcher %s\n", archiveName)
	_ = errOut
	return path, nil
}

func downloadReleaseBytes(client *http.Client, url string, maxBytes int64) (data []byte, err error) {
	request, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("User-Agent", config.LauncherName)
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer func() {
		if closeErr := response.Body.Close(); err == nil && closeErr != nil {
			err = fmt.Errorf("close HTTP response: %w", closeErr)
		}
	}()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP status %s", response.Status)
	}
	limited := io.LimitReader(response.Body, maxBytes+1)
	data, err = io.ReadAll(limited)
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxBytes {
		return nil, fmt.Errorf("response exceeds %d bytes", maxBytes)
	}
	return data, nil
}

func releaseChecksum(checksums, filename string) (string, bool) {
	for _, line := range strings.Split(checksums, "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[1] == filename && len(fields[0]) == sha256.Size*2 {
			if _, err := hex.DecodeString(fields[0]); err == nil {
				return fields[0], true
			}
		}
	}
	return "", false
}

func extractLauncherArchive(data []byte) (path string, err error) {
	reader, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return "", fmt.Errorf("open launcher archive: %w", err)
	}
	defer func() {
		if closeErr := reader.Close(); err == nil && closeErr != nil {
			path = ""
			err = fmt.Errorf("close launcher archive: %w", closeErr)
		}
	}()
	tarReader := tar.NewReader(reader)
	for {
		header, err := tarReader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", fmt.Errorf("read launcher archive: %w", err)
		}
		if filepath.Base(header.Name) != "ai-launcher" || header.Typeflag != tar.TypeReg {
			continue
		}
		if header.Size <= 0 || header.Size > 64<<20 {
			return "", errors.New("launcher archive contains an invalid binary size")
		}
		binary, err := io.ReadAll(io.LimitReader(tarReader, 64<<20+1))
		if err != nil || int64(len(binary)) != header.Size {
			return "", errors.New("could not extract the complete launcher binary")
		}
		tmp, err := os.CreateTemp("", launcherTempPrefix)
		if err != nil {
			return "", fmt.Errorf("create extracted launcher: %w", err)
		}
		path = tmp.Name()
		if chmodErr := tmp.Chmod(0o755); chmodErr != nil {
			_ = tmp.Close()
			_ = os.Remove(path)
			return "", fmt.Errorf("make extracted launcher executable: %w", chmodErr)
		}
		if _, writeErr := tmp.Write(binary); writeErr != nil {
			_ = tmp.Close()
			_ = os.Remove(path)
			return "", fmt.Errorf("write extracted launcher: %w", writeErr)
		}
		closeErr := tmp.Close()
		if closeErr != nil {
			_ = os.Remove(path)
			return "", fmt.Errorf("close extracted launcher: %w", closeErr)
		}
		return path, nil
	}
	return "", errors.New("launcher archive does not contain ai-launcher")
}
