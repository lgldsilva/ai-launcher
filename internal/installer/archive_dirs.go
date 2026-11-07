package installer

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/lgldsilva/ai-launcher/internal/config"
)

// Release dirs: some tools need more than their executable. ai-memory's
// `install-hooks` and `setup` look for the hooks bundle shipped inside the
// release archive, so installing only the binary leaves every new machine
// unable to wire hooks. A GitHubRelease lists those dirs; they are staged
// beside their target first and swapped in only after the binary installs.

const stagingPattern = ".ai-launcher-*"

// archiveMember is one entry of a release archive, format-independent. open
// is valid only during the visit callback.
type archiveMember struct {
	name    string
	mode    os.FileMode
	dir     bool
	regular bool
	open    func() (io.ReadCloser, error)
}

type stagedDir struct {
	staging, target string
}

// stagedDirs holds extracted dirs waiting to replace their targets.
type stagedDirs []stagedDir

// dirsPresent reports whether every release dir already exists, so a current
// binary installed before the dirs were configured still gets them.
func dirsPresent(home string, dirs []config.ArchiveDir) bool {
	for _, dir := range dirs {
		target, err := dirTarget(home, dir)
		if err != nil {
			return false
		}
		if info, err := os.Stat(target); err != nil || !info.IsDir() {
			return false
		}
	}
	return true
}

// dirTarget resolves a release dir's destination, which must stay inside home.
func dirTarget(home string, dir config.ArchiveDir) (string, error) {
	clean := filepath.Clean(filepath.FromSlash(dir.Target))
	if dir.Target == "" || filepath.IsAbs(clean) || filepath.VolumeName(clean) != "" ||
		clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("release dir target %q must be a path inside the home directory", dir.Target)
	}
	return filepath.Join(home, clean), nil
}

// stageDirs extracts every release dir from the verified archive into a
// staging dir beside its target. On error nothing is left behind.
func (i *Installer) stageDirs(data []byte, assetName string, dirs []config.ArchiveDir) (stagedDirs, error) {
	staged := stagedDirs{}
	for _, dir := range dirs {
		one, err := i.stageDir(data, assetName, dir)
		if err != nil {
			staged.discard()
			return nil, err
		}
		staged = append(staged, one)
	}
	return staged, nil
}

func (i *Installer) stageDir(data []byte, assetName string, dir config.ArchiveDir) (stagedDir, error) {
	target, err := dirTarget(i.HomeDir, dir)
	if err != nil {
		return stagedDir{}, err
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
		return stagedDir{}, err
	}
	staging, err := os.MkdirTemp(filepath.Dir(target), filepath.Base(target)+stagingPattern)
	if err != nil {
		return stagedDir{}, err
	}
	if err := extractDir(data, assetName, strings.Trim(path.Clean(dir.Source), "/"), staging); err != nil {
		_ = os.RemoveAll(staging)
		return stagedDir{}, fmt.Errorf("release dir %q: %w", dir.Source, err)
	}
	return stagedDir{staging: staging, target: target}, nil
}

// commit swaps each staged dir into place; the previous contents go away so an
// upgrade leaves no stale files behind.
func (s stagedDirs) commit() error {
	for _, dir := range s {
		old := dir.staging + ".old"
		if err := os.Rename(dir.target, old); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err := os.Rename(dir.staging, dir.target); err != nil {
			return err
		}
		if err := os.RemoveAll(old); err != nil {
			return err
		}
	}
	return nil
}

// discard removes staging dirs that were not committed; a no-op after commit.
func (s stagedDirs) discard() {
	for _, dir := range s {
		_ = os.RemoveAll(dir.staging)
	}
}

// extractDir copies the archive members under source into dest. Only plain
// files and dirs are accepted: a link could point the hooks outside dest.
func extractDir(data []byte, assetName, source, dest string) error {
	found := false
	err := walkArchive(data, assetName, func(member archiveMember) error {
		name := strings.TrimPrefix(path.Clean("/"+member.name), "/")
		if name != source && !strings.HasPrefix(name, source+"/") {
			return nil
		}
		found = true
		return writeMember(member, filepath.Join(dest, filepath.FromSlash(strings.TrimPrefix(name, source))))
	})
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("%q not found in archive", source)
	}
	return nil
}

func writeMember(member archiveMember, dest string) error {
	if member.dir {
		return os.MkdirAll(dest, 0o750)
	}
	if !member.regular {
		return fmt.Errorf("%s: not a regular file", member.name)
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o750); err != nil {
		return err
	}
	reader, err := member.open()
	if err != nil {
		return err
	}
	defer func() { _ = reader.Close() }()
	contents, err := io.ReadAll(io.LimitReader(reader, maxDownloadSize))
	if err != nil {
		return err
	}
	mode := os.FileMode(0o644)
	if member.mode&0o111 != 0 {
		mode = 0o755
	}
	return os.WriteFile(dest, contents, mode) // #nosec G306 -- hook scripts must stay executable by their owner's agents
}

// walkArchive visits every member of a tar.gz/tgz or zip archive after the
// same path-traversal check the binary extraction applies.
func walkArchive(data []byte, assetName string, visit func(archiveMember) error) error {
	lower := strings.ToLower(assetName)
	switch {
	case strings.HasSuffix(lower, ".zip"):
		return walkZip(data, visit)
	case strings.HasSuffix(lower, ".tar.gz"), strings.HasSuffix(lower, ".tgz"):
		return walkTarGz(data, visit)
	}
	return fmt.Errorf("%s is not an archive", assetName)
}

func walkTarGz(data []byte, visit func(archiveMember) error) error {
	reader, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return err
	}
	defer func() { _ = reader.Close() }()
	tarReader := tar.NewReader(reader)
	for {
		header, err := tarReader.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		if err := validateArchivePath(header.Name); err != nil {
			return err
		}
		member := archiveMember{
			name: header.Name, mode: os.FileMode(header.Mode).Perm(), // #nosec G115 -- only the permission bits are kept
			dir: header.Typeflag == tar.TypeDir, regular: header.Typeflag == tar.TypeReg,
			open: func() (io.ReadCloser, error) { return io.NopCloser(tarReader), nil },
		}
		if err := visit(member); err != nil {
			return err
		}
	}
}

func walkZip(data []byte, visit func(archiveMember) error) error {
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return err
	}
	for _, file := range reader.File {
		if err := validateArchivePath(file.Name); err != nil {
			return err
		}
		member := archiveMember{
			name: file.Name, mode: file.Mode().Perm(),
			dir: file.FileInfo().IsDir(), regular: file.Mode().IsRegular(),
			open: file.Open,
		}
		if err := visit(member); err != nil {
			return err
		}
	}
	return nil
}
