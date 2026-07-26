#!/bin/sh
# ai-launcher installer — downloads the right release archive for your OS/arch
# from the GitHub releases, verifies its SHA-256 against checksums.txt
# (mandatory), and installs the binary. No sudo: the default destination is
# ~/.local/bin.
#
#   curl -fsSL https://raw.githubusercontent.com/lgldsilva/ai-launcher/main/install.sh | sh
#   ./install.sh                        # install latest for this OS/arch
#   ./install.sh --version v0.2.0       # a specific release
#   ./install.sh --bin-dir /opt/bin     # install somewhere else
#
# Env overrides: AI_LAUNCHER_API, AI_LAUNCHER_DOWNLOAD_BASE,
# AI_LAUNCHER_INSTALL_DIR, AI_LAUNCHER_UPDATE_TOKEN (or GITHUB_TOKEN).
#
# Default path uses public release download URLs. When those fail and a token
# is set, assets are fetched via the GitHub assets API (private forks/mirrors).
# The token is never printed.
set -eu

API="${AI_LAUNCHER_API:-https://api.github.com/repos/lgldsilva/ai-launcher}"
DL_BASE="${AI_LAUNCHER_DOWNLOAD_BASE:-https://github.com/lgldsilva/ai-launcher/releases/download}"
BIN_DIR="${AI_LAUNCHER_INSTALL_DIR:-$HOME/.local/bin}"
TOKEN="${AI_LAUNCHER_UPDATE_TOKEN:-${GITHUB_TOKEN:-}}"
VERSION=""

die() { echo "install: $*" >&2; exit 1; }

while [ $# -gt 0 ]; do
  case "$1" in
    --version) VERSION="${2:?--version requires a value}"; shift 2 ;;
    --bin-dir) BIN_DIR="${2:?--bin-dir requires a value}"; shift 2 ;;
    -h|--help) sed -n '2,17p' "$0"; exit 0 ;;
    *) die "unknown flag: $1" ;;
  esac
done

command -v curl >/dev/null 2>&1 || die "curl is required"

# curl_auth URL ACCEPT OUTFILE — optional Bearer auth; never logs TOKEN.
curl_auth() {
  _url="$1"
  _accept="$2"
  _out="$3"
  if [ -n "$TOKEN" ]; then
    curl -fsSL \
      -H "Authorization: Bearer ${TOKEN}" \
      -H "Accept: ${_accept}" \
      -H "User-Agent: ai-launcher-install" \
      -o "$_out" "$_url"
  else
    curl -fsSL \
      -H "Accept: ${_accept}" \
      -H "User-Agent: ai-launcher-install" \
      -o "$_out" "$_url"
  fi
}

detect_os() {
  case "$(uname -s | tr '[:upper:]' '[:lower:]')" in
    linux) echo linux ;;
    darwin) echo darwin ;;
    *) die "unsupported OS: $(uname -s) (on Windows, download the .zip from the releases page instead)" ;;
  esac
}
detect_arch() {
  case "$(uname -m)" in
    x86_64|amd64) echo amd64 ;;
    aarch64|arm64) echo arm64 ;;
    *) die "unsupported arch: $(uname -m) (only amd64 and arm64 releases are published)" ;;
  esac
}

# Pick the highest semver tag from a JSON release list on stdin. Drafts never
# appear in unauthenticated API responses; prerelease-style tags (with a
# suffix, e.g. v0.2.0-rc.1) are skipped unless nothing else remains.
pick_highest() {
  tags="$(grep -o '"tag_name":"[^"]*"' | cut -d'"' -f4 || true)"
  # also handle pretty-printed "tag_name": "v1.2.3"
  if [ -z "$tags" ]; then
    tags="$(grep -o '"tag_name"[[:space:]]*:[[:space:]]*"[^"]*"' | sed 's/.*"\([^"]*\)"$/\1/' || true)"
  fi
  stable="$(echo "$tags" | grep -E '^v?[0-9]+\.[0-9]+\.[0-9]+$' || true)"
  if [ -z "$stable" ]; then
    stable="$(echo "$tags" | grep -E '^v?[0-9]+\.[0-9]+\.[0-9]+' || true)"
  fi
  [ -n "$stable" ] || return 1
  echo "$stable" | awk '{ v=$0; sub(/^v/, "", v); n=split(v, a, ".");
    printf "%010d %010d %010d %s\n", a[1], a[2], a[3], $0 }' \
    | sort | tail -1 | awk '{ print $4 }'
}

