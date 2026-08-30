#!/usr/bin/env bash
set -euo pipefail

BASE_URL="https://static.devin.ai/cli"
BINARY_NAME="devin"
PRODUCT_SUBDIR="cli"
DISTRIBUTION="curl-bash"
CLI_DISPLAY_NAME=""

# The binary inside the bundle is always "devin" (Cargo [[bin]] target name),
# regardless of channel. BINARY_NAME is the user-facing symlink name.
COMPILED_BIN_NAME="devin"
XDG_DIR_NAMESPACE="devin"

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

# Set by the build system for versioned setup scripts (e.g. cli/2026.3.5-1/setup.sh).
# The top-level setup.sh leaves this empty to install the latest promoted version.
PINNED_VERSION=""

detect_target() {
  local os arch
  os=$(uname -s)
  arch=$(uname -m)

  case "$os" in
    Darwin)
      case "$arch" in
        arm64) echo "aarch64-apple-darwin" ;;
        x86_64) echo "x86_64-apple-darwin" ;;
        *) echo "unsupported" ;;
      esac
      ;;
    Linux)
      case "$arch" in
        x86_64) echo "x86_64-unknown-linux" ;;
        aarch64) echo "aarch64-unknown-linux" ;;
        *) echo "unsupported" ;;
      esac
      ;;
    MINGW64_NT*|MINGW32_NT*|MSYS_NT*|CYGWIN*)
      echo "windows"
      ;;
    *)
      echo "unsupported"
      ;;
  esac
}

TARGET=$(detect_target)
if [ "$TARGET" = "unsupported" ]; then
  echo -e "${RED}Error: Unsupported platform ($(uname -s) $(uname -m))${NC}"
  exit 1
fi

# On Windows (Git Bash / MSYS2 / Cygwin), delegate to the PowerShell
# installer which handles zip extraction, PATH configuration, and other
# platform-native concerns.
if [ "$TARGET" = "windows" ]; then
  echo "Detected Windows (Git Bash). Launching PowerShell installer..."
  # Use the actual S3 filenames since BASE_URL points to the raw CDN
  # (static.devin.ai), not the friendly domain (cli.devin.ai) which has
  # CloudFront rewrites. When PINNED_VERSION is set, fetch the versioned PS1.
  PS1_PREFIX="${BASE_URL}"
  if [ -n "$PINNED_VERSION" ]; then
    PS1_PREFIX="${BASE_URL}/${PINNED_VERSION}"
  fi
  if [ "$DISTRIBUTION" = "enterprise-portal" ]; then
    # The portal serves the PS1 installer with the portal URL substituted in.
    SETUP_PS1_URL="${BASE_URL}/api/cli/setup.ps1"
  elif [ "$DISTRIBUTION" = "curl-bash-enterprise" ]; then
    SETUP_PS1_URL="${PS1_PREFIX}/setup-enterprise.ps1"
  elif [ "$DISTRIBUTION" = "curl-bash-windsurfcom" ]; then
    SETUP_PS1_URL="${PS1_PREFIX}/setup-windsurfcom.ps1"
  elif [ "$DISTRIBUTION" = "curl-bash-federal" ]; then
    SETUP_PS1_URL="${PS1_PREFIX}/setup-federal.ps1"
  else
    SETUP_PS1_URL="${PS1_PREFIX}/setup.ps1"
  fi
  TMPFILE="$(mktemp "${TEMP:-/tmp}/devin-setup.XXXXXX.ps1")"
  if ! curl -fsSL "$SETUP_PS1_URL" -o "$TMPFILE"; then
    echo -e "${RED}Error: Failed to download PowerShell installer from $SETUP_PS1_URL${NC}"
    rm -f "$TMPFILE"
    exit 1
  fi
  if command -v cygpath &> /dev/null; then
    WIN_TMPFILE="$(cygpath -w "$TMPFILE")"
  else
    # Fallback for minimal MSYS2/Cygwin without cygpath: convert /c/... to C:\...
    WIN_TMPFILE="$(echo "$TMPFILE" | sed 's|^/\([a-zA-Z]\)/|\1:\\|;s|/|\\|g')"
  fi
  powershell.exe -ExecutionPolicy Bypass -File "$WIN_TMPFILE"
  EXIT_CODE=$?
  rm -f "$TMPFILE"
  exit $EXIT_CODE
