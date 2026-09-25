#!/bin/sh
set +e
emerge --ask=n sys-apps/bubblewrap
raw=$?
set -e
if [ "$raw" -ne 0 ]; then
  echo STATUS raw=fail
  emerge-webrsync
  emerge --ask=n sys-apps/bubblewrap
else
  echo STATUS raw=ok
fi
command -v bwrap
bwrap --version || true
