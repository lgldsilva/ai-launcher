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
// Script, when set, is the body of `sh -c`: apt-get and eopkg have no single
// flag that both refreshes the index and installs. NixStore marks a row
// whose bwrap lands in /nix/store, which ai-jail only trusts while the store
// is protected; on a single-user install the row is skipped.
type packageManager struct {
	Command   string
	Args      []string
	Script    string
	Env       []string
	Sudo      bool
	NixStore  bool
	BlockedBy []string
}

// bubblewrapManagers is the detection order. The binary of ai-jail itself
// stays on the GitHub release path; this list only installs bwrap. Linuxbrew
// is absent because its bwrap belongs to the brew user, which ai-jail
// refuses; slackpkg is absent because stock Slackware has no bubblewrap.
func bubblewrapManagers() []packageManager {
	return []packageManager{
		{Command: "apt-get", Script: "apt-get update && apt-get install -y " + bubblewrapPackage, Env: []string{"DEBIAN_FRONTEND=noninteractive"}, Sudo: true},
		{Command: "dnf", Args: []string{"install", "-y", bubblewrapPackage}, Sudo: true},
		{Command: "yum", Args: []string{"install", "-y", bubblewrapPackage}, Sudo: true, BlockedBy: []string{"dnf"}},
		{Command: "microdnf", Args: []string{"install", "-y", bubblewrapPackage}, Sudo: true, BlockedBy: []string{"dnf", "yum"}},
		{Command: "pacman", Args: []string{"-S", noConfirmFlag, bubblewrapPackage}, Sudo: true},
		{Command: "zypper", Args: []string{"--non-interactive", "install", bubblewrapPackage}, Sudo: true},
		{Command: "apk", Args: []string{"add", "--no-interactive", bubblewrapPackage}, Sudo: true},
		{Command: "xbps-install", Args: []string{"-S", "-y", bubblewrapPackage}, Sudo: true},
		{Command: "eopkg", Script: "eopkg update-repo && eopkg install -y " + bubblewrapPackage, Sudo: true},
		{Command: "urpmi", Args: []string{"--auto", bubblewrapPackage}, Sudo: true},
		{Command: "emerge", Args: []string{"--ask=n", "sys-apps/bubblewrap"}, Sudo: true},
		{Command: "opkg", Args: []string{"install", bubblewrapPackage}, Sudo: true},
		{Command: "nix", Args: []string{"--extra-experimental-features", "nix-command flakes", "profile", "install", "nixpkgs#bubblewrap"}, NixStore: true},
		{Command: "guix", Args: []string{"install", bubblewrapPackage}},
		{Command: "pamac", Args: []string{"install", "--no-confirm", bubblewrapPackage}, Sudo: true, BlockedBy: []string{"pacman"}},
		{Command: "yay", Args: []string{"-S", noConfirmFlag, bubblewrapPackage}, BlockedBy: []string{"pacman", "pamac"}},
		{Command: "paru", Args: []string{"-S", noConfirmFlag, bubblewrapPackage}, BlockedBy: []string{"pacman", "pamac", "yay"}},
	}
}

// BubblewrapPlan is the install command for a missing bwrap, or a zero plan
// when the host does not need one. Argv is what the launcher executes.
// Text is what a person can copy: it has no sudo -n, because -n refuses to
// ask for a password. Untrusted names a bwrap that exists but that ai-jail
// refuses to run, when that is why the plan is needed.
type BubblewrapPlan struct {
	Needed    bool
	Argv      []string
	Text      string
	Untrusted string
}

// BwrapHost is what ResolveBubblewrap reads from the machine. BwrapBin is
// the value of BWRAP_BIN, not a lookup. Root is true for uid 0. A nil Probe
// counts any BWRAP_BIN and any bwrap on PATH as usable, without ai-jail's
// ownership check.
type BwrapHost struct {
	GOOS     string
	BwrapBin string
	Root     bool
	LookPath func(string) (string, error)
	Probe    BwrapProbe
}

