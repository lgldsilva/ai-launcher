package installer

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
)

// bubblewrapPackage is the distro package that provides bwrap. The name is
// the same on the families this table covers.
const bubblewrapPackage = "bubblewrap"

// packageManager is one way to install bubblewrap. Earlier rows win. A row
// is skipped when any command in BlockedBy is already on PATH, so an AUR
// helper does not run beside pacman and yum does not run beside dnf.
type packageManager struct {
	Command   string
	Args      []string
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
		{Command: "apt-get", Args: []string{"install", "-y", bubblewrapPackage}, Env: []string{"DEBIAN_FRONTEND=noninteractive"}, Sudo: true},
		{Command: "dnf", Args: []string{"install", "-y", bubblewrapPackage}, Sudo: true},
		{Command: "yum", Args: []string{"install", "-y", bubblewrapPackage}, Sudo: true, BlockedBy: []string{"dnf"}},
		{Command: "microdnf", Args: []string{"install", "-y", bubblewrapPackage}, Sudo: true, BlockedBy: []string{"dnf", "yum"}},
		{Command: "pacman", Args: []string{"-S", "--noconfirm", bubblewrapPackage}, Sudo: true},
		{Command: "zypper", Args: []string{"--non-interactive", "install", bubblewrapPackage}, Sudo: true},
		{Command: "apk", Args: []string{"add", "--no-interactive", bubblewrapPackage}, Sudo: true},
		{Command: "xbps-install", Args: []string{"-y", bubblewrapPackage}, Sudo: true},
		{Command: "eopkg", Args: []string{"install", "-y", bubblewrapPackage}, Sudo: true},
		{Command: "urpmi", Args: []string{"--auto", bubblewrapPackage}, Sudo: true},
		{Command: "emerge", Args: []string{"--ask=n", "sys-apps/bubblewrap"}, Sudo: true},
		{Command: "slackpkg", Args: []string{"install", bubblewrapPackage}, Sudo: true},
		{Command: "opkg", Args: []string{"install", bubblewrapPackage}, Sudo: true},
		{Command: "nix", Args: []string{"profile", "install", "nixpkgs#bubblewrap"}},
		{Command: "guix", Args: []string{"install", bubblewrapPackage}},
		{Command: "pamac", Args: []string{"install", "--no-confirm", bubblewrapPackage}, Sudo: true, BlockedBy: []string{"pacman"}},
		{Command: "yay", Args: []string{"-S", "--noconfirm", bubblewrapPackage}, BlockedBy: []string{"pacman", "pamac"}},
		{Command: "paru", Args: []string{"-S", "--noconfirm", bubblewrapPackage}, BlockedBy: []string{"pacman", "pamac", "yay"}},
		{Command: "brew", Args: []string{"install", bubblewrapPackage}, BlockedBy: natives},
	}
}

// BubblewrapPlan is the install command for a missing bwrap, or a zero plan
// when the host does not need one.
type BubblewrapPlan struct {
	Needed bool
	Argv   []string
	Text   string
}

// ResolveBubblewrap decides whether this host still needs the bubblewrap
// package. goos other than linux, a bwrap on PATH, or a set BWRAP_BIN all
// produce an empty plan. bwrapBin is the value of BWRAP_BIN, not a lookup.
func ResolveBubblewrap(goos, bwrapBin string, lookPath func(string) (string, error)) BubblewrapPlan {
	if goos != "linux" || strings.TrimSpace(bwrapBin) != "" || commandFound(lookPath, "bwrap") {
		return BubblewrapPlan{}
	}
	manager, ok := selectBubblewrapManager(lookPath)
	if !ok {
		return BubblewrapPlan{Needed: true, Text: "install the bubblewrap package and ensure bwrap is on PATH"}
	}
	argv := manager.argv()
	return BubblewrapPlan{Needed: true, Argv: argv, Text: strings.Join(argv, " ")}
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

func (m packageManager) argv() []string {
	argv := append([]string{m.Command}, m.Args...)
	if len(m.Env) > 0 {
		argv = append(append([]string{"env"}, m.Env...), argv...)
	}
	if m.Sudo {
		argv = append([]string{"sudo", "-n"}, argv...)
	}
	return argv
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
	plan := ResolveBubblewrap(i.GOOS, os.Getenv("BWRAP_BIN"), lookPath)
	if !plan.Needed {
		return
	}
	if plan.Argv == nil {
		fmt.Fprintf(errOut, "bubblewrap: %s\n", plan.Text)
		return
	}
	if !systemDeps {
		fmt.Fprintf(out, "bubblewrap: bwrap is not installed. Re-run with --install-system-deps, or run: %s\n", plan.Text)
		return
	}
	if i.Run == nil {
		fmt.Fprintf(errOut, "bubblewrap: cannot run %s\n", plan.Text)
		return
	}
	if _, err := i.Run(ctx, plan.Argv[0], plan.Argv[1:]...); err != nil {
		fmt.Fprintf(errOut, "bubblewrap: %s failed: %v\n", plan.Text, err)
	}
}
