//go:build e2e

package e2e

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

type pinnedTool struct {
	name string
	url  string
	sha  string
	bin  string
}

var pinnedTools = []pinnedTool{
	{
		name: "ai-jail-2.15",
		url:  "https://github.com/akitaonrails/ai-jail/releases/download/v2.15.0/ai-jail-linux-x86_64.tar.gz",
		sha:  "d06b3cc09ee14a287f14e1d87b9cdf0bf3107bf025b6944829b5cc60227940c4",
		bin:  "ai-jail",
	},
	{
		name: "ai-jail-3.0.1",
		url:  "https://github.com/akitaonrails/ai-jail/releases/download/v3.0.1/ai-jail-linux-x86_64.tar.gz",
		sha:  "53c8b653bc1a693c7022e91ec47d81d8441a1bf813fbb737a35da224207b0e11",
		bin:  "ai-jail",
	},
	{
		name: "ai-memory-3.4.2",
		url:  "https://github.com/akitaonrails/ai-memory/releases/download/v3.4.2/ai-memory-linux-x86_64.tar.gz",
		sha:  "15a4f4a346a27e6fd63a36d1b3de23c5cbe142b80b3bce561e21b1f225a1e645",
		bin:  "ai-memory",
	},
}

func cacheDir() string {
	if dir := os.Getenv("E2E_CACHE"); dir != "" {
		return dir
	}
	return filepath.Join(os.TempDir(), "ai-launcher-e2e-cache")
}

func installPinnedTools(dir string) error {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	for _, tool := range pinnedTools {
		if err := installPinnedTool(dir, tool); err != nil {
			return err
		}
	}
	return nil
}

func installPinnedTool(dir string, tool pinnedTool) error {
	dest := filepath.Join(dir, tool.name)
	if _, err := os.Stat(dest); err == nil {
		return nil
	}
	archive := filepath.Join(dir, tool.name+".tar.gz")
	if err := ensureArchive(archive, tool); err != nil {
		return err
	}
	return extractBinary(archive, tool.bin, dest)
}

func ensureArchive(path string, tool pinnedTool) error {
	if checksum(path) == tool.sha {
		return nil
	}
	return download(tool.url, path, tool.sha)
}

func download(url, path, want string) error {
	client := &http.Client{Timeout: 3 * time.Minute}
	resp, err := client.Get(url) // #nosec G107 -- url is a pinned GitHub release asset, not operator input
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600) // #nosec G304 -- path is the e2e cache
	if err != nil {
		return err
	}
	defer file.Close()
	if _, err := io.Copy(file, resp.Body); err != nil {
		return err
	}
	if got := checksum(path); got != want {
		return fmt.Errorf("checksum %s: got %s want %s", path, got, want)
	}
	return nil
}

func checksum(path string) string {
	file, err := os.Open(path) // #nosec G304 -- path is the e2e cache
	if err != nil {
		return ""
	}
	defer file.Close()
	sum := sha256.New()
	if _, err := io.Copy(sum, file); err != nil {
		return ""
	}
	return hex.EncodeToString(sum.Sum(nil))
}

func extractBinary(archive, name, dest string) error {
	file, err := os.Open(archive) // #nosec G304 -- path is the e2e cache
	if err != nil {
		return err
	}
	defer file.Close()
	reader, err := gzip.NewReader(file)
	if err != nil {
		return err
	}
	defer reader.Close()
	tr := tar.NewReader(reader)
	for {
		header, err := tr.Next()
		if err == io.EOF {
			return fmt.Errorf("%s not found in %s", name, archive)
		}
		if err != nil {
			return err
		}
		if filepath.Base(header.Name) != name || header.Typeflag != tar.TypeReg {
			continue
		}
		return writeExecutable(dest, tr)
	}
}

func writeExecutable(dest string, src io.Reader) error {
	out, err := os.OpenFile(dest, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o700) // #nosec G302,G304 -- e2e cache binary
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, src)
	return err
}
