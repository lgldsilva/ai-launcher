#!/bin/sh
set +e
opkg install bubblewrap
raw=$?
set -e
if [ "$raw" -ne 0 ]; then
  echo STATUS raw=fail
  opkg update
  opkg install bubblewrap
else
  echo STATUS raw=ok
fi
command -v bwrap
bwrap --version || true
