package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// stubMemoryRecordingArgv puts an ai-memory on PATH that writes its argv and a
// chosen subset of its environment to a file, then exits. It returns the path
// of that file.
func stubMemoryRecordingArgv(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	record := filepath.Join(dir, "argv")
	script := "#!/bin/sh\n" +
		": > " + record + "\n" +
		"for arg in \"$@\"; do printf 'ARG %s\\n' \"$arg\" >> " + record + "; done\n" +
		"printf 'ENV_URL %s\\n' \"$AI_MEMORY_SERVER_URL\" >> " + record + "\n" +
		"printf 'ENV_TOKEN %s\\n' \"$AI_MEMORY_AUTH_TOKEN\" >> " + record + "\n" +
		"echo searched\n"
	bin := filepath.Join(dir, "ai-memory")
	if err := os.WriteFile(bin, []byte(script), 0o700); err != nil { // #nosec G306 -- test stub must be executable
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return record
}

func writeSearchGlobal(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	globalPath := filepath.Join(dir, "global.yaml")
	body := "memory_server_url: http://memory.example.internal:49374\n" +
		"memory_auth_token: s3cr3t\n"
	if err := os.WriteFile(globalPath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return globalPath
}

// The launcher creates workstreams (--new) and resumes them (--workstream), so
// it owned the write side of the ledger and gave no way to read it back. The
// delta ai-memory injects into the next harness is size-limited by design; an
// old decision lives in the searchable ledger, and reaching it should not need
// a second tool.
func TestWorkstreamSearchForwardsTheQueryToAiMemory(t *testing.T) {
	record := stubMemoryRecordingArgv(t)
	globalPath := writeSearchGlobal(t)

	stdout, _, err := runCapture(t, "--config", globalPath, "--workstream-id", "ws-1",
		"--workstream-search", "why did we drop redis")
	if err != nil {
		t.Fatalf("run() error = %v", err)
	}
	if !strings.Contains(stdout, "searched") {
		t.Errorf("stdout = %q; ai-memory's own output must reach the terminal", stdout)
	}

	got := readRecord(t, record)
	wantArgs := []string{"ARG workstream-search", "ARG why did we drop redis"}
	for _, want := range wantArgs {
		if !strings.Contains(got, want) {
			t.Errorf("argv = %q; missing %q", got, want)
		}
	}
	// The query is one argument, not shell-split into words.
	if strings.Contains(got, "ARG why\n") {
		t.Errorf("argv = %q; the query must stay a single argument", got)
	}
}

// The server URL and bearer token come from the trusted global config, exactly
// as they do for a launch. A search that silently queried the default loopback
// server would answer from the wrong ledger.
func TestWorkstreamSearchCarriesTheConfiguredEndpoint(t *testing.T) {
	record := stubMemoryRecordingArgv(t)
	globalPath := writeSearchGlobal(t)

	if _, _, err := runCapture(t, "--config", globalPath, "--workstream-id", "ws-1",
		"--workstream-search", "redis"); err != nil {
		t.Fatalf("run() error = %v", err)
	}
	got := readRecord(t, record)
	if !strings.Contains(got, "ENV_URL http://memory.example.internal:49374") {
		t.Errorf("env = %q; the configured server URL must be forwarded", got)
	}
	if !strings.Contains(got, "ENV_TOKEN s3cr3t") {
		t.Errorf("env = %q; the configured auth token must be forwarded", got)
	}
}

// --limit and --json are ai-memory's own flags; unset means "ai-memory decides",
// which is not the same as sending a zero.
func TestWorkstreamSearchForwardsLimitAndJSONOnlyWhenAsked(t *testing.T) {
	t.Run("unset sends neither", func(t *testing.T) {
		record := stubMemoryRecordingArgv(t)
		globalPath := writeSearchGlobal(t)
		if _, _, err := runCapture(t, "--config", globalPath, "--workstream-id", "ws-1",
			"--workstream-search", "redis"); err != nil {
			t.Fatalf("run() error = %v", err)
		}
		got := readRecord(t, record)
		if strings.Contains(got, "ARG --limit") || strings.Contains(got, "ARG --json") {
			t.Errorf("argv = %q; unset flags must not be invented", got)
		}
	})

	t.Run("set sends both", func(t *testing.T) {
		record := stubMemoryRecordingArgv(t)
		globalPath := writeSearchGlobal(t)
		if _, _, err := runCapture(t, "--config", globalPath, "--workstream-id", "ws-1",
			"--workstream-search", "redis", "--limit", "50", "--json"); err != nil {
			t.Fatalf("run() error = %v", err)
		}
		got := readRecord(t, record)
		for _, want := range []string{"ARG --limit", "ARG 50", "ARG --json"} {
			if !strings.Contains(got, want) {
				t.Errorf("argv = %q; missing %q", got, want)
			}
		}
	})
}

// A read-only query against an HTTP server is not a launch: no harness runs,
// nothing touches the checkout, and wrapping it in ai-jail would only stop the
// operator's terminal from seeing the answer.
func TestWorkstreamSearchIsNotJailWrapped(t *testing.T) {
	record := stubMemoryRecordingArgv(t)
	globalPath := writeSearchGlobal(t)

	if _, _, err := runCapture(t, "--config", globalPath, "--workstream-id", "ws-1",
		"--workstream-search", "redis"); err != nil {
		t.Fatalf("run() error = %v", err)
	}
	if got := readRecord(t, record); strings.Contains(got, "ai-jail") {
		t.Errorf("argv = %q; a search is not a sandboxed launch", got)
	}
}

// Missing upstream is an actionable message, not an exec error.
func TestWorkstreamSearchReportsAMissingAiMemory(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	globalPath := writeSearchGlobal(t)

	_, _, err := runCapture(t, "--config", globalPath, "--workstream-id", "ws-1",
		"--workstream-search", "redis")
	if err == nil {
		t.Fatal("run() = nil; a missing ai-memory must be reported")
	}
	if !strings.Contains(err.Error(), "ai-memory") || !strings.Contains(err.Error(), "--install") {
		t.Errorf("error = %v; want the tool name and the way to get it", err)
	}
}

// The contract with upstream, not just with our own stub: ai-memory's
// workstream-search declares --workstream-id as a required argument, so a
// command built without it is refused by the argument parser before the query
// is ever run. The launcher shipped exactly that command, and every test here
// passed because the stub accepts any argv — they asserted what the launcher
// emits, never that upstream would take it.
func TestWorkstreamSearchAlwaysCarriesTheWorkstreamID(t *testing.T) {
	record := stubMemoryRecordingArgv(t)
	globalPath := writeSearchGlobal(t)

	if _, _, err := runCapture(t, "--config", globalPath, "--workstream-id", "ws-42",
		"--workstream-search", "redis"); err != nil {
		t.Fatalf("run() error = %v", err)
	}
	got := readRecord(t, record)
	for _, want := range []string{"ARG --workstream-id", "ARG ws-42"} {
		if !strings.Contains(got, want) {
			t.Errorf("argv = %q; missing %q — upstream requires it", got, want)
		}
	}
}

// ai-memory exports AI_MEMORY_WORKSTREAM_ID into the agents it manages, and the
// launcher does not strip it, so an agent started from here can search its own
// ledger with no extra argument. The flag stays the way in from a bare shell.
func TestWorkstreamSearchResolvesTheIDFromTheEnvironment(t *testing.T) {
	t.Run("inherited when the flag is absent", func(t *testing.T) {
		record := stubMemoryRecordingArgv(t)
		globalPath := writeSearchGlobal(t)
		t.Setenv("AI_MEMORY_WORKSTREAM_ID", "ws-from-env")

		if _, _, err := runCapture(t, "--config", globalPath, "--workstream-search", "redis"); err != nil {
			t.Fatalf("run() error = %v", err)
		}
		if got := readRecord(t, record); !strings.Contains(got, "ARG ws-from-env") {
			t.Errorf("argv = %q; the inherited workstream must be used", got)
		}
	})

	t.Run("the flag wins over the environment", func(t *testing.T) {
		record := stubMemoryRecordingArgv(t)
		globalPath := writeSearchGlobal(t)
		t.Setenv("AI_MEMORY_WORKSTREAM_ID", "ws-from-env")

		if _, _, err := runCapture(t, "--config", globalPath, "--workstream-id", "ws-explicit",
			"--workstream-search", "redis"); err != nil {
			t.Fatalf("run() error = %v", err)
		}
		got := readRecord(t, record)
		if !strings.Contains(got, "ARG ws-explicit") || strings.Contains(got, "ARG ws-from-env") {
			t.Errorf("argv = %q; the explicit id must win", got)
		}
	})
}

// With neither source the launcher answers for itself instead of letting
// upstream's argument parser print a usage block about a flag ai-launcher never
// documented.
func TestWorkstreamSearchWithoutAnIDIsALauncherError(t *testing.T) {
	stubMemoryRecordingArgv(t)
	globalPath := writeSearchGlobal(t)
	t.Setenv("AI_MEMORY_WORKSTREAM_ID", "")

	_, _, err := runCapture(t, "--config", globalPath, "--workstream-search", "redis")
	if err == nil {
		t.Fatal("run() = nil; a search with no workstream must be refused")
	}
	for _, want := range []string{"--workstream-id", "AI_MEMORY_WORKSTREAM_ID"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %v; want it to name %q", err, want)
		}
	}
}

// --workstream carries the name `ai-memory run --workstream` selects by;
// workstream-search wants an id, and upstream maps neither to the other.
// Treating the name as an id would silently search the wrong ledger.
func TestWorkstreamSearchDoesNotReuseTheWorkstreamName(t *testing.T) {
	stubMemoryRecordingArgv(t)
	globalPath := writeSearchGlobal(t)
	t.Setenv("AI_MEMORY_WORKSTREAM_ID", "")

	_, _, err := runCapture(t, "--config", globalPath, "--workstream", "refactor-auth",
		"--workstream-search", "redis")
	if err == nil {
		t.Fatal("run() = nil; a workstream name is not a workstream id")
	}
	if !strings.Contains(err.Error(), "--workstream-id") {
		t.Errorf("error = %v; want the id flag named", err)
	}
}

func readRecord(t *testing.T, path string) string {
	t.Helper()
	body, err := os.ReadFile(path) // #nosec G304 -- path is a t.TempDir file this test wrote
	if err != nil {
		t.Fatalf("read stub record: %v", err)
	}
	return string(body)
}