// ResolveBubblewrap decides whether this host still needs the bubblewrap
// package. goos other than linux, or a bwrap ai-jail would run, produce an
// empty plan. Root does not use sudo, because a container image often has
// no sudo binary. A normal user on a host without sudo (doas on Alpine or
// Void) gets no Argv: the manager would only fail on permissions, so the
// plan names the command for a root shell instead.
func ResolveBubblewrap(host BwrapHost) BubblewrapPlan {
	if host.GOOS != "linux" {
		return BubblewrapPlan{}
	}
	search := findTrustedBwrap(host.BwrapBin, host.LookPath, host.Probe)
	if search.Found {
		return BubblewrapPlan{}
	}
	plan := BubblewrapPlan{Needed: true, Untrusted: search.Untrusted}
	manager, ok := selectBubblewrapManager(host.LookPath, host.Probe)
	if !ok {
		plan.Text = "install the bubblewrap package and ensure bwrap is on PATH"
		return plan
	}
	elevate := manager.Sudo && !host.Root
	sudo := elevate && commandFound(host.LookPath, "sudo")
	if elevate && !sudo {
		plan.Text = "as root, run: " + manager.display(false)
		return plan
	}
	plan.Argv = manager.execArgv(sudo)
	plan.Text = manager.display(sudo)
	return plan
}

// selectBubblewrapManager returns the first row on PATH that is not blocked.
// A Nix row is skipped when the probe shows an unprotected store: the bwrap
// it installs there is one ai-jail refuses.
func selectBubblewrapManager(lookPath func(string) (string, error), probe BwrapProbe) (packageManager, bool) {
	for _, manager := range bubblewrapManagers() {
		if blocked(lookPath, manager.BlockedBy) || !commandFound(lookPath, manager.Command) {
			continue
		}
		if manager.NixStore && probe != nil && !nixStoreProtected(probe) {
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

// UntrustedBwrapReason explains why ai-jail refuses a bwrap that exists.
func UntrustedBwrapReason(path string) string {
	return fmt.Sprintf("ai-jail does not trust the bwrap at %s: it must be owned by root and not group- or world-writable, or sit read-only in a root-owned /nix/store", path)
}

// EnsureBubblewrap installs the distro bubblewrap package when systemDeps is
// set and ai-jail has no bwrap it trusts. A missing manager or a failed sudo
// is reported and does not stop the ai-jail binary install that follows.
// Without systemDeps the command is printed and not run. After a run, a
// bwrap ai-jail still cannot use is reported.
func (i *Installer) EnsureBubblewrap(ctx context.Context, systemDeps bool, out, errOut io.Writer) {
	if i == nil {
		return
	}
	host := i.bwrapHost()
	plan := ResolveBubblewrap(host)
	if !plan.Needed {
		return
	}
	if plan.Untrusted != "" {
		_, _ = fmt.Fprintf(errOut, "bubblewrap: %s\n", UntrustedBwrapReason(plan.Untrusted))
	}
	if plan.Argv == nil {
		_, _ = fmt.Fprintf(errOut, "bubblewrap: %s\n", plan.Text)
		return
	}
	if !systemDeps {
		_, _ = fmt.Fprintf(out, "bubblewrap: ai-jail has no bwrap it can run. Re-run with --install-system-deps, or run: %s\n", plan.Text)
		return
	}
	if i.Run == nil {
		_, _ = fmt.Fprintf(errOut, "bubblewrap: cannot run %s\n", plan.Text)
		return
	}
	if _, err := i.Run(ctx, plan.Argv[0], plan.Argv[1:]...); err != nil {
		_, _ = fmt.Fprintf(errOut, "bubblewrap: %s failed: %v\n", plan.Text, err)
		return
	}
	if after := ResolveBubblewrap(host); after.Needed {
		_, _ = fmt.Fprintf(errOut, "bubblewrap: %s ran, but ai-jail still sees no bwrap it trusts; a guix or nix profile may need a new login shell\n", plan.Text)
	}
}

func (i *Installer) bwrapHost() BwrapHost {
	lookPath := i.LookPath
	if lookPath == nil {
		lookPath = func(string) (string, error) { return "", fmt.Errorf("lookpath unset") }
	}
	return BwrapHost{
		GOOS:     i.GOOS,
		BwrapBin: os.Getenv("BWRAP_BIN"),
		Root:     os.Geteuid() == 0,
		LookPath: lookPath,
		Probe:    i.BwrapProbe,
	}
}
