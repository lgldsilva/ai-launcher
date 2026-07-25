#!/usr/bin/env bash
# Compatibility wrapper — prefer scripts/sonar/publish-sonar-pr-artifacts.sh
exec "$(cd "$(dirname "$0")" && pwd)/sonar/publish-sonar-pr-artifacts.sh" "$@"
