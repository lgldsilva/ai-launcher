#!/bin/bash
# Run every distro compose file and record whether the launcher's root argv
# installs bwrap. A non-zero status from one distro does not stop the others.
# TIMEOUT bounds each distro. emerge, guix, yay, and paru get a longer cap.
# PARALLEL is how many compose runs share the machine.
set -u
root=$(CDPATH='' cd -- "$(dirname "$0")" && pwd)
out=${OUT:-/tmp/bubblewrap-distros}
mkdir -p "$out"
echo $$ >"$out/runner.pid"
timeout_s=${TIMEOUT:-600}
parallel=${PARALLEL:-3}

cleanup() {
  local dir name
  for dir in "$root"/*/; do
    name=$(basename "$dir")
    [ -f "$dir/docker-compose.yaml" ] || continue
    docker compose -f "$dir/docker-compose.yaml" -p "bwrap-$name" down --remove-orphans >>"$out/runner.log" 2>&1 || true
  done
}
trap cleanup EXIT

alarm_run() {
  local secs=$1
  shift
  perl - "$secs" "$@" <<'PERL'
use strict;
my $secs = shift @ARGV;
my $pid = fork();
die "fork: $!" unless defined $pid;
if ($pid == 0) {
    setpgrp(0, 0);
    exec @ARGV or die "exec: $!";
}
my $killed = 0;
local $SIG{ALRM} = sub {
    $killed = 1;
    kill "TERM", -$pid;
    sleep 2;
    kill "KILL", -$pid;
};
alarm $secs;
waitpid $pid, 0;
exit 124 if $killed;
exit ($? >> 8);
PERL
}

timeout_for() {
  case $1 in
    emerge | yay | paru | guix) echo 1800 ;;
    *) echo "$timeout_s" ;;
  esac
}

run_one() {
  local name=$1
  local dir=$root/$name
  local log=$out/$name.log
  local secs code=0 status ver result
  secs=$(timeout_for "$name")
  echo "=== $name timeout=${secs}s ===" >>"$out/runner.log"
  alarm_run "$secs" docker compose -f "$dir/docker-compose.yaml" -p "bwrap-$name" run --rm -T check >"$log" 2>&1 || code=$?
  if [ "$code" -eq 124 ]; then
    docker compose -f "$dir/docker-compose.yaml" -p "bwrap-$name" down --remove-orphans >>"$log" 2>&1 || true
    result="$name FAIL timeout"
  elif [ "$code" -ne 0 ]; then
    docker compose -f "$dir/docker-compose.yaml" -p "bwrap-$name" down --remove-orphans >>"$log" 2>&1 || true
    if grep -q '^STATUS raw=ok' "$log"; then
      result="$name FAIL after-raw"
    elif grep -q '^STATUS raw=fail' "$log"; then
      result="$name FAIL raw-and-refresh"
    else
      result="$name FAIL startup"
    fi
  else
    status=$(grep '^STATUS ' "$log" | tail -n 1)
    ver=$(grep -E 'bubblewrap [0-9]|bwrap [0-9]' "$log" | tail -n 1)
    result="$name OK ${status:-no-status} ${ver:-no-version-line}"
  fi
  printf '%s\n' "$result" | tee "$out/$name.result"
}

fifo=$(mktemp -u)
mkfifo "$fifo"
exec 3<>"$fifo"
rm -f "$fifo"
for _ in $(seq 1 "$parallel"); do
  echo >&3
done

names=()
for dir in "$root"/*/; do
  name=$(basename "$dir")
  [ -f "$dir/docker-compose.yaml" ] || continue
  names+=("$name")
done

for name in "${names[@]}"; do
  read -r -u 3
  {
    run_one "$name"
    echo >&3
  } &
done
wait
exec 3>&-

{
  echo "# bubblewrap distro eval"
  for name in "${names[@]}"; do
    if [ -f "$out/$name.result" ]; then
      cat "$out/$name.result"
    else
      echo "$name FAIL missing-result"
    fi
  done
} >"$out/SUMMARY"
echo "DONE $(grep -c . "$out/SUMMARY" | tr -d ' ') lines"
