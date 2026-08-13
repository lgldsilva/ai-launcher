#!/bin/sh

set -eu

mutation_image=${MUTATION_IMAGE:-gogremlins/gremlins@sha256:1a026981a9155871ccaae7101e5ff8dc3a62616f6e054ace07336d5b559d5efe}
mutation_base=${MUTATION_BASE:-origin/main}
mutation_efficacy_min=${MUTATION_EFFICACY_MIN:-70}
mutation_coverage_min=${MUTATION_COVERAGE_MIN:-90}
mutation_output=${MUTATION_OUTPUT:-.mutation/gremlins.json}

if ! command -v docker >/dev/null 2>&1; then
  printf '%s\n' 'FAIL: Docker is required for mutation testing; no mutation result was produced.' >&2
  exit 1
fi

if ! git rev-parse --verify "$mutation_base" >/dev/null 2>&1; then
  printf 'FAIL: mutation base %s is not available; fetch the base ref before running mutation testing.\n' "$mutation_base" >&2
  exit 1
fi

mutation_output_dir=$(dirname -- "$mutation_output")
mkdir -p "$mutation_output_dir"

# A diff that only changes workflows, documentation, commands, or other files
# outside the mutation scope has no mutants to generate. Treating that case as
# a zero-percent mutation result turns a non-applicable gate into a failure.
mutation_scope=$(git diff --name-only "$mutation_base"... -- internal | awk '
  /^internal\/.*\.go$/ &&
  $0 !~ /^internal\/(tui|installer|selfupdate|cmd)\// &&
  $0 !~ /^internal\/launcher\/(executor|replace_.*)\.go$/ {
    print
  }
')
if [ -z "$mutation_scope" ]; then
  printf 'SKIP: no mutation-eligible Go files changed relative to %s; mutation thresholds are not applicable.\n' "$mutation_base"
  printf '%s\n' '{"status":"skipped","reason":"no mutation-eligible Go files changed"}' >"$mutation_output"
  exit 0
fi

# Gremlins reports the thresholds but does not fail the run when they are
# missed (verified empirically: 89.00% mcover with a 90 minimum exits 0), so
# this script captures the summary and enforces the minimums itself.
mutation_log=$(mktemp)
git_file=''
cleanup() {
  rm -f "$mutation_log"
  if [ -n "$git_file" ]; then
    rm -f "$git_file"
  fi
}
trap cleanup EXIT HUP INT TERM

git_common_dir=$(git rev-parse --path-format=absolute --git-common-dir)
git_worktree_dir=$(git rev-parse --path-format=absolute --git-dir)
run_mutation() {
  docker run --rm \
    --user "$(id -u):$(id -g)" \
    -e HOME=/tmp/ai-launcher-home \
    -e MUTATION_BASE="$mutation_base" \
    -e MUTATION_EFFICACY_MIN="$mutation_efficacy_min" \
    -e MUTATION_COVERAGE_MIN="$mutation_coverage_min" \
    -e MUTATION_OUTPUT="/app/$mutation_output" \
    -v "$(pwd):/app" \
    "$@" \
    -w /app \
    "$mutation_image" \
    sh -eu -c '
	stub_dir=$(mktemp -d)
	trap '\''rm -rf "$stub_dir"'\'' EXIT
	mkdir -p "$HOME" /tmp/ai-launcher-go-cache /tmp/ai-launcher-go-mod

	for command_name in \
		aider agy ai-jail ai-memory cline claude codex crush cursor-agent \
		devin docker gemini goose grok hermes kiro-cli kimi kilo mimo oc \
		openclaw opencode omp pi podman qwen semidx zero; do
		printf '\''#!/bin/sh\nexit 0\n'\'' > "$stub_dir/$command_name"
		chmod 755 "$stub_dir/$command_name"
	done

	PATH="$stub_dir:$PATH"
	GOCACHE=/tmp/ai-launcher-go-cache
	GOMODCACHE=/tmp/ai-launcher-go-mod
	export PATH GOCACHE GOMODCACHE

	gremlins unleash \
		--diff "$MUTATION_BASE" \
		--coverpkg ./internal/... \
		--exclude-files '\''^(cmd|test|internal/(tui|installer|selfupdate|cmd))/|internal/launcher/(executor|replace_.*)\.go$'\'' \
		--output-statuses lctv \
		--output "$MUTATION_OUTPUT" \
		--threshold-efficacy "$MUTATION_EFFICACY_MIN" \
		--threshold-mcover "$MUTATION_COVERAGE_MIN"
'
}

# enforce_thresholds fails the gate when the gremlins summary misses the
# configured minimums; a run without a summary line is a failure too.
enforce_thresholds() {
  efficacy=$(sed -n 's/^Test efficacy:[[:space:]]*\([0-9.]*\)%.*/\1/p' "$mutation_log")
  mcover=$(sed -n 's/^Mutator coverage:[[:space:]]*\([0-9.]*\)%.*/\1/p' "$mutation_log")
  if [ -z "$efficacy" ] || [ -z "$mcover" ]; then
    printf '%s\n' 'FAIL: mutation run produced no efficacy/coverage summary.' >&2
    return 1
  fi
  if ! awk -v got="$efficacy" -v min="$mutation_efficacy_min" 'BEGIN { exit !(got + 0 >= min + 0) }'; then
    printf 'FAIL: mutation efficacy %s%% is below the %s%% minimum.\n' "$efficacy" "$mutation_efficacy_min" >&2
    return 1
  fi
  if ! awk -v got="$mcover" -v min="$mutation_coverage_min" 'BEGIN { exit !(got + 0 >= min + 0) }'; then
    printf 'FAIL: mutator coverage %s%% is below the %s%% minimum.\n' "$mcover" "$mutation_coverage_min" >&2
    return 1
  fi
  printf 'PASS: mutation efficacy %s%% (min %s%%), mutator coverage %s%% (min %s%%).\n' \
    "$efficacy" "$mutation_efficacy_min" "$mcover" "$mutation_coverage_min"
}

# finish replays the gremlins output, then gates on the real exit status and
# the enforced thresholds.
finish() {
  run_status=$1
  cat "$mutation_log"
  if [ "$run_status" -ne 0 ]; then
    exit "$run_status"
  fi
  enforce_thresholds
}

if [ "$git_worktree_dir" = "$git_common_dir" ]; then
  if run_mutation >"$mutation_log" 2>&1; then
    run_status=0
  else
    run_status=$?
  fi
  finish "$run_status"
  exit $?
fi

git_worktree_name=$(basename -- "$git_worktree_dir")
git_file=$(mktemp)
printf 'gitdir: /git/common/worktrees/%s\n' "$git_worktree_name" >"$git_file"
if run_mutation \
  -v "$git_common_dir:/git/common:ro" \
  -v "$git_file:/app/.git:ro" >"$mutation_log" 2>&1; then
  run_status=0
else
  run_status=$?
fi
finish "$run_status"
