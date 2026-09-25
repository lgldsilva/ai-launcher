#!/bin/sh
set +e
paru -S --noconfirm bubblewrap
raw=$?
set -e
if [ "$raw" -ne 0 ]; then
  echo STATUS raw=fail
  paru -Sy
  paru -S --noconfirm bubblewrap
else
  echo STATUS raw=ok
fi
command -v bwrap
bwrap --version || true
