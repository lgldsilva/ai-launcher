package installer

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
)

// bubblewrapPackage is the distro package that provides bwrap. The name is
// the same on the families this table covers. noConfirmFlag is the pacman
// family switch that skips the install prompt.
const (
	bubblewrapPackage = "bubblewrap"
	noConfirmFlag     = "--noconfirm"
)

// packageManager is one way to install bubblewrap. Earlier rows win. A row
// is skipped when any command in BlockedBy is already on PATH, so an AUR
// helper does not run beside pacman and yum does not run beside dnf.
// Script, when set, is the body of `sh -c`: apt-get has no single flag that
// both refreshes the index and installs.
type packageManager struct {
	Command   string
	Args      []string
	Script    string
	Env       []string
	Sudo      bool
	BlockedBy []string
}

// bubblewrapManagers is the detection order. The binary of ai-jail itself
// stays on the GitHub release path; this list only installs bwrap.
func bubblewrapManagers() []packageManager {
	natives := []string{
		"apt-get", "dnf", "yum", "microdnf", "pacman", "zypper", "apk",
		"xbps-install", "eopkg", "urpmi", "emerge", "slackpkg", "opkg", "nix", "guix",
	}
	return []packageManager{
		{Command: "apt-get", Script: "apt-get update && apt-get install -y " + bubblewrapPackage, Env: []string{"DEBIAN_FRONTEND=noninteractive"}, Sudo: true},
		{Command: "dnf", Args: []string{"install", "-y", bubblewrapPackage}, Sudo: true},
		{Command: "yum", Args: []string{"install", "-y", bubblewrapPackage}, Sudo: true, BlockedBy: []string{"dnf"}},
		{Command: "microdnf", Args: []string{"install", "-y", bubblewrapPackage}, Sudo: true, BlockedBy: []string{"dnf", "yum"}},
		{Command: "pacman", Args: []string{"-S", noConfirmFlag, bubblewrapPackage}, Sudo: true},
		{Command: "zypper", Args: []string{"--non-interactive", "install", bubblewrapPackage}, Sudo: true},
		{Command: "apk", Args: []string{"add", "--no-interactive", bubblewrapPackage}, Sudo: true},
		{Command: "xbps-install", Args: []string{"-S", "-y", bubblewrapPackage}, Sudo: true},
		{Command: "eopkg", Args: []string{"install", "-y", bubblewrapPackage}, Sudo: true},
		{Command: "urpmi", Args: []string{"--auto", bubblewrapPackage}, Sudo: true},
		{Command: "emerge", Args: []string{"--ask=n", "sys-apps/bubblewrap"}, Sudo: true},
		{Command: "slackpkg", Args: []string{"install", bubblewrapPackage}, Sudo: true},
		{Command: "opkg", Args: []string{"install", bubblewrapPackage}, Sudo: true},
		{Command: "nix", Args: []string{"--extra-experimental-features", "nix-command flakes", "profile", "install", "nixpkgs#bubblewrap"}},
		{Command: "guix", Args: []string{"install", bubblewrapPackage}},
		{Command: "pamac", Args: []string{"install", "--no-confirm", bubblewrapPackage}, Sudo: true, BlockedBy: []string{"pacman"}},
		{Command: "yay", Args: []string{"-S", noConfirmFlag, bubblewrapPackage}, BlockedBy: []string{"pacman", "pamac"}},
		{Command: "paru", Args: []string{"-S", noConfirmFlag, bubblewrapPackage}, BlockedBy: []string{"pacman", "pamac", "yay"}},
		{Command: "brew", Args: []string{"install", bubblewrapPackage}, BlockedBy: natives},
	}
}

// BubblewrapPlan is the install command for a missing bwrap, or a zero plan
// when the host does not need one. Argv is what the launcher executes.
// Text is what a person can copy: it has no sudo -n, because -n refuses to
// ask for a password.
type BubblewrapPlan struct {
	Needed bool
	Argv   []string
	Text   string
}

