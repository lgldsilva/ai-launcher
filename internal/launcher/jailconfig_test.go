package launcher

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/lgldsilva/ai-launcher/internal/config"
)

// fixedDir returns a getwd/home stub answering dir.
func fixedDir(dir string) func() (string, error) {
	return func() (string, error) { return dir, nil }
}

func failingDir() (string, error) { return "", errors.New("no directory") }

// writeRegular creates a regular 0600 file at path, with its parents.
func writeRegular(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("rw_maps = []\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

// symlink creates link -> target, with the link's parents.
func symlink(t *testing.T, target, link string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(link), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
}

func issueCodes(issues []Issue) []string {
	codes := make([]string, 0, len(issues))
	for _, issue := range issues {
		codes = append(codes, issue.Code)
	}
	return codes
}

func requireSingleFatal(t *testing.T, issues []Issue, code string) {
	t.Helper()
	if len(issues) != 1 || issues[0].Code != code {
		t.Fatalf("issues = %v, want exactly [%s]", issueCodes(issues), code)
	}
	if issues[0].Warning {
		t.Fatalf("%s must be fatal: ai-jail refuses the launch", code)
	}
}

// The layout that broke: a dotfile-managed ~/.ai-jail (symlink into
// ~/.config) and a launch from $HOME itself.
func TestJailConfigSymlinkHomeAsProject(t *testing.T) {
	home := t.TempDir()
	writeRegular(t, filepath.Join(home, ".config", "ai-jail-config", "ai-jail.toml"))
	symlink(t, filepath.Join(home, ".config", "ai-jail-config", "ai-jail.toml"), filepath.Join(home, ".ai-jail"))

	issues := jailConfigSymlinkIssues(LaunchConfig{UseJail: true}, fixedDir(home), fixedDir(home))
	requireSingleFatal(t, issues, "jail-home-as-project")
}

// macOS temp dirs live under /var -> /private/var: the same home reached
// through a symlinked parent is still the same file.
func TestJailConfigSymlinkHomeAsProjectThroughSymlinkedParent(t *testing.T) {
	root := t.TempDir()
	realPath := filepath.Join(root, "real")
	writeRegular(t, filepath.Join(root, "outside.toml"))
	symlink(t, filepath.Join(root, "outside.toml"), filepath.Join(realPath, "home", ".ai-jail"))
	symlink(t, realPath, filepath.Join(root, "alias"))

	issues := jailConfigSymlinkIssues(LaunchConfig{UseJail: true},
		fixedDir(filepath.Join(root, "alias", "home")), fixedDir(filepath.Join(realPath, "home")))
	requireSingleFatal(t, issues, "jail-home-as-project")
}

// ai-jail refuses a symlinked project .ai-jail anywhere, not only in $HOME.
func TestJailConfigSymlinkProjectConfig(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	project := filepath.Join(home, "src", "app")
	writeRegular(t, filepath.Join(root, "shared.toml"))
	symlink(t, filepath.Join(root, "shared.toml"), filepath.Join(project, ".ai-jail"))

	issues := jailConfigSymlinkIssues(LaunchConfig{UseJail: true}, fixedDir(project), fixedDir(home))
	requireSingleFatal(t, issues, "jail-project-config-symlink")
}

// A stow-style global symlink is fine until the launch directory contains its
// target: the sandbox could then rewrite the policy that constrains it.
func TestJailConfigSymlinkGlobalTargetInsideProject(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	dotfiles := filepath.Join(home, "dotfiles")
	writeRegular(t, filepath.Join(dotfiles, "ai-jail", "ai-jail.toml"))
	symlink(t, filepath.Join(dotfiles, "ai-jail", "ai-jail.toml"), filepath.Join(home, ".ai-jail"))

	for name, cwd := range map[string]string{
		"target directory":    filepath.Join(dotfiles, "ai-jail"),
		"dotfiles repository": dotfiles,
		"parent of home":      root,
	} {
		t.Run(name, func(t *testing.T) {
			issues := jailConfigSymlinkIssues(LaunchConfig{UseJail: true}, fixedDir(cwd), fixedDir(home))
			requireSingleFatal(t, issues, "jail-global-config-inside-project")
		})
	}
}

func TestJailConfigSymlinkAllowedLayouts(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	dotfiles := filepath.Join(home, "dotfiles")
	project := filepath.Join(home, "src", "app")
	sibling := filepath.Join(home, "dotfiles-other")
	writeRegular(t, filepath.Join(dotfiles, "ai-jail.toml"))
	writeRegular(t, filepath.Join(project, ".ai-jail"))
	if err := os.MkdirAll(sibling, 0o700); err != nil {
		t.Fatal(err)
	}
	symlink(t, filepath.Join(dotfiles, "ai-jail.toml"), filepath.Join(home, ".ai-jail"))

	for name, cwd := range map[string]string{
		"project with a regular .ai-jail": project,
		"directory with no .ai-jail":      filepath.Join(home, "src"),
		"sibling sharing a name prefix":   sibling,
	} {
		t.Run(name, func(t *testing.T) {
			if issues := jailConfigSymlinkIssues(LaunchConfig{UseJail: true}, fixedDir(cwd), fixedDir(home)); len(issues) != 0 {
				t.Fatalf("issues = %v, want none", issueCodes(issues))
			}
		})
	}
}

// ai-jail accepts a regular ~/.ai-jail when launched from $HOME.
func TestJailConfigSymlinkRegularGlobalInHome(t *testing.T) {
	home := t.TempDir()
	writeRegular(t, filepath.Join(home, ".ai-jail"))
	if issues := jailConfigSymlinkIssues(LaunchConfig{UseJail: true}, fixedDir(home), fixedDir(home)); len(issues) != 0 {
		t.Fatalf("issues = %v, want none", issueCodes(issues))
	}
}

// A dangling global symlink is left for ai-jail to report.
func TestJailConfigSymlinkDanglingGlobal(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	symlink(t, filepath.Join(home, "missing.toml"), filepath.Join(home, ".ai-jail"))
	if issues := jailConfigSymlinkIssues(LaunchConfig{UseJail: true}, fixedDir(root), fixedDir(home)); len(issues) != 0 {
		t.Fatalf("issues = %v, want none", issueCodes(issues))
	}
}

func TestJailConfigSymlinkSkipped(t *testing.T) {
	home := t.TempDir()
	writeRegular(t, filepath.Join(home, "target.toml"))
	symlink(t, filepath.Join(home, "target.toml"), filepath.Join(home, ".ai-jail"))
	cases := map[string]struct {
		cfg         LaunchConfig
		getwd, home func() (string, error)
	}{
		"jail disabled":  {LaunchConfig{}, fixedDir(home), fixedDir(home)},
		"nil getwd":      {LaunchConfig{UseJail: true}, nil, fixedDir(home)},
		"nil home":       {LaunchConfig{UseJail: true}, fixedDir(home), nil},
		"getwd failure":  {LaunchConfig{UseJail: true}, failingDir, fixedDir(home)},
		"home failure":   {LaunchConfig{UseJail: true}, fixedDir(home), failingDir},
		"empty cwd":      {LaunchConfig{UseJail: true}, fixedDir(" "), fixedDir(home)},
		"empty home dir": {LaunchConfig{UseJail: true}, fixedDir(home), fixedDir("")},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if issues := jailConfigSymlinkIssues(tc.cfg, tc.getwd, tc.home); len(issues) != 0 {
				t.Fatalf("issues = %v, want none", issueCodes(issues))
			}
		})
	}
}

