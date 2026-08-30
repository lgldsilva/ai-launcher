package sourcehealth

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The fixtures below are transcribed from the real vendor installers and the
// real managed wrapper that produced the incident this package prevents; the
// comments name the file each excerpt came from.

func TestClassifyScriptDetectsVendorInstallers(t *testing.T) {
	installers := map[string]string{
		// antigravity (agy) bootstrapper: self-detects its own target path.
		"antigravity_notice": "#!/bin/bash\nTARGET_DIR=\"$HOME/.local/bin\"\nBINARY_PATH=\"$TARGET_DIR/agy\"\nif [ -f \"$BINARY_PATH\" ]; then\n  echo \"Notice: 'agy' is already installed at $BINARY_PATH.\"\n  echo \"  rm \\\"$BINARY_PATH\\\"\"\n  exit 0\nfi\n",
		// claude.ai/install.sh
		"claude_dirs": "#!/bin/bash\nset -e\nDOWNLOAD_BASE_URL=\"https://downloads.claude.ai\"\nINSTALL_DIR=\"$HOME/.local/bin\"\n",
		// cursor.com/install
		"cursor_download": "#!/usr/bin/env bash\nDOWNLOAD_URL=\"https://downloads.cursor.com/lab/$VERSION/${OS}/${ARCH}/agent-cli-package.tar.gz\"\n",
		// code.kimi.com legacy installer (deprecated) — no *_DIR= assignment at all.
		"kimi_by_filename": "#!/usr/bin/env bash\n# Legacy kimi-cli (Python) installer - DEPRECATED.\n# Usage: curl -fsSL https://code.kimi.com/install.sh | bash\n",
		// oh-my-pi install.sh
		"omp_install_dir": "#!/bin/sh\nset -e\n# OMP Coding Agent Installer\nINSTALL_DIR=\"${PI_INSTALL_DIR:-$HOME/.local/bin}\"\n",
		// release-fetch language only (a downloader that unpacks an archive).
		"release_language": "#!/bin/sh\necho \"Extracting binary from archive...\"\n",
		// Notice-only deadlock: no *_DIR= assignment, no install.sh, no release
		// language — only the self-detect branch reveals what the script is. The
		// real corpus never reaches that branch (every vendor there also assigns a
		// directory), so it needs its own case to stay proven rather than assumed.
		"notice_only_deadlock": "#!/bin/sh\nTOOL=\"$HOME/bin/tool\"\nif [ -f \"$TOOL\" ]; then\n  echo \"'tool' is already installed at $TOOL\"\n  echo \"delete the binary first: rm \\\"$TOOL\\\"\"\n  exit 0\nfi\n",
	}
	for name, script := range installers {
		if got := ClassifyScript([]byte(script)); got != KindInstaller {
			t.Errorf("%s: ClassifyScript = %v, want KindInstaller", name, got)
		}
		if !LooksLikeInstaller([]byte(script)) {
			t.Errorf("%s: LooksLikeInstaller = false, want true", name)
		}
	}
}

func TestClassifyScriptKeepsManagedWrappersAsLaunchers(t *testing.T) {
	// ai-memory's bin/ai-memory wrapper: it DOES fetch with curl -fsSL and it
	// DOES mention installing — classifying it as an installer would replace a
	// working self-managing command with a one-shot run, so these markers must
	// not fire on it. This is the regression guard for that.
	wrapper := "#!/usr/bin/env bash\n" +
		"# ai-memory wrapper\n" +
		"#   ai-memory run ...       Use a cached native client so it can exec host agents.\n" +
		"command -v curl >/dev/null 2>&1 || { echo \"  curl not found; skipping wrapper self-upgrade\" >&2; return 0; }\n" +
		"if ! curl -fsSL \"${WRAPPER_URL}\" -o \"${tmp}\" 2>/dev/null; then return 0; fi\n" +
		"# Sanity: must look like our bash script. Refuse to install\n" +
		"AI_MEMORY_SKIP_SELF_UPGRADE=1 exec \"${script_path}\" upgrade\n"
	if got := ClassifyScript([]byte(wrapper)); got != KindLauncher {
		t.Fatalf("managed wrapper: ClassifyScript = %v, want KindLauncher", got)
	}
	if LooksLikeInstaller([]byte(wrapper)) {
		t.Fatal("managed wrapper must not look like an installer")
	}
	// A plain launcher with no fetch language at all.
	if got := ClassifyScript([]byte("#!/bin/sh\nexec my-tool \"$@\"\n")); got != KindLauncher {
		t.Errorf("plain launcher = %v, want KindLauncher", got)
	}
	// Empty input must not be mistaken for an installer.
	if got := ClassifyScript(nil); got != KindLauncher {
		t.Errorf("empty script = %v, want KindLauncher", got)
	}
}

