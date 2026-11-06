package launcher

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/lgldsilva/ai-launcher/internal/config"
)

// JailFeatures is the feature set from `ai-jail capabilities --json`.
// A false flag means this binary does not speak that part of the contract,
// which is the 2.x answer and the answer when the probe could not parse JSON.
type JailFeatures struct {
	HarnessFlag       bool `json:"harness_flag"`
	RequestFile       bool `json:"request_file"`
	StatusJSON        bool `json:"status_json"`
	SecretPrivateHTTP bool `json:"secret_private_http"`
	ConfigureRequests bool `json:"configure_requests"`
	InsideMarker      bool `json:"inside_marker"`
}

// JailExitCodes are the machine-facing exits from the wrapper contract.
// Zero values are filled from the 3.0.1 defaults (3, 4, 5, 130) when the
// document advertises the contract but omits a number.
type JailExitCodes struct {
	Cancelled          int `json:"cancelled"`
	RequiredGrant      int `json:"required_grant"`
	Blocked            int `json:"blocked"`
	BackendUnavailable int `json:"backend_unavailable"`
}

// JailContract is a parsed `ai-jail capabilities --json` document.
// The zero value means the probe did not run. Probed with an empty feature
// set means the binary was found and does not speak the contract, so Build
// keeps the 2.x argv instead of guessing from the version string.
type JailContract struct {
	Probed        bool
	Path          string
	Version       string        `json:"version"`
	SchemaVersion int           `json:"schema_version"`
	Flags         []string      `json:"flags"`
	Features      JailFeatures  `json:"features"`
	ExitCodes     JailExitCodes `json:"exit_codes"`
}

// JailBlocker is one entry from `ai-jail status --json`.
type JailBlocker struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// JailStatusHarness is one harness row from `ai-jail status --json`.
type JailStatusHarness struct {
	Name               string `json:"name"`
	OnboardingComplete bool   `json:"onboarding_complete"`
}

// JailRequestIdentity is one approved or pending wrapper request.
// Values are names and paths; a secret request names the variable, never
// the secret itself.
type JailRequestIdentity struct {
	Requester string `json:"requester"`
	Kind      string `json:"kind"`
	Value     string `json:"value"`
	Host      string `json:"host,omitempty"`
	Port      int    `json:"port,omitempty"`
}

// JailStatusRequests splits wrapper requests the way status --json does.
type JailStatusRequests struct {
	Pending  []JailRequestIdentity `json:"pending"`
	Approved []JailRequestIdentity `json:"approved"`
}

// JailStatus is a parsed `ai-jail status --json` document.
type JailStatus struct {
	Version       string              `json:"version"`
	SchemaVersion int                 `json:"schema_version"`
	Blockers      []JailBlocker       `json:"blockers"`
	Harnesses     []JailStatusHarness `json:"harnesses"`
	Requests      JailStatusRequests  `json:"requests"`
}

// Documented 3.0.1 exits, used when a contract document omits the numbers.
const (
	jailExitCancelledDefault          = 130
	jailExitRequiredGrantDefault      = 3
	jailExitBlockedDefault            = 4
	jailExitBackendUnavailableDefault = 5
)

// runJailCommand executes ai-jail with fixed subcommand arguments.
// It is a seam so tests can feed capabilities and status JSON without a binary.
var runJailCommand = func(path string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), versionProbeTimeout)
	defer cancel()
	// #nosec G204 -- path is LookPath of the fixed ai-jail name; args are fixed subcommands, not operator input
	return exec.CommandContext(ctx, path, args...).CombinedOutput()
}

var jailContractCache struct {
	once  sync.Once
	value JailContract
}

// ProbeJailContract reads `ai-jail capabilities --json` at most once per
// process. A missing binary stays unprobed so a caller that already knows
// the version can fall back to MinJailContractVersion. A binary that does
// not speak the contract is remembered as probed with no features, which
// keeps the 2.x argv.
func ProbeJailContract() JailContract {
	jailContractCache.once.Do(func() { jailContractCache.value = probeJailContract() })
	return jailContractCache.value
}

