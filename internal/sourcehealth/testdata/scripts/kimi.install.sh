#!/usr/bin/env bash
#
# Legacy kimi-cli (Python) installer - DEPRECATED.
#
# The next-generation CLI is Kimi Code:
#   curl -fsSL https://code.kimi.com/kimi-code/install.sh | bash
#
# This script still installs the legacy Python kimi-cli, but interactive
# users are offered Kimi Code instead (default after a 30s timeout).
#
# Optional env:
#   KIMI_CLI_FORCE_OLD   Non-empty: skip the prompt and install legacy
#                        kimi-cli (intended for CI / automation).

set -euo pipefail

KIMI_CODE_INSTALL_URL="https://code.kimi.com/kimi-code/install.sh"

install_kimi_code() {
  if command -v curl >/dev/null 2>&1; then
    curl -fsSL "$KIMI_CODE_INSTALL_URL" | bash
    return
  fi

  if command -v wget >/dev/null 2>&1; then
    wget -qO- "$KIMI_CODE_INSTALL_URL" | bash
    return
  fi

  echo "Error: curl or wget is required to install Kimi Code." >&2
  exit 1
}

install_uv() {
  if command -v curl >/dev/null 2>&1; then
    curl -fsSL https://astral.sh/uv/install.sh | sh
    return
  fi

  if command -v wget >/dev/null 2>&1; then
    wget -qO- https://astral.sh/uv/install.sh | sh
    return
  fi

  echo "Error: curl or wget is required to install uv." >&2
  exit 1
}

install_legacy_kimi_cli() {
  if command -v uv >/dev/null 2>&1; then
    UV_BIN="uv"
  else
    install_uv
    UV_BIN="uv"
  fi

  if ! command -v "$UV_BIN" >/dev/null 2>&1; then
    echo "Error: uv not found after installation." >&2
    exit 1
  fi

  "$UV_BIN" tool install --python 3.13 kimi-cli
}

print_deprecation_banner() {
  printf '%s\n' \
    '' \
    '========================================================================' \
    '  [DEPRECATED] The legacy Python kimi-cli is no longer maintained.' \
    '  The next-generation CLI is Kimi Code - please migrate.' \
    '' \
    '  Press Enter to install Kimi Code (auto-installs in 30s);' \
    "  type 'o' + Enter to install the legacy kimi-cli instead;" \
    "  type 'n' + Enter or Ctrl+C to abort." \
    '' \
    '  CI/automation: set KIMI_CLI_FORCE_OLD=1 to skip this prompt.' \
    '========================================================================' \
    >&2
}

# ---------- main ----------

if [ -z "${KIMI_CLI_FORCE_OLD:-}" ]; then
  # stdin is usually a pipe (curl | bash), so prompt via /dev/tty.
  # Without a controlling terminal (CI, Docker, cron): warn, then keep
  # the legacy behavior so automation does not break.
  if (: < /dev/tty) 2>/dev/null; then
    print_deprecation_banner

    # read(1) exit codes cannot distinguish timeout (>128) from EOF (1) on
    # bash 3.2 (stock macOS) - both are 1. Elapsed time can: EOF/Ctrl+D
    # returns instantly, the 30s timeout does not.
    reply=""
    rc=0
    start=$SECONDS
    read -t 30 -r -p '> ' reply < /dev/tty || rc=$?
    if [ "$rc" -ne 0 ]; then
      printf '\n' >&2   # move off the prompt line
      if [ $((SECONDS - start)) -lt 25 ]; then
        # EOF (Ctrl+D) or read error, not a timeout: treat as abort,
        # never as consent.
        echo "==> Aborted; nothing was installed." >&2
        exit 1
      fi
      # otherwise: 30s timeout - fall through to the default below.
    fi
    reply="$(printf '%s' "$reply" | tr -d '[:space:]')"

    case "$reply" in
      o|O)
        echo "==> Installing legacy kimi-cli..." >&2
        ;;
      n|N)
        echo "==> Aborted; nothing was installed." >&2
        exit 1
        ;;
      *)
        echo "==> Installing Kimi Code..." >&2
        install_kimi_code
        exit 0
        ;;
    esac
  else
    printf '%s\n' \
      'Warning: the legacy Python kimi-cli is deprecated; the next-generation CLI is Kimi Code:' \
      '  curl -fsSL https://code.kimi.com/kimi-code/install.sh | bash' \
      'Non-interactive environment detected; continuing with the legacy kimi-cli install.' \
      'Set KIMI_CLI_FORCE_OLD=1 to silence this warning.' \
      '' \
      >&2
  fi
fi

install_legacy_kimi_cli
