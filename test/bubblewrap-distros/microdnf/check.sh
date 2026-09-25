#!/bin/sh
set +e
microdnf install -y bubblewrap
raw=$?
set -e
if [ "$raw" -ne 0 ]; then
  echo STATUS raw=fail
  microdnf makecache
  microdnf install -y bubblewrap
else
  echo STATUS raw=ok
fi
command -v bwrap
bwrap --version || true