func TestInterpreterFollowsTheShebang(t *testing.T) {
	cases := map[string]string{
		"#!/bin/bash\n":         "bash",
		"#!/usr/bin/env bash\n": "bash",
		"#!/bin/sh\n":           "sh",
		"#!/usr/bin/env sh\n":   "sh",
		"#!/usr/bin/zsh\n":      "zsh",
		"":                      "sh",
		"# no shebang at all\n": "sh",
		strings.Repeat("x", 300) + "#!/bin/bash\n": "sh", // shebang must be at the head
	}
	for script, want := range cases {
		if got := Interpreter([]byte(script)); got != want {
			t.Errorf("Interpreter(%q…) = %q, want %q", firstRunes(script, 24), got, want)
		}
	}
}

func TestClassifyProbeAcceptsRealVersionOutputs(t *testing.T) {
	healthy := map[string]string{
		"bare":            "1.1.22\n",
		"cli-prefixed":    "grok 1.0.5 (5115b46bc909)\n",
		"word-version":    "kimi, version 1.41.0\n",
		"slash-form":      "omp/18.0.11\n",
		"codex-cli":       "codex-cli 0.151.0\n",
		"parenthesised":   "2.1.251 (Claude Code)\n",
		"after-a-warning": "WARNING: failed to clean up stale dirs\nsemidx version 0.51.1\n",
	}
	for name, out := range healthy {
		probe := ClassifyProbe(out, true)
		if probe.State != ProbeHealthy {
			t.Errorf("%s: State = %v, want healthy", name, probe.State)
		}
		if probe.Version == "" {
			t.Errorf("%s: Version empty, want a version token", name)
		}
	}
	// A launcher that prints a version but exits non-zero behind a warning is
	// still evidence the command runs.
	if probe := ClassifyProbe("grok 1.0.5\n", false); probe.State != ProbeHealthy {
		t.Errorf("version with non-zero exit: State = %v, want healthy", probe.State)
	}
}

// TestClassifyProbeDetectsInstallerInPath is the behavioural signature of the
// incident: each string here is what one of the corrupted commands actually
// printed when asked for its version.
func TestClassifyProbeDetectsInstallerInPath(t *testing.T) {
	cases := map[string]struct {
		output string
		exitOK bool
	}{
		"agy_notice":        {"Notice: 'agy' is already installed at /Users/u/.local/bin/agy.\n", true},
		"agy_usage":         {"[ERROR] Unknown parameter: --version\nUsage: install.sh [options]\n", false},
		"claude_usage":      {"Usage: /Users/u/.local/bin/claude [stable|latest|VERSION]\n", false},
		"grok_version_form": {"Invalid version format: --version (expected X.Y.Z)\n", false},
		"opencode_arg":      {"Error: --version requires a version argument\n", false},
		"cursor_header":     {"Cursor Agent Installer\n", true},
		// Generic path: a script that answers --version by advertising its own
		// usage, with no marker string and no version anywhere. A real CLI would
		// answer a supported flag with a version; usage-instead-of-version is the
		// signal that the file is a script standing in for a command.
		"plain-cli-usage": {"Usage: mycmd [OPTIONS] [COMMAND]\n", false},
	}
	for name, tc := range cases {
		probe := ClassifyProbe(tc.output, tc.exitOK)
		if probe.State != ProbeInstallerLike {
			t.Errorf("%s: State = %v, want installer-in-path", name, probe.State)
		}
		if probe.Version != "" {
			t.Errorf("%s: Version = %q, want empty", name, probe.Version)
		}
	}
}

