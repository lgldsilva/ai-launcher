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
// auxiliary tools + the dev-profile flag and memory requirement. Because
// Normalize canonicalized everything, the same
// selection always hashes to the same tag regardless of the order the user
// ticked the checkboxes — the cache hit depends only on the selection, which
// is what makes "identical selection → run without rebuilding" (R1 item 2)
// hold. Pinned versions (design C2) are part of the hash, so a version bump
// produces a new tag and a fresh build instead of a lying cache hit.
func ImageTag(selection Selection) (string, error) {
	return ImageTagWithOptions(selection, DockerfileOptions{})
}

// ImageTagWithOptions is ImageTag including the Dockerfile build options in
// the hash: an option changes the image contents, so it must change the tag
// or a same-selection image built without the option would be reused for a
// launch that needs it (a lying cache hit). DockerfileOptions.LauncherHash is
// part of the hash for the same reason, even though it leaves the rendered
// Dockerfile untouched: the launcher binary copied into the image is image
// content, and omitting it made an upgraded launcher reuse the image an older
// one built.
func ImageTagWithOptions(selection Selection, options DockerfileOptions) (string, error) {
	canonical, err := selectionCanonical(selection)
	if err != nil {
		return "", err
	}
	if options.DockerCLI {
		canonical += "|docker-cli"
	}
	if options.LauncherHash != "" {
		canonical += "|launcher=" + options.LauncherHash
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
		if agent.NeedsNode {
			b.WriteString(":node")
		}
		if agent.NeedsMemory {
			b.WriteString(":memory")
		}
		b.WriteString(",")
	}
	for _, tool := range selection.Tools {
		b.WriteString("tool:")
		b.WriteString(tool.Command)
		b.WriteString("=")
		b.WriteString(tool.Version)
		b.WriteString("@")
		b.WriteString(string(tool.Kind))
		if tool.Release != nil {
			b.WriteString(":")
			b.WriteString(tool.Release.Repository)
			for _, key := range SortedAssetKeys(tool.Release.Assets) {
				b.WriteString("|")
				b.WriteString(key)
				b.WriteString("=")
				b.WriteString(tool.Release.Assets[key])
			}
		}
		b.WriteString(",")
	}
	return b.String(), nil
}
