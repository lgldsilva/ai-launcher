#!/bin/sh
set +e
yum install -y bubblewrap
raw=$?
set -e
if [ "$raw" -ne 0 ]; then
  echo STATUS raw=fail
  yum makecache
  yum install -y bubblewrap
else
  echo STATUS raw=ok
fi
command -v bwrap
bwrap --version || true
