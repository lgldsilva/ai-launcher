#!/bin/sh
set +e
yay -S --noconfirm bubblewrap
raw=$?
set -e
if [ "$raw" -ne 0 ]; then
  echo STATUS raw=fail
  yay -Sy
  yay -S --noconfirm bubblewrap
else
  echo STATUS raw=ok
fi
command -v bwrap
bwrap --version || true
