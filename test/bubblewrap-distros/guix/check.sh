#!/bin/sh
set +e
guix install bubblewrap
raw=$?
set -e
if [ "$raw" -ne 0 ]; then
  echo STATUS raw=fail
  true
  guix install bubblewrap
else
  echo STATUS raw=ok
fi
# guix install puts bwrap in the user profile; a login shell would add it.
PATH="$HOME/.guix-profile/bin:$PATH"
command -v bwrap
bwrap --version || true
