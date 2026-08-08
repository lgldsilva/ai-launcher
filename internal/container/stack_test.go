package container

import (
	"strings"
	"testing"
)

// The zsh stack is the minimal shell option: apt package only, no oh-my-zsh
// framework, no helper layer and no dependency mounts.
func TestZshStack(t *testing.T) {
	stack, ok := StackByID("zsh")
	if !ok {
		t.Fatal("StackByID(zsh) = false; want the catalog entry")
	}
	if stack.Name != "zsh" {
		t.Errorf("zsh stack Name = %q; want %q", stack.Name, "zsh")
	}
	if stack.Helpers != "" {
		t.Errorf("zsh stack must not have helpers, got %q", stack.Helpers)
	}
	if len(stack.DependencyIDs) != 0 {
		t.Errorf("zsh stack must not carry dependency IDs, got %v", stack.DependencyIDs)
	}
	if !strings.Contains(stack.Layer, "    zsh \\") {
		t.Errorf("zsh stack Layer must install the zsh apt package:\n%s", stack.Layer)
	}
	if strings.Contains(stack.Layer, "oh-my-zsh") {
		t.Errorf("zsh stack must not install oh-my-zsh:\n%s", stack.Layer)
	}
	if _, err := ValidStackIDs([]string{"zsh"}); err != nil {
		t.Errorf("ValidStackIDs(zsh) error = %v", err)
	}
}

// Package-manager helper layers must not leave their download/build caches in
// the image: the cache is repopulated from the mounted host caches at run
// time, and keeping it in the layer adds hundreds of megabytes per stack.
func TestStackHelpersCleanTheirCaches(t *testing.T) {
	want := map[string]string{
		"go":     "go clean -modcache -cache",
		"node":   "npm cache clean --force",
		"python": "pip3 cache purge",
		"rust":   ".cargo/registry",
	}
	for id, marker := range want {
		stack, ok := StackByID(id)
		if !ok {
			t.Fatalf("StackByID(%q) = false", id)
		}
		if stack.Helpers == "" {
			t.Errorf("stack %q unexpectedly has no helpers", id)
			continue
		}
		if !strings.Contains(stack.Helpers, marker) {
			t.Errorf("stack %q helpers must clean their cache (%q):\n%s", id, marker, stack.Helpers)
		}
	}
}

// The docker CLI is not part of the shared dev profile: it is a separate
// conditional layer (DockerCLIProfile) emitted only for docker-permission
// launches.
func TestDevProfileHasNoDockerCLI(t *testing.T) {
	if strings.Contains(DevProfile, "docker.io") {
		t.Errorf("DevProfile must not install docker.io:\n%s", DevProfile)
	}
	if !strings.Contains(DockerCLIProfile, "docker.io") {
		t.Errorf("DockerCLIProfile must install docker.io:\n%s", DockerCLIProfile)
	}
}

// The Node toolchain layer hands the npm global prefix to the least-privilege
// user inside the same RUN (the directory is new and almost empty, so the
// chown costs no extra layer) and keeps /opt/nvm root-owned but traversable.
func TestNodeProfileOwnership(t *testing.T) {
	if !strings.Contains(NodeProfile, "chown -R ai-launcher:ai-launcher /usr/local/lib/nvm-bin") {
		t.Errorf("NodeProfile must chown the npm global prefix to the runtime user:\n%s", NodeProfile)
	}
	if strings.Contains(NodeProfile, "chown -R ai-launcher:ai-launcher /opt/nvm") {
		t.Errorf("/opt/nvm must stay root-owned; only the npm prefix is handed over:\n%s", NodeProfile)
	}
	// The prefix config must not leave a root-owned .npmrc in the shared home.
	if !strings.Contains(NodeProfile, "HOME=/root npm config set prefix") {
		t.Errorf("NodeProfile must write the npm prefix config outside the shared home:\n%s", NodeProfile)
	}
}

// Apt-based stack layers must drop the apt lists in the same RUN so the index
// does not ride along in every toolchain layer.
func TestAptStackLayersCleanAptLists(t *testing.T) {
	for _, stack := range Stacks {
		if !strings.HasPrefix(stack.Layer, "RUN apt-get update") {
			continue
		}
		if !strings.HasSuffix(stack.Layer, aptCleanupSuffix) {
			t.Errorf("stack %q apt layer must end with the apt cleanup suffix:\n%s", stack.ID, stack.Layer)
		}
	}
}