fi

# When PINNED_VERSION is set (versioned setup script), fetch the manifest from
# the versioned path instead of current/. This allows installing specific RC builds.
VERSION_PATH="${PINNED_VERSION:-current}"

if [ "$DISTRIBUTION" = "enterprise-portal" ]; then
  # The portal serves exactly one version; there is no versioned manifest path.
  MANIFEST_URL="${BASE_URL}/api/cli/update"
elif [ "$DISTRIBUTION" = "curl-bash-enterprise" ]; then
  MANIFEST_URL="${BASE_URL}/${VERSION_PATH}/manifest-enterprise.json"
elif [ "$DISTRIBUTION" = "curl-bash-windsurfcom" ]; then
  MANIFEST_URL="${BASE_URL}/${VERSION_PATH}/manifest-windsurfcom.json"
elif [ "$DISTRIBUTION" = "curl-bash-federal" ]; then
  MANIFEST_URL="${BASE_URL}/${VERSION_PATH}/manifest-federal.json"
else
  MANIFEST_URL="${BASE_URL}/${VERSION_PATH}/manifest.json"
fi
MANIFEST=$(curl -sSf "$MANIFEST_URL" || true)
if [ -z "$MANIFEST" ]; then
  echo -e "${RED}Error: Failed to fetch manifest from $MANIFEST_URL${NC}"
  exit 1
fi

VERSION=$(echo "$MANIFEST" | grep -o '"version"[[:space:]]*:[[:space:]]*"[^"]*"' | head -1 | sed 's/.*"\([^"]*\)"$/\1/' || true)
if [ -z "$VERSION" ]; then
  echo -e "${RED}Error: Failed to parse version from manifest${NC}"
  exit 1
fi

BUNDLE_URL=$(echo "$MANIFEST" | grep -o "\"$TARGET\"[[:space:]]*:[[:space:]]*{[^}]*}" | grep -o '"url"[[:space:]]*:[[:space:]]*"[^"]*"' | sed 's/.*"\([^"]*\)"$/\1/' || true)
if [ -z "$BUNDLE_URL" ]; then
  echo -e "${RED}Error: No bundle available for platform $TARGET${NC}"
  exit 1
fi

EXPECTED_SHA256=$(echo "$MANIFEST" | grep -o "\"$TARGET\"[[:space:]]*:[[:space:]]*{[^}]*}" | grep -o '"sha256"[[:space:]]*:[[:space:]]*"[^"]*"' | sed 's/.*"\([^"]*\)"$/\1/' || true)
if [ -z "$EXPECTED_SHA256" ]; then
  echo -e "${RED}Error: No checksum found in manifest for platform $TARGET${NC}"
  exit 1
fi

XDG_DATA_HOME="${XDG_DATA_HOME:-$HOME/.local/share}"
VERSIONS_DIR="$XDG_DATA_HOME/$XDG_DIR_NAMESPACE/$PRODUCT_SUBDIR/_versions"
VERSION_DIR="$VERSIONS_DIR/$VERSION"
CURRENT_LINK="$VERSIONS_DIR/current"
LOCAL_BIN_DIR="$HOME/.local/bin"

# Migrate old namespace data dirs to devin/ before creating the versions
# directory. If we mkdir -p devin/ first, both old and devin/ would
# exist when the binary's migration runs, causing it to skip (to avoid data
# loss), which would strand credentials, sessions, etc. at the old path.
# Check cognition/ first (most common), then chisel/ (very old installs).
OLD_NAMESPACE_DIR=""
if [ -d "$XDG_DATA_HOME/cognition" ] && [ ! -e "$XDG_DATA_HOME/$XDG_DIR_NAMESPACE" ]; then
  OLD_NAMESPACE_DIR="$XDG_DATA_HOME/cognition"
