#!/bin/sh
set +e
xbps-install -S -y bubblewrap
raw=$?
set -e
if [ "$raw" -ne 0 ]; then
  echo STATUS raw=fail
  xbps-install -S
  xbps-install -S -y bubblewrap
else
  echo STATUS raw=ok
fi
command -v bwrap
bwrap --version || true
