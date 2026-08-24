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

# A change that only touches _test.go files under internal/ used to look
# eligible, start a real mutation run, and then fail on the 0.00% gremlins
# reports for a diff with nothing to mutate.
test_only_dir="$tmp_dir/test-only-repo"
test_only_docker_called="$tmp_dir/test-only-docker-called"
test_only_log_file="$tmp_dir/test-only-mutation.log"
test_only_report_file="$tmp_dir/test-only-gremlins.json"
mkdir -p "$test_only_dir/internal/config" "$test_only_dir/scripts"
cp "$repo_dir/scripts/mutation.sh" "$test_only_dir/scripts/mutation.sh"
chmod 755 "$test_only_dir/scripts/mutation.sh"

git -C "$test_only_dir" init -q
git -C "$test_only_dir" config user.email mutation-test@example.invalid
git -C "$test_only_dir" config user.name mutation-test
printf '%s\n' 'package config' 'func Value() int { return 1 }' >"$test_only_dir/internal/config/value.go"
printf '%s\n' 'package config' 'import "testing"' 'func TestValue(t *testing.T) { _ = Value() }' \
  >"$test_only_dir/internal/config/value_test.go"
git -C "$test_only_dir" add internal/config
git -C "$test_only_dir" -c core.hooksPath=/dev/null commit -qm base
test_only_base=$(git -C "$test_only_dir" rev-parse HEAD)
printf '%s\n' 'package config' 'import "testing"' 'func TestValue(t *testing.T) { if Value() != 1 { t.Fatal("no") } }' \
  >"$test_only_dir/internal/config/value_test.go"
git -C "$test_only_dir" add internal/config/value_test.go
git -C "$test_only_dir" -c core.hooksPath=/dev/null commit -qm 'test only'

if (
  cd "$test_only_dir"
  MUTATION_TEST_DOCKER_CALLED="$test_only_docker_called" \
    PATH="$fake_bin:$PATH" \
    MUTATION_BASE="$test_only_base" \
    MUTATION_OUTPUT="$test_only_report_file" \
    "$test_only_dir/scripts/mutation.sh"
) >"$test_only_log_file" 2>&1; then
  :
else
  echo "mutation wrapper failed for a diff that only changes test files"
  sed -n '1,120p' "$test_only_log_file"
  exit 1
fi

if [ -e "$test_only_docker_called" ]; then
  echo "mutation wrapper invoked Docker for a diff that only changes test files"
  sed -n '1,120p' "$test_only_log_file"
  exit 1
fi

if ! grep -Fq 'SKIP: no mutation-eligible Go files changed' "$test_only_log_file"; then
  echo "mutation wrapper did not explain the skipped run for a test-only diff"
  sed -n '1,120p' "$test_only_log_file"
  exit 1
fi

echo 'PASS: mutation wrapper skips diffs that only change test files'

# summary_fixture builds a repo with an eligible production change plus a fake
# docker that replays the given gremlins summary, so the threshold enforcement
# can be exercised without running a real mutation pass.
summary_fixture() {
  fixture_name=$1
  summary_body=$2
  summary_dir="$tmp_dir/$fixture_name"
  summary_bin="$summary_dir/bin"
  mkdir -p "$summary_dir/internal/config" "$summary_dir/scripts" "$summary_bin"
  cp "$repo_dir/scripts/mutation.sh" "$summary_dir/scripts/mutation.sh"
  chmod 755 "$summary_dir/scripts/mutation.sh"
  printf '#!/bin/sh\n%s\nexit 0\n' "$summary_body" >"$summary_bin/docker"
  chmod 755 "$summary_bin/docker"

  git -C "$summary_dir" init -q
  git -C "$summary_dir" config user.email mutation-test@example.invalid
  git -C "$summary_dir" config user.name mutation-test
  printf '%s\n' 'package config' 'func Value() int { return 1 }' >"$summary_dir/internal/config/value.go"
  git -C "$summary_dir" add internal/config/value.go
  git -C "$summary_dir" -c core.hooksPath=/dev/null commit -qm base
  summary_base=$(git -C "$summary_dir" rev-parse HEAD)
  printf '%s\n' 'package config' 'func Value() int { return 2 }' >"$summary_dir/internal/config/value.go"
  git -C "$summary_dir" add internal/config/value.go
  git -C "$summary_dir" -c core.hooksPath=/dev/null commit -qm change
}

# A change to production code can still generate no mutants: adding catalog
# entries or other data leaves no conditional, arithmetic or statement for a
# mutator to touch. There is no percentage to judge, so the gate must not read
# the 0.00% gremlins prints as a failure.
summary_fixture no-mutants-repo \
  'printf "Killed: 0, Lived: 0, Not covered: 0\nTest efficacy: 0.00%%\nMutator coverage: 0.00%%\n"'
no_mutants_log="$tmp_dir/no-mutants.log"
if (
  cd "$summary_dir"
  PATH="$summary_bin:$PATH" \
    MUTATION_BASE="$summary_base" \
    MUTATION_OUTPUT="$tmp_dir/no-mutants.json" \
    "$summary_dir/scripts/mutation.sh"
) >"$no_mutants_log" 2>&1; then
  :
else
  echo "mutation wrapper failed a run that generated no mutants"
  sed -n '1,120p' "$no_mutants_log"
  exit 1
fi

if ! grep -Fq 'SKIP: the diff generated no mutants' "$no_mutants_log"; then
  echo "mutation wrapper did not explain the empty mutant sample"
  sed -n '1,120p' "$no_mutants_log"
  exit 1
fi

echo 'PASS: mutation wrapper treats an empty mutant sample as not applicable'

# The empty-sample exemption must not swallow a real miss: mutants that lived
# still fail the gate.
summary_fixture surviving-mutants-repo \
  'printf "Killed: 1, Lived: 9, Not covered: 0\nTest efficacy: 10.00%%\nMutator coverage: 95.00%%\n"'
surviving_log="$tmp_dir/surviving.log"
if (
  cd "$summary_dir"
  PATH="$summary_bin:$PATH" \
    MUTATION_BASE="$summary_base" \
    MUTATION_OUTPUT="$tmp_dir/surviving.json" \
    "$summary_dir/scripts/mutation.sh"
) >"$surviving_log" 2>&1; then
  echo "mutation wrapper passed a run whose mutants survived"
  sed -n '1,120p' "$surviving_log"
  exit 1
fi

if ! grep -Fq 'FAIL: mutation efficacy 10.00% is below' "$surviving_log"; then
  echo "mutation wrapper did not report the efficacy miss"
  sed -n '1,120p' "$surviving_log"
  exit 1
fi

echo 'PASS: mutation wrapper still fails when mutants survive'