elif [ -d "$XDG_DATA_HOME/chisel" ] && [ ! -e "$XDG_DATA_HOME/$XDG_DIR_NAMESPACE" ]; then
  OLD_NAMESPACE_DIR="$XDG_DATA_HOME/chisel"
fi
if [ -n "$OLD_NAMESPACE_DIR" ] && [ "$XDG_DIR_NAMESPACE" != "$(basename "$OLD_NAMESPACE_DIR")" ]; then
  NEW_NAMESPACE_DIR="$XDG_DATA_HOME/$XDG_DIR_NAMESPACE"
  mv "$OLD_NAMESPACE_DIR" "$NEW_NAMESPACE_DIR"
  ln -s "$NEW_NAMESPACE_DIR" "$OLD_NAMESPACE_DIR"
fi

mkdir -p "$VERSIONS_DIR"
mkdir -p "$LOCAL_BIN_DIR"

if [ -d "$VERSION_DIR" ] && [ -x "$VERSION_DIR/bin/$COMPILED_BIN_NAME" ]; then
  : # already installed, skip download
else
  DOWNLOAD_DIR="$VERSIONS_DIR/_download"
  mkdir -p "$DOWNLOAD_DIR"

  BUNDLE_FILE="$DOWNLOAD_DIR/$VERSION.tar.gz"
  TEMP_EXTRACT_DIR="$VERSIONS_DIR/_${VERSION}.tmp"

  if ! curl -f --progress-bar "$BUNDLE_URL" -o "$BUNDLE_FILE"; then
    echo -e "${RED}Error: Failed to download from $BUNDLE_URL${NC}"
    exit 1
  fi
  # Erase curl's progress bar line so the checkmark replaces it.
  printf "\033[1A\033[2K"

  if command -v sha256sum &> /dev/null; then
    ACTUAL_SHA256=$(sha256sum "$BUNDLE_FILE" | cut -d' ' -f1)
  elif command -v shasum &> /dev/null; then
    ACTUAL_SHA256=$(shasum -a 256 "$BUNDLE_FILE" | cut -d' ' -f1)
  else
    rm -f "$BUNDLE_FILE"
    echo -e "${RED}Error: Cannot verify checksum (no sha256sum or shasum)${NC}"
    exit 1
  fi

  if [ "$ACTUAL_SHA256" != "$EXPECTED_SHA256" ]; then
    rm -f "$BUNDLE_FILE"
    echo -e "${RED}Error: Checksum mismatch${NC}"
    echo "Expected: $EXPECTED_SHA256"
    echo "Got:      $ACTUAL_SHA256"
    exit 1
  fi

  rm -rf "$TEMP_EXTRACT_DIR"
  mkdir -p "$TEMP_EXTRACT_DIR"

  tar xzf "$BUNDLE_FILE" -C "$TEMP_EXTRACT_DIR"

  rm -rf "$VERSION_DIR"
  mv "$TEMP_EXTRACT_DIR" "$VERSION_DIR"
  rm -f "$BUNDLE_FILE"
fi

rm -f "$CURRENT_LINK"
ln -s "$VERSION" "$CURRENT_LINK"

TARGET_BIN="$LOCAL_BIN_DIR/$BINARY_NAME"
CURRENT_BIN="$CURRENT_LINK/bin/$COMPILED_BIN_NAME"

