#!/bin/sh
set +e
slackpkg install bubblewrap
raw=$?
set -e
if [ "$raw" -ne 0 ]; then
  echo STATUS raw=fail
  slackpkg update
  slackpkg install bubblewrap
else
  echo STATUS raw=ok
fi
command -v bwrap
bwrap --version || true