// The check rides the Validator: fatal for the ai-jail backend, absent for the
// container backend, which never runs ai-jail.
func TestValidatorReportsHomeAsJailProject(t *testing.T) {
	home := t.TempDir()
	writeRegular(t, filepath.Join(home, "target.toml"))
	symlink(t, filepath.Join(home, "target.toml"), filepath.Join(home, ".ai-jail"))
	v := Validator{
		LookPath: func(name string) (string, error) { return "/usr/bin/" + name, nil },
		Stat:     os.Stat,
		Getwd:    fixedDir(home),
		Home:     fixedDir(home),
		GOOS:     "darwin",
	}

	jailed := v.Validate(LaunchConfig{UseJail: true, Agent: config.Agent{Command: "claude"}})
	if !containsCode(jailed, "jail-home-as-project") {
		t.Fatalf("jail backend issues = %v, want jail-home-as-project", issueCodes(jailed))
	}
	containerised := v.Validate(LaunchConfig{UseJail: true, UseDocker: true, Agent: config.Agent{Command: "claude"}})
	if containsCode(containerised, "jail-home-as-project") {
		t.Fatalf("container backend issues = %v, must not carry the ai-jail check", issueCodes(containerised))
	}
}

func containsCode(issues []Issue, code string) bool {
	for _, issue := range issues {
		if issue.Code == code {
			return true
		}
	}
	return false
}

func TestPathInside(t *testing.T) {
	for _, tc := range []struct {
		dir, target string
		want        bool
	}{
		{"/a/b", "/a/b", true},
		{"/a/b", "/a/b/c/d", true},
		{"/a/b", "/a/bc", false},
		{"/a/b", "/a", false},
		{"/a/b", "/x/y", false},
		{"/a/b", "/a/b/..hidden", true},
	} {
		if got := pathInside(tc.dir, tc.target); got != tc.want {
			t.Errorf("pathInside(%q, %q) = %v, want %v", tc.dir, tc.target, got, tc.want)
		}
	}
}
