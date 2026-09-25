#!/bin/sh
set +e
pacman -S --noconfirm bubblewrap
raw=$?
set -e
if [ "$raw" -ne 0 ]; then
  echo STATUS raw=fail
  pacman -Sy
  pacman -S --noconfirm bubblewrap
else
  echo STATUS raw=ok
fi
command -v bwrap
bwrap --version || true
