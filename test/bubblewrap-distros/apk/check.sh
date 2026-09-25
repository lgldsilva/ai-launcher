#!/bin/sh
set +e
apk add --no-interactive bubblewrap
raw=$?
set -e
if [ "$raw" -ne 0 ]; then
  echo STATUS raw=fail
  apk update
  apk add --no-interactive bubblewrap
else
  echo STATUS raw=ok
fi
command -v bwrap
bwrap --version || true
