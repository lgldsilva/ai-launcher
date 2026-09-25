#!/bin/sh
set +e
brew install bubblewrap
raw=$?
set -e
if [ "$raw" -ne 0 ]; then
  echo STATUS raw=fail
  brew update
  brew install bubblewrap
else
  echo STATUS raw=ok
fi
command -v bwrap
bwrap --version || true
