#!/bin/sh
set +e
eopkg install -y bubblewrap
raw=$?
set -e
if [ "$raw" -ne 0 ]; then
  echo STATUS raw=fail
  eopkg update-repo
  eopkg install -y bubblewrap
else
  echo STATUS raw=ok
fi
command -v bwrap
bwrap --version || true