// A managed wrapper reporting its own and the latest version IS answering as a
// command, even when it exits non-zero to signal "upgrade available". That must
// count as healthy: the file at the path is the command, not an installer.
func TestClassifyProbeTreatsVersionCheckLogAsHealthy(t *testing.T) {
	probe := ClassifyProbe("Current version: 1.2.3\nLatest version is 2.1.177\n", false)
	if probe.State != ProbeHealthy {
		t.Fatalf("State = %v, want healthy", probe.State)
	}
	if probe.Version == "" {
		t.Fatal("Version empty, want a token")
	}
}

func TestClassifyProbeSilentAndFailed(t *testing.T) {
	if probe := ClassifyProbe("", true); probe.State != ProbeSilent {
		t.Errorf("silent exit0: State = %v, want unresponsive", probe.State)
	}
	if probe := ClassifyProbe("", false); probe.State != ProbeFailed {
		t.Errorf("silent exit1: State = %v, want failed", probe.State)
	}
	if probe := ClassifyProbe("permission denied\n", false); probe.State != ProbeFailed {
		t.Errorf("error exit1: State = %v, want failed", probe.State)
	}
}

func TestProbeStateString(t *testing.T) {
	for state, want := range map[ProbeState]string{
		ProbeHealthy:       "healthy",
		ProbeInstallerLike: "installer-in-path",
		ProbeSilent:        "unresponsive",
		ProbeFailed:        "failed",
	} {
		if got := state.String(); got != want {
			t.Errorf("%d.String() = %q, want %q", state, got, want)
		}
	}
}

func firstRunes(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n])
}

// TestClassifyScriptOnRealVendorCorpus runs the classifier over the actual
// bootstrappers involved in the incident (kept under testdata/scripts, see the
// README there) plus the managed wrapper that must keep being stored.
//
// Hand-written excerpts hid a real gap while this package was being developed,
// so the corpus is the authority: every one of those scripts must be recognised
// as an installer, and the wrapper must not.
func TestClassifyScriptOnRealVendorCorpus(t *testing.T) {
	const dir = "testdata/scripts"
	installers := []string{
		"agy.install.sh", "claude.install.sh", "cursor-agent.install.sh",
		"devin.install.sh", "grok.install.sh", "kimi.install.sh",
		"omp.install.sh", "opencode.install.sh",
	}
	for _, name := range installers {
		script, err := os.ReadFile(filepath.Join(dir, name)) // #nosec G304 -- fixed file list under testdata
		if err != nil {
			t.Fatalf("read corpus %s: %v", name, err)
		}
		if got := ClassifyScript(script); got != KindInstaller {
			t.Errorf("%s: ClassifyScript = %v, want KindInstaller", name, got)
		}
		if Interpreter(script) == "" {
			t.Errorf("%s: Interpreter returned empty", name)
		}
	}
	wrapper, err := os.ReadFile(filepath.Join(dir, "ai-memory.wrapper.sh")) // #nosec G304 -- fixed testdata path
	if err != nil {
		t.Fatalf("read wrapper: %v", err)
	}
	if got := ClassifyScript(wrapper); got != KindLauncher {
		t.Errorf("ai-memory wrapper: ClassifyScript = %v, want KindLauncher (executing it would discard a working command)", got)
	}
}

// TestProbeCorpusDetectsInstallerAnswers pairs each real installer with the
// text it actually printed when asked for a version.
func TestProbeCorpusDetectsInstallerAnswers(t *testing.T) {
	cases := map[string]string{
		"agy":      "Notice: 'agy' is already installed at /Users/u/.local/bin/agy.\nThe Antigravity CLI automatically self-updates in the background during regular runs.\n",
		"claude":   "Usage: /Users/u/.local/bin/claude [stable|latest|VERSION]\n",
		"grok":     "Invalid version format: --version (expected X.Y.Z or X.Y.Z-suffix)\n",
		"opencode": "Error: --version requires a version argument\n",
		"cursor":   "\n",
	}
	for name, output := range cases {
		probe := ClassifyProbe(output, name == "agy" || name == "cursor")
		if probe.State != ProbeInstallerLike && probe.State != ProbeSilent {
			t.Errorf("%s: verdict %v, want installer-in-path or unresponsive (never healthy)", name, probe.State)
		}
		if probe.State == ProbeHealthy {
			t.Errorf("%s: an installer answer must never count as healthy", name)
		}
	}
}

