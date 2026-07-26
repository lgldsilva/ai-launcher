package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/lgldsilva/ai-launcher/internal/selfupdate"
)

// stubUpgradeSeams replaces the upgrade resolver/apply seams for one test and
// returns a record of the applied tags.
func stubUpgradeSeams(t *testing.T, tag string, resolveErr, applyErr error) *[]string {
	t.Helper()
	applied := &[]string{}
	oldResolve, oldApply := upgradeResolveTag, upgradeApply
	t.Cleanup(func() { upgradeResolveTag, upgradeApply = oldResolve, oldApply })
	upgradeResolveTag = func(_ context.Context, _ *selfupdate.Updater, wantVersion string) (string, error) {
		if wantVersion != "" {
			return wantVersion, nil
		}
		return tag, resolveErr
	}
	upgradeApply = func(_ context.Context, _ *selfupdate.Updater, got string) error {
		*applied = append(*applied, got)
		return applyErr
	}
	return applied
}

func TestUpgradeCheckReportsUpdate(t *testing.T) {
	applied := stubUpgradeSeams(t, "v9.9.9", nil, nil)
	out, err := runDryRun(t, "upgrade", "--check")
	if err != nil {
		t.Fatal(err)
	}
	// The test binary is a dev build, which is always outdated.
	for _, want := range []string{"current: dev", "latest:  v9.9.9", "an update is available"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output %q missing %q", out, want)
		}
	}
	if len(*applied) != 0 {
		t.Fatalf("--check must not apply, applied %v", *applied)
	}
}

func TestUpgradeCheckUpToDate(t *testing.T) {
	stubUpgradeSeams(t, "v1.2.3", nil, nil)
	old := version
	version = "v1.2.3"
	t.Cleanup(func() { version = old })
	out, err := runDryRun(t, "upgrade", "--check")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "already up to date.") {
		t.Fatalf("output %q, want already up to date", out)
	}
}

func TestUpgradeAlreadyUpToDateSkipsApply(t *testing.T) {
	applied := stubUpgradeSeams(t, "v1.2.3", nil, nil)
	old := version
	version = "v1.2.3"
	t.Cleanup(func() { version = old })
	out, err := runDryRun(t, "upgrade")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "already up to date (v1.2.3)") {
		t.Fatalf("output %q, want already up to date (v1.2.3)", out)
	}
	if len(*applied) != 0 {
		t.Fatalf("nothing should be applied, got %v", *applied)
	}
}

func TestUpgradeAppliesLatest(t *testing.T) {
	applied := stubUpgradeSeams(t, "v0.2.0", nil, nil)
	out, err := runDryRun(t, "upgrade")
	if err != nil {
		t.Fatal(err)
	}
	if len(*applied) != 1 || (*applied)[0] != "v0.2.0" {
		t.Fatalf("applied = %v, want [v0.2.0]", *applied)
	}
	if !strings.Contains(out, "ai-launcher updated: dev → v0.2.0") {
		t.Fatalf("output %q, want the updated banner", out)
	}
}

func TestUpgradeVersionFlagPinsTag(t *testing.T) {
	// The resolver seam returns the requested tag without touching the
	// network, so any resolution error proves --version was not honored.
	applied := stubUpgradeSeams(t, "", errors.New("network should not be used"), nil)
	out, err := runDryRun(t, "upgrade", "--version", "v1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	if len(*applied) != 1 || (*applied)[0] != "v1.0.0" {
		t.Fatalf("applied = %v, want [v1.0.0]", *applied)
	}
	if !strings.Contains(out, "ai-launcher updated: dev → v1.0.0") {
		t.Fatalf("output %q, want the updated banner", out)
	}
}

func TestUpgradePropagatesApplyError(t *testing.T) {
	stubUpgradeSeams(t, "v0.2.0", nil, errors.New("boom"))
	if _, err := runDryRun(t, "upgrade"); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("err = %v, want boom", err)
	}
}

func TestUpgradeUnknownFlagFails(t *testing.T) {
	if _, err := runDryRun(t, "upgrade", "--nope"); err == nil {
		t.Fatal("expected an error for an unknown upgrade flag")
	}
}

// captureUpdater stubs the resolve seam and records the updater the command
// wiring built, so tests can assert how environment variables were folded in.
func captureUpdater(t *testing.T) **selfupdate.Updater {
	t.Helper()
	captured := &selfupdate.Updater{}
	oldResolve, oldApply := upgradeResolveTag, upgradeApply
	t.Cleanup(func() { upgradeResolveTag, upgradeApply = oldResolve, oldApply })
	upgradeResolveTag = func(_ context.Context, updater *selfupdate.Updater, _ string) (string, error) {
		*captured = *updater
		return "v9.9.9", nil
	}
	upgradeApply = func(_ context.Context, _ *selfupdate.Updater, _ string) error { return nil }
	return &captured
}

func TestUpgradeWithholdsTokenWhenEndpointIsOverridden(t *testing.T) {
	captured := captureUpdater(t)
	t.Setenv("AI_LAUNCHER_UPDATE_TOKEN", "secret-token")
	t.Setenv("AI_LAUNCHER_UPDATE_API", "https://evil.example.com/api")
	t.Setenv("AI_LAUNCHER_UPDATE_URL", "")
	var out, errOut bytes.Buffer
	if err := run([]string{"upgrade"}, strings.NewReader(""), &out, &errOut); err != nil {
		t.Fatal(err)
	}
	if (*captured).Token != "" {
		t.Fatalf("Token = %q; want it withheld when the endpoint is overridden", (*captured).Token)
	}
	if !strings.Contains(errOut.String(), "warning:") {
		t.Fatalf("stderr %q; want a warning about the withheld token", errOut.String())
	}
}

func TestUpgradeSendsTokenToDefaultEndpoint(t *testing.T) {
	captured := captureUpdater(t)
	t.Setenv("AI_LAUNCHER_UPDATE_TOKEN", "secret-token")
	t.Setenv("AI_LAUNCHER_UPDATE_API", "")
	t.Setenv("AI_LAUNCHER_UPDATE_URL", "")
	var out, errOut bytes.Buffer
	if err := run([]string{"upgrade"}, strings.NewReader(""), &out, &errOut); err != nil {
		t.Fatal(err)
	}
	if (*captured).Token != "secret-token" {
		t.Fatalf("Token = %q; want the configured token against the default endpoint", (*captured).Token)
	}
}