func probeJailContract() JailContract {
	path, err := lookPathCommand(config.AIJailCommand)
	if err != nil {
		return JailContract{}
	}
	output, _ := runJailCommand(path, "capabilities", "--json")
	contract, parseErr := ParseJailCapabilities(output)
	if parseErr != nil {
		return JailContract{Probed: true, Path: path}
	}
	contract.Probed = true
	contract.Path = path
	return contract
}

// ParseJailCapabilities parses a capabilities --json document.
// Leading or trailing log lines are ignored; the JSON object is the span
// from the first '{' to the last '}'.
func ParseJailCapabilities(output []byte) (JailContract, error) {
	raw, err := jsonObject(output)
	if err != nil {
		return JailContract{}, err
	}
	var contract JailContract
	if err := json.Unmarshal(raw, &contract); err != nil {
		return JailContract{}, err
	}
	if contract.SchemaVersion < 1 {
		return JailContract{}, errors.New("ai-jail capabilities JSON has no schema_version")
	}
	contract.ExitCodes = contract.ExitCodes.normalized()
	return contract, nil
}

// ParseJailStatus parses a status --json document.
func ParseJailStatus(output []byte) (JailStatus, error) {
	raw, err := jsonObject(output)
	if err != nil {
		return JailStatus{}, err
	}
	var status JailStatus
	if err := json.Unmarshal(raw, &status); err != nil {
		return JailStatus{}, err
	}
	if status.SchemaVersion < 1 {
		return JailStatus{}, errors.New("ai-jail status JSON has no schema_version")
	}
	return status, nil
}

func jsonObject(output []byte) ([]byte, error) {
	start := bytes.IndexByte(output, '{')
	end := bytes.LastIndexByte(output, '}')
	if start < 0 || end < start {
		return nil, errors.New("ai-jail JSON document not found")
	}
	return output[start : end+1], nil
}

func (codes JailExitCodes) normalized() JailExitCodes {
	if codes.Cancelled == 0 {
		codes.Cancelled = jailExitCancelledDefault
	}
	if codes.RequiredGrant == 0 {
		codes.RequiredGrant = jailExitRequiredGrantDefault
	}
	if codes.Blocked == 0 {
		codes.Blocked = jailExitBlockedDefault
	}
	if codes.BackendUnavailable == 0 {
		codes.BackendUnavailable = jailExitBackendUnavailableDefault
	}
	return codes
}

// SupportsHarness reports whether --harness should be emitted.
// A probed contract answers from its feature flag. An unprobed contract
// falls back to the version floor, so a test or a caller that already
// resolved the version still gets the flag on 3.0.1+.
func (c JailContract) SupportsHarness(version string) bool {
	if c.Probed {
		return c.Features.HarnessFlag
	}
	return jailSupportsFlag(version, config.MinJailContractVersion)
}

// SupportsRequestFile reports whether --request-file is accepted.
// The rule matches SupportsHarness: the probe wins, the version is the fallback.
func (c JailContract) SupportsRequestFile(version string) bool {
	if c.Probed {
		return c.Features.RequestFile
	}
	return jailSupportsFlag(version, config.MinJailContractVersion)
}

// AdvertisesExitCodes reports whether a child exit can be classified with
// the wrapper contract. Version fallback is intentionally not enough: an
// exit code of 3 is also a normal child status on 2.x.
func (c JailContract) AdvertisesExitCodes() bool {
	if !c.Probed || c.SchemaVersion < 1 {
		return false
	}
	return c.Features.HarnessFlag || c.Features.RequestFile || c.Features.StatusJSON
}

// ClassifyJailExit maps a process status onto the wrapper contract.
// ok is false when the contract does not apply or the status is some other
// code (including the launched child's own status, which ai-jail forwards).
func ClassifyJailExit(code int, contract JailContract) (class, advice string, ok bool) {
	if !contract.AdvertisesExitCodes() {
		return "", "", false
	}
	codes := contract.ExitCodes.normalized()
	switch code {
	case codes.RequiredGrant:
		return "required_grant", jailExitAdvice("required_grant", code), true
	case codes.Blocked:
		return "blocked", jailExitAdvice("blocked", code), true
	case codes.BackendUnavailable:
		return "backend_unavailable", jailExitAdvice("backend_unavailable", code), true
	case codes.Cancelled:
		return "cancelled", jailExitAdvice("cancelled", code), true
	default:
		return "", "", false
	}
}

