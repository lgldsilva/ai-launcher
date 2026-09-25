#!/bin/sh
set +e
zypper --non-interactive install bubblewrap
raw=$?
set -e
if [ "$raw" -ne 0 ]; then
  echo STATUS raw=fail
  zypper --non-interactive refresh
  zypper --non-interactive install bubblewrap
else
  echo STATUS raw=ok
fi
command -v bwrap
bwrap --version || true
