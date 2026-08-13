#!/bin/sh

set -eu

script_dir=$(
  CDPATH=
  cd -- "$(dirname -- "$0")" && pwd
)
repo_dir=$(
  CDPATH=
  cd -- "$script_dir/.." && pwd
)
tmp_dir=$(mktemp -d)
trap 'rm -rf "$tmp_dir"' EXIT HUP INT TERM

fake_bin="$tmp_dir/bin"
docker_called="$tmp_dir/docker-called"
log_file="$tmp_dir/mutation.log"
report_file="$tmp_dir/gremlins.json"
mkdir -p "$fake_bin"

cat >"$fake_bin/docker" <<'EOF'
#!/bin/sh

touch "$MUTATION_TEST_DOCKER_CALLED"
exit 99
EOF
chmod 755 "$fake_bin/docker"

if MUTATION_TEST_DOCKER_CALLED="$docker_called" \
  PATH="$fake_bin:$PATH" \
  MUTATION_BASE=HEAD \
  MUTATION_OUTPUT="$report_file" \
  "$repo_dir/scripts/mutation.sh" >"$log_file" 2>&1; then
  :
else
  echo "mutation wrapper failed for a non-mutatable diff"
  sed -n '1,120p' "$log_file"
  exit 1
fi

if [ -e "$docker_called" ]; then
  echo "mutation wrapper invoked Docker despite having no eligible Go changes"
  exit 1
fi

if ! grep -Fq 'SKIP: no mutation-eligible Go files changed' "$log_file"; then
  echo "mutation wrapper did not explain the skipped mutation run"
  sed -n '1,120p' "$log_file"
  exit 1
fi

if ! grep -Fq '"status":"skipped"' "$report_file"; then
  echo "mutation wrapper did not write a skipped report"
  sed -n '1,120p' "$report_file"
  exit 1
fi

echo 'PASS: mutation wrapper skips diffs without mutation-eligible Go files'

fixture_dir="$tmp_dir/eligible-repo"
eligible_docker_called="$tmp_dir/eligible-docker-called"
eligible_log_file="$tmp_dir/eligible-mutation.log"
eligible_report_file="$tmp_dir/eligible-gremlins.json"
mkdir -p "$fixture_dir/internal/config" "$fixture_dir/scripts"
cp "$repo_dir/scripts/mutation.sh" "$fixture_dir/scripts/mutation.sh"
chmod 755 "$fixture_dir/scripts/mutation.sh"

git -C "$fixture_dir" init -q
git -C "$fixture_dir" config user.email mutation-test@example.invalid
git -C "$fixture_dir" config user.name mutation-test
printf '%s\n' 'package config' 'func Value() int { return 1 }' >"$fixture_dir/internal/config/value.go"
git -C "$fixture_dir" add internal/config/value.go
git -C "$fixture_dir" -c core.hooksPath=/dev/null commit -qm base
eligible_base=$(git -C "$fixture_dir" rev-parse HEAD)
printf '%s\n' 'package config' 'func Value() int { return 2 }' >"$fixture_dir/internal/config/value.go"
git -C "$fixture_dir" add internal/config/value.go
git -C "$fixture_dir" -c core.hooksPath=/dev/null commit -qm change

if (
  cd "$fixture_dir"
  MUTATION_TEST_DOCKER_CALLED="$eligible_docker_called" \
    PATH="$fake_bin:$PATH" \
    MUTATION_BASE="$eligible_base" \
    MUTATION_OUTPUT="$eligible_report_file" \
    "$fixture_dir/scripts/mutation.sh"
) >"$eligible_log_file" 2>&1; then
  echo "mutation wrapper skipped an eligible Go diff"
  exit 1
fi

if [ ! -e "$eligible_docker_called" ]; then
  echo "mutation wrapper did not invoke Docker for an eligible Go diff"
  sed -n '1,120p' "$eligible_log_file"
  exit 1
fi

echo 'PASS: mutation wrapper runs the mutator for eligible Go changes'
