package container

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSocketGroupIDReadsUnixGroupMetadata(t *testing.T) {
	path := filepath.Join(t.TempDir(), "socket-metadata")
	if err := os.WriteFile(path, []byte("fixture"), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	if groupID, err := SocketGroupID(path); err != nil || groupID < 0 {
		t.Fatalf("SocketGroupID(%q) = (%d, %v); want a non-negative group", path, groupID, err)
	}
}

func TestSocketGroupIDRejectsMissingPath(t *testing.T) {
	if _, err := SocketGroupID(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("SocketGroupID() accepted a missing socket path")
	}
}