// wrapperRecipe is a managed wrapper: stored at the target, it IS the command.
const wrapperRecipe = "#!/usr/bin/env bash\nexec managed-native-runner \"$@\"\n"

// installerRecipe carries an install-target assignment, so it is a one-shot
// bootstrapper that must be executed rather than stored.
const installerRecipe = "#!/bin/bash\nINSTALL_DIR=\"$HOME/.local/bin\"\n"

func TestDecideExisting(t *testing.T) {
	producedLauncher := "#!/usr/bin/env bash\n# vendor launcher produced by the bootstrapper\nexec node \"$DIR/index.js\" \"$@\"\n"
	nativeBinary := "\x7fELF\x02\x01\x01 not a script at all"

	cases := []struct {
		name         string
		stored       []byte
		recipe       []byte
		recordedKind string
		recordedSum  string
		want         ExistingVerdict
	}{
		{name: "nothing there", stored: nil, recipe: []byte(wrapperRecipe), want: ExistingNothing},
		{"installer stored as the command is the bug", []byte(installerRecipe), []byte(installerRecipe), "", "", ExistingResidue},
		{"any installer script at the target is residue", []byte("INSTALL_DIR=/x\n"), []byte(wrapperRecipe), "", "", ExistingResidue},
		{"wrapper unchanged", []byte(wrapperRecipe), []byte(wrapperRecipe), "wrapper", Digest([]byte(wrapperRecipe)), ExistingCurrentWrapper},
		{"wrapper replaced upstream", []byte("#!/bin/sh\nexec old-runner\n"), []byte(wrapperRecipe), "wrapper", "stale", ExistingChanged},
		{"vendor launcher produced by this installer", []byte(producedLauncher), []byte(installerRecipe), "installer", Digest([]byte(installerRecipe)), ExistingCurrentProduced},
		{"produced launcher but the recipe moved on", []byte(producedLauncher), []byte(installerRecipe), "installer", "different-digest", ExistingChanged},
		{"native binary produced, no state to prove it", []byte(nativeBinary), []byte(installerRecipe), "", "", ExistingCurrentProduced},
		{"native binary with a wrapper recipe cannot be its product", []byte(nativeBinary), []byte(wrapperRecipe), "", "", ExistingChanged},
		{"installer recipe, produced binary, stale digest recorded", []byte(nativeBinary), []byte(installerRecipe), "installer", "old", ExistingCurrentProduced},
	}
	for _, tc := range cases {
		got := DecideExisting(tc.stored, tc.recipe, tc.recordedKind, tc.recordedSum)
		if got != tc.want {
			t.Errorf("%s: DecideExisting = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestExistingVerdictString(t *testing.T) {
	for verdict, want := range map[ExistingVerdict]string{
		ExistingNothing:         "nothing",
		ExistingCurrentWrapper:  "current-wrapper",
		ExistingCurrentProduced: "current-produced",
		ExistingResidue:         "installer-residue",
		ExistingChanged:         "recipe-changed",
	} {
		if got := verdict.String(); got != want {
			t.Errorf("%d.String() = %q, want %q", verdict, got, want)
		}
	}
}

func TestDigestAndIsScript(t *testing.T) {
	if Digest(nil) == "" {
		t.Error("Digest(nil) empty, want the sha256 of empty input")
	}
	x := []byte("a")
	copied := []byte(string(x))
	if Digest(x) != Digest(copied) {
		t.Error("Digest not deterministic across equal inputs")
	}
	if Digest(x) == Digest([]byte("b")) {
		t.Error("Digest collides across different inputs")
	}
	if len(Digest([]byte("a"))) != 64 {
		t.Errorf("Digest length = %d, want 64 hex chars", len(Digest([]byte("a"))))
	}
	if !IsScript([]byte("#!/bin/sh\n")) {
		t.Error("IsScript false for a shebang file")
	}
	if IsScript([]byte("\x7fELF")) {
		t.Error("IsScript true for a binary")
	}
}