if [ -L "$TARGET_BIN" ]; then
  # Fully resolve the existing symlink through any intermediate symlinks (e.g.
  # the $XDG_DATA_HOME/cognition -> $XDG_DATA_HOME/devin legacy compat symlink
  # created by the namespace migration above, or the _versions/current ->
  # _versions/<version> symlink) and check that it lands inside the physical
  # $VERSIONS_DIR. Old installs may have $TARGET_BIN pointing at a legacy path
  # like $XDG_DATA_HOME/cognition/cli/_versions/current/bin/devin, which still
  # resolves to the same file and should be accepted rather than erroring.
  EXISTING_TARGET=$(readlink "$TARGET_BIN")
  EXISTING_RESOLVED=""
  if [ -e "$EXISTING_TARGET" ]; then
    EXISTING_RESOLVED=$(cd "$(dirname "$EXISTING_TARGET")" 2>/dev/null && pwd -P || true)
  fi
  VERSIONS_RESOLVED=$(cd "$VERSIONS_DIR" 2>/dev/null && pwd -P || true)
  if [ -n "$EXISTING_RESOLVED" ] && [ -n "$VERSIONS_RESOLVED" ] && [[ "$EXISTING_RESOLVED" == "$VERSIONS_RESOLVED"/* ]]; then
    rm -f "$TARGET_BIN"
  else
    echo -e "${RED}Error: $TARGET_BIN exists but points to unexpected location: $EXISTING_TARGET${NC}"
    echo "Please remove it manually if you want to proceed."
    exit 1
  fi
elif [ -e "$TARGET_BIN" ]; then
  echo -e "${RED}Error: $TARGET_BIN already exists and is not a symlink${NC}"
  echo "Please remove or rename it manually if you want to proceed."
  exit 1
fi
ln -s "$CURRENT_BIN" "$TARGET_BIN"

# Link man pages silently.
MAN_SRC_DIR="$CURRENT_LINK/share/man/man1"
if [ -d "$VERSION_DIR/share/man/man1" ]; then
  LOCAL_MAN_DIR="${XDG_DATA_HOME}/man/man1"
  mkdir -p "$LOCAL_MAN_DIR"
  for manpage in "$MAN_SRC_DIR"/*.1; do
    [ -f "$manpage" ] || continue
    TARGET_MAN="$LOCAL_MAN_DIR/$(basename "$manpage")"
    rm -f "$TARGET_MAN"
    ln -s "$manpage" "$TARGET_MAN"
  done
fi

# Write the distribution marker in the version root so the updater uses the
# correct URLs going forward. Each installation method (curl|bash, Homebrew,
# etc.) keeps its own marker.
DISTRIBUTION_FILE="$VERSION_DIR/distribution"
echo "$DISTRIBUTION" > "$DISTRIBUTION_FILE"

# Federal installations have no baked-in manifest URL; record the release base
# URL so a future updater can derive it at runtime (self-update is not wired up
# yet, but the marker keeps the install forward-compatible).
if [ "$DISTRIBUTION" = "curl-bash-federal" ]; then
  echo "$BASE_URL" > "$VERSION_DIR/base-url"
fi

# Clean up the legacy distribution marker (pre-0.x) that lived in the
# shared data directory. This avoids stale values from shadowing the
# per-installation marker after an upgrade.
LEGACY_DISTRIBUTION_FILE="$XDG_DATA_HOME/$XDG_DIR_NAMESPACE/$PRODUCT_SUBDIR/distribution"
rm -f "$LEGACY_DISTRIBUTION_FILE"

# Portal installs record the portal-provided identity in the install root so
# the binary can brand itself and reach its portal before any login.
if [ "$DISTRIBUTION" = "enterprise-portal" ]; then
  INSTALL_ROOT="$XDG_DATA_HOME/$XDG_DIR_NAMESPACE/$PRODUCT_SUBDIR"
  echo "$CLI_DISPLAY_NAME" > "$INSTALL_ROOT/display-name"
  echo "$BASE_URL" > "$INSTALL_ROOT/portal-url"
fi

echo -e "${GREEN}✓${NC} Installed $BINARY_NAME v${VERSION} to ${TARGET_BIN/$HOME/\~}.\n"
"$VERSION_DIR/bin/$COMPILED_BIN_NAME" setup
