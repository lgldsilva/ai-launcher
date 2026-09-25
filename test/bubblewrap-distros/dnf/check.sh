#!/bin/sh
set +e
dnf install -y bubblewrap
raw=$?
set -e
if [ "$raw" -ne 0 ]; then
  echo STATUS raw=fail
  dnf makecache
  dnf install -y bubblewrap
else
  echo STATUS raw=ok
fi
command -v bwrap
bwrap --version || true
