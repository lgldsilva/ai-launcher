#!/bin/sh
set +e
urpmi --auto bubblewrap
raw=$?
set -e
if [ "$raw" -ne 0 ]; then
  echo STATUS raw=fail
  urpmi.update -a
  urpmi --auto bubblewrap
else
  echo STATUS raw=ok
fi
command -v bwrap
bwrap --version || true