// ResolveBubblewrap decides whether this host still needs the bubblewrap
// package. goos other than linux, a bwrap on PATH, or a set BWRAP_BIN all
// produce an empty plan. bwrapBin is the value of BWRAP_BIN, not a lookup.
// root is true when the process is uid 0: sudo is not used, because a
// container image often has no sudo binary. sudo is also skipped when it is
// not on PATH.
func ResolveBubblewrap(goos, bwrapBin string, root bool, lookPath func(string) (string, error)) BubblewrapPlan {
	if goos != "linux" || strings.TrimSpace(bwrapBin) != "" || commandFound(lookPath, "bwrap") {
		return BubblewrapPlan{}
	}
	manager, ok := selectBubblewrapManager(lookPath)
	if !ok {
		return BubblewrapPlan{Needed: true, Text: "install the bubblewrap package and ensure bwrap is on PATH"}
	}
	sudo := manager.Sudo && !root && commandFound(lookPath, "sudo")
	return BubblewrapPlan{
		Needed: true,
		Argv:   manager.execArgv(sudo),
		Text:   manager.display(sudo),
	}
}

func selectBubblewrapManager(lookPath func(string) (string, error)) (packageManager, bool) {
	for _, manager := range bubblewrapManagers() {
		if blocked(lookPath, manager.BlockedBy) || !commandFound(lookPath, manager.Command) {
			continue
		}
		return manager, true
	}
	return packageManager{}, false
}

func (m packageManager) body() []string {
	var argv []string
	if m.Script != "" {
		argv = []string{"sh", "-c", m.Script}
	} else {
		argv = append([]string{m.Command}, m.Args...)
	}
	if len(m.Env) > 0 {
		argv = append(append([]string{"env"}, m.Env...), argv...)
	}
	return argv
}

// execArgv is the launcher's command. -n stops sudo from waiting on a
// password that this process cannot type.
func (m packageManager) execArgv(sudo bool) []string {
	if sudo {
		return append([]string{"sudo", "-n"}, m.body()...)
	}
	return m.body()
}

// pasteArgv is the command printed for a person. sudo may ask for a password.
func (m packageManager) pasteArgv(sudo bool) []string {
	if sudo {
		return append([]string{"sudo"}, m.body()...)
	}
	return m.body()
}

// display is the copy-paste form. Arguments with spaces or shell
// metacharacters are quoted, so `nix-command flakes` stays one argument
// and `&&` stays inside `sh -c`.
func (m packageManager) display(sudo bool) string {
	argv := m.pasteArgv(sudo)
	parts := make([]string, len(argv))
	for i, arg := range argv {
		parts[i] = shellQuoteIfNeeded(arg)
	}
	return strings.Join(parts, " ")
}

func shellQuoteIfNeeded(s string) string {
	if s == "" || strings.ContainsAny(s, " \t'\"$&;|<>()*?[]{}\\") {
		return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
	}
	return s
}

func commandFound(lookPath func(string) (string, error), name string) bool {
	if lookPath == nil {
		return false
	}
	_, err := lookPath(name)
	return err == nil
}

func blocked(lookPath func(string) (string, error), names []string) bool {
	for _, name := range names {
		if commandFound(lookPath, name) {
			return true
		}
	}
	return false
}

// EnsureBubblewrap installs the distro bubblewrap package when systemDeps is
// set and bwrap is missing. A missing manager or a failed sudo is reported
// and does not stop the ai-jail binary install that follows. Without
// systemDeps the command is printed and not run.
func (i *Installer) EnsureBubblewrap(ctx context.Context, systemDeps bool, out, errOut io.Writer) {
	if i == nil {
		return
	}
	lookPath := i.LookPath
	if lookPath == nil {
		lookPath = func(string) (string, error) { return "", fmt.Errorf("lookpath unset") }
	}
	plan := ResolveBubblewrap(i.GOOS, os.Getenv("BWRAP_BIN"), os.Geteuid() == 0, lookPath)
	if !plan.Needed {
		return
	}
	if plan.Argv == nil {
		_, _ = fmt.Fprintf(errOut, "bubblewrap: %s\n", plan.Text)
		return
	}
	if !systemDeps {
		_, _ = fmt.Fprintf(out, "bubblewrap: bwrap is not installed. Re-run with --install-system-deps, or run: %s\n", plan.Text)
		return
	}
	if i.Run == nil {
		_, _ = fmt.Fprintf(errOut, "bubblewrap: cannot run %s\n", plan.Text)
		return
	}
	if _, err := i.Run(ctx, plan.Argv[0], plan.Argv[1:]...); err != nil {
		_, _ = fmt.Fprintf(errOut, "bubblewrap: %s failed: %v\n", plan.Text, err)
	}
}