# asset_api_url NAME RELEASE_JSON — print the API URL for the named asset.
# Works on both pretty-printed and compact JSON (User-Agent may force compact).
# Splits on '{' so each object is scanned independently.
asset_api_url() {
  want="$1"
  rel_json="$2"
  tr '{' '\n' <"$rel_json" | awk -v want="$want" '
    {
      url = ""
      name = ""
      if (match($0, /https:\/\/api\.github\.com\/repos\/[^"]+\/assets\/[0-9]+/)) {
        url = substr($0, RSTART, RLENGTH)
      }
      if (match($0, /"name"[[:space:]]*:[[:space:]]*"[^"]+"/)) {
        name = substr($0, RSTART, RLENGTH)
        sub(/.*"name"[[:space:]]*:[[:space:]]*"/, "", name)
        sub(/".*/, "", name)
      }
      if (url != "" && name == want) {
        print url
        exit
      }
    }
  '
}

# download_asset NAME DEST — public URL first; assets API if TOKEN and public fails.
download_asset() {
  _name="$1"
  _dest="$2"
  if curl -fsSL -H "User-Agent: ai-launcher-install" -o "$_dest" "$BASE/$_name" 2>/dev/null; then
    return 0
  fi
  [ -n "$TOKEN" ] || die "download failed: $BASE/$_name (set AI_LAUNCHER_UPDATE_TOKEN or GITHUB_TOKEN for private releases)"
  _rel="$WORK/release.json"
  curl_auth "$API/releases/tags/$VERSION" "application/vnd.github+json" "$_rel" \
    || die "could not fetch release $VERSION via API (check AI_LAUNCHER_UPDATE_TOKEN / GITHUB_TOKEN)"
  _url="$(asset_api_url "$_name" "$_rel")"
  [ -n "$_url" ] || die "asset $_name not found in release $VERSION"
  curl_auth "$_url" "application/octet-stream" "$_dest" \
    || die "download failed via assets API: $_name"
}

# Resolve the version from the latest release when not pinned. Some release
# hosts return 404 on /releases/latest, so fall back to the release list.
if [ -z "$VERSION" ]; then
  _tmp="$(mktemp)"
  if curl_auth "$API/releases/latest" "application/vnd.github+json" "$_tmp" 2>/dev/null; then
    VERSION="$(grep -o '"tag_name"[[:space:]]*:[[:space:]]*"[^"]*"' "$_tmp" | head -1 | sed 's/.*"\([^"]*\)"$/\1/' || true)"
  fi
  if [ -z "$VERSION" ]; then
    if curl_auth "$API/releases?per_page=50" "application/vnd.github+json" "$_tmp" 2>/dev/null; then
      VERSION="$(pick_highest <"$_tmp" || true)"
    fi
  fi
  rm -f "$_tmp"
  [ -n "$VERSION" ] || die "could not resolve the latest release from $API"
fi
VER_NOV="${VERSION#v}"

OS="$(detect_os)"
ARCH="$(detect_arch)"
EXT=tar.gz
[ "$OS" = windows ] && EXT=zip
ARCHIVE="ai-launcher_${VER_NOV}_${OS}_${ARCH}.${EXT}"
BASE="$DL_BASE/$VERSION"

WORK="$(mktemp -d)"; trap 'rm -rf "$WORK"' EXIT
echo "Fetching $ARCHIVE ($VERSION) ..."
download_asset "$ARCHIVE" "$WORK/$ARCHIVE"

# Verify SHA-256 against the release checksums.txt. Verification is mandatory:
# this script installs the executable you will run, so an unverifiable
# download is a hard error.
download_asset "checksums.txt" "$WORK/checksums.txt" \
  || die "checksums.txt not found — refusing to install an unverified binary"
want="$(grep " $ARCHIVE\$" "$WORK/checksums.txt" | awk '{print $1}')"
[ -n "$want" ] || die "no checksum entry for $ARCHIVE in checksums.txt"
if command -v sha256sum >/dev/null 2>&1; then
  got="$(sha256sum "$WORK/$ARCHIVE" | awk '{print $1}')"
else
  command -v shasum >/dev/null 2>&1 || die "sha256sum or shasum is required"
  got="$(shasum -a 256 "$WORK/$ARCHIVE" | awk '{print $1}')"
fi
[ "$want" = "$got" ] || die "checksum mismatch for $ARCHIVE"
echo "Checksum OK."

# Extract and install the binary.
tar -xzf "$WORK/$ARCHIVE" -C "$WORK"
BIN="$WORK/ai-launcher"
[ -f "$BIN" ] || die "binary not found inside $ARCHIVE"

mkdir -p "$BIN_DIR"
install -m 0755 "$BIN" "$BIN_DIR/ai-launcher" 2>/dev/null \
  || { cp "$BIN" "$BIN_DIR/ai-launcher" && chmod 0755 "$BIN_DIR/ai-launcher"; } \
  || die "could not write to $BIN_DIR — set AI_LAUNCHER_INSTALL_DIR to a user-writable directory"
echo "Installed ai-launcher $VERSION to $BIN_DIR/ai-launcher"
case ":$PATH:" in
  *":$BIN_DIR:"*) ;;
  *) echo "note: add $BIN_DIR to your PATH, e.g. export PATH=\"$BIN_DIR:\$PATH\"" ;;
esac
