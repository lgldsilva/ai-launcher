#!/usr/bin/env bash
# Compatibility wrapper — prefer scripts/sonar/sonar-ephemeral.sh
exec "$(cd "$(dirname "$0")" && pwd)/sonar/sonar-ephemeral.sh" "$@"
