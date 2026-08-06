package container

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// ImageTag derives the deterministic image tag for a canonical Selection.
//
// The tag is the sha256 of the *canonical* serialization of the selection:
// sorted stacks + sorted agents (command + pinned version + install kind) +
// the dev-profile flag. Because Normalize canonicalized everything, the same
// selection always hashes to the same tag regardless of the order the user
// ticked the checkboxes — the cache hit depends only on the selection, which
// is what makes "identical selection → run without rebuilding" (R1 item 2)
// hold. Pinned versions (design C2) are part of the hash, so a version bump
// produces a new tag and a fresh build instead of a lying cache hit.
func ImageTag(selection Selection) (string, error) {
	canonical, err := selectionCanonical(selection)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(canonical))
	return "ai-launcher-box:" + hex.EncodeToString(sum[:])[:12], nil
}

// selectionCanonical returns the canonical serialization that backs both the
// image tag and (by construction) the Dockerfile. It must stay byte-identical
// for equal selections: any new field that affects the image contents MUST be
// added here, or the tag will lie.
func selectionCanonical(selection Selection) (string, error) {
	if err := selection.Validate(); err != nil {
		return "", err
	}
	var b strings.Builder
	b.WriteString("v1|")
	if selection.IncludeDevProfile {
		b.WriteString("dev|")
	} else {
		b.WriteString("nodev|")
	}
	b.WriteString(strings.Join(selection.Stacks, ","))
	b.WriteString("|")
	for _, agent := range selection.Agents {
		b.WriteString(agent.Command)
		b.WriteString("=")
		b.WriteString(agent.Version)
		b.WriteString("@")
		b.WriteString(string(agent.Kind))
		// The recipe text must be part of the hash: a changed source_url,
		// npm package, or setup flag changes the Dockerfile and therefore the
		// image — the tag must change too or the cache lies (H1).
		if agent.Kind == InstallScript {
			b.WriteString(":")
			b.WriteString(agent.Script)
		}
		if agent.Kind == InstallNpm {
			b.WriteString(":")
			b.WriteString(agent.NpmPackage)
		}
		if agent.Kind == InstallHostBinary {
			b.WriteString(":")
			b.WriteString(agent.HostPath)
		}
		if agent.AllowSetupFailure {
			b.WriteString(":setup-failure-tolerant")
		}
		b.WriteString(",")
	}
	return b.String(), nil
}
