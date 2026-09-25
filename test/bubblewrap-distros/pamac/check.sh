#!/bin/sh
set +e
pamac install --no-confirm bubblewrap
raw=$?
set -e
if [ "$raw" -ne 0 ]; then
  echo STATUS raw=fail
  pamac update --no-confirm
  pamac install --no-confirm bubblewrap
else
  echo STATUS raw=ok
fi
command -v bwrap
bwrap --version || true