// ClassifyJailExitError is ClassifyJailExit for an *exec.ExitError.
// Other errors, including a failed start, are not contract exits.
func ClassifyJailExitError(err error, contract JailContract) (class, advice string, ok bool) {
	var exitErr *exec.ExitError
	if err == nil || !errors.As(err, &exitErr) {
		return "", "", false
	}
	return ClassifyJailExit(exitErr.ExitCode(), contract)
}

func jailExitAdvice(class string, code int) string {
	switch class {
	case "required_grant":
		return fmt.Sprintf(
			"ai-jail exit %d (required_grant): a wrapper request is not approved. Review it with `ai-jail configure --harness HARNESS` before retrying, or remove jail_flags.request_file.",
			code)
	case "blocked":
		return fmt.Sprintf(
			"ai-jail exit %d (blocked): a saved approval is blocked by host drift (missing environment variable, replaced directory, or changed SSH key).",
			code)
	case "backend_unavailable":
		return fmt.Sprintf(
			"ai-jail exit %d (backend_unavailable): the sandbox backend is unavailable (bubblewrap on Linux, sandbox-exec on macOS).",
			code)
	case "cancelled":
		return fmt.Sprintf("ai-jail exit %d (cancelled): onboarding was cancelled.", code)
	default:
		return ""
	}
}

// JailExitHint returns the contract advice already present in errText.
// A bare "exit status 3" is not enough: that status is also a child exit
// on ai-jail 2.x, and this hint must not relabel it.
func JailExitHint(errText string) string {
	for _, line := range strings.Split(errText, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "ai-jail exit ") && strings.Contains(line, "(") {
			return line
		}
	}
	return ""
}

// DoctorNote is the one line --doctor prints when the contract was detected.
// An install that does not speak it produces an empty note, same as 2.x.
func (c JailContract) DoctorNote() string {
	if !c.AdvertisesExitCodes() {
		return ""
	}
	codes := c.ExitCodes.normalized()
	marker := "false"
	if c.Features.InsideMarker {
		marker = "true"
	}
	return fmt.Sprintf(
		"ai-jail contract schema %d (%s): harness=%t request-file=%t status-json=%t inside-marker=%s; exits required_grant=%d blocked=%d backend_unavailable=%d",
		c.SchemaVersion, c.Version,
		c.Features.HarnessFlag, c.Features.RequestFile, c.Features.StatusJSON, marker,
		codes.RequiredGrant, codes.Blocked, codes.BackendUnavailable,
	)
}

// ProbeJailStatus runs `ai-jail status --json` when the contract advertises
// it. Status is read-only: it does not prompt, migrate, or save policy.
// A probe that does not return schema JSON is ignored.
func ProbeJailStatus(contract JailContract) (JailStatus, bool) {
	if !contract.Features.StatusJSON || strings.TrimSpace(contract.Path) == "" {
		return JailStatus{}, false
	}
	output, _ := runJailCommand(contract.Path, "status", "--json")
	status, err := ParseJailStatus(output)
	if err != nil {
		return JailStatus{}, false
	}
	return status, true
}

// FormatJailStatus renders blocker rows for --doctor. Other status fields
// stay out of the text: grants and requests are large, and blockers are the
// part an operator acts on.
func FormatJailStatus(status JailStatus) []string {
	lines := make([]string, 0, len(status.Blockers))
	for _, blocker := range status.Blockers {
		code := strings.TrimSpace(blocker.Code)
		if code == "" {
			continue
		}
		lines = append(lines, fmt.Sprintf("ai-jail-status %s: %s", code, strings.TrimSpace(blocker.Message)))
	}
	return lines
}

