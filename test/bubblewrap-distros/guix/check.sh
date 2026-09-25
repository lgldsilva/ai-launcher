#!/bin/sh
set +e
guix install bubblewrap
raw=$?
set -e
if [ "$raw" -ne 0 ]; then
  echo STATUS raw=fail
  true
  guix install bubblewrap
else
  echo STATUS raw=ok
fi
command -v bwrap
bwrap --version || true