// JailStoreDir is the ai-jail 3 trusted store: $XDG_CONFIG_HOME/ai-jail, or
// ~/.config/ai-jail when XDG_CONFIG_HOME is unset. A relative XDG base is
// rejected, matching ai-jail. ok is false when neither home nor an absolute
// XDG base is available.
func JailStoreDir(home, xdgConfigHome string) (string, bool) {
	base := strings.TrimSpace(xdgConfigHome)
	if base == "" {
		home = strings.TrimSpace(home)
		if home == "" {
			return "", false
		}
		base = filepath.Join(home, ".config")
	}
	if !filepath.IsAbs(base) {
		return "", false
	}
	return filepath.Clean(filepath.Join(base, "ai-jail")), true
}

// jailUsesExternalStore reports whether this launch's ai-jail keeps trusted
// policy in the XDG store. The store arrived in 3.0.0, one release before
// the wrapper contract, so the version floor and a probed contract both count.
func jailUsesExternalStore(cfg LaunchConfig) bool {
	if jailSupportsFlag(cfg.JailVersion, config.MinXDGStoreAIJailVersion) {
		return true
	}
	return cfg.JailContract.Probed && cfg.JailContract.AdvertisesExitCodes()
}

func launchSupportsHarness(cfg LaunchConfig) bool {
	return cfg.JailContract.SupportsHarness(cfg.JailVersion)
}

// jailAcceptsHarness reports whether ai-jail 3.0.1 will accept --harness NAME.
// The wrapper contract rejects an unknown name before the sandbox starts, so
// emitting it for a catalog agent ai-jail does not know (kilo, goose, oc, …)
// turns a launch that 2.x ran into an immediate failure. Those agents keep
// the legacy argv inference. The set mirrors command::normalize_harness_name
// and KNOWN_API_AGENTS in ai-jail 3.0.1, including the kimi* prefix.
func jailAcceptsHarness(name string) bool {
	switch strings.TrimSpace(name) {
	case "claude", "claude-code",
		"codex",
		"opencode", "open-code",
		"pi",
		"omp", "oh-my-pi",
		"agy", "antigravity", "antigravity-cli",
		"gemini", "crush", "grok", "jcode", "devin", "aider", "soulforge", "kiro-cli":
		return true
	default:
		return strings.HasPrefix(strings.TrimSpace(name), "kimi")
	}
}

func launchSupportsRequestFile(cfg LaunchConfig) bool {
	return cfg.JailContract.SupportsRequestFile(cfg.JailVersion)
}

// jail3KnownAPIHosts are the hosts ai-jail 3 adds by itself for a known API
// agent during non-interactive onboarding. Passing --network in that case is
// rejected: filtered egress and unrestricted network cannot be combined.
// The map mirrors command::api_host in ai-jail 3.0.1, plus chatgpt.com for
// codex, which default_egress_hosts adds beside api.openai.com.
func jail3KnownAPIHosts(name string) []string {
	switch strings.TrimSpace(name) {
	case "claude", "claude-code":
		return []string{"api.anthropic.com"}
	case "codex":
		return []string{"api.openai.com", "chatgpt.com"}
	case "gemini":
		return []string{"generativelanguage.googleapis.com"}
	case "grok":
		return []string{"api.x.ai"}
	case "devin":
		return []string{"server.codeium.com"}
	default:
		return nil
	}
}

// jail3FiltersUnrestrictedNetwork reports whether this launch must express
// "network on" as filtered egress. ai-jail 3's non-interactive onboarding
// inserts the harness API host into allow_hosts, then refuses --network.
func jail3FiltersUnrestrictedNetwork(cfg LaunchConfig) bool {
	if !jailAtLeast3(cfg) {
		return false
	}
	return len(jail3KnownAPIHosts(resolvedHarnessName(cfg))) > 0
}

func jailAtLeast3(cfg LaunchConfig) bool {
	if jailSupportsFlag(cfg.JailVersion, config.MinXDGStoreAIJailVersion) {
		return true
	}
	return cfg.JailContract.Probed && cfg.JailContract.SchemaVersion >= 1
}

func resolvedHarnessName(cfg LaunchConfig) string {
	if cfg.UseMemory {
		return memoryRunHarness(cfg.Agent)
	}
	return filepath.Base(strings.TrimSpace(cfg.Agent.Command))
}
