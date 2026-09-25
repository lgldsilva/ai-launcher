#!/bin/sh
set +e
nix --extra-experimental-features 'nix-command flakes' profile install nixpkgs#bubblewrap
raw=$?
set -e
if [ "$raw" -ne 0 ]; then
  echo STATUS raw=fail
  true
  nix --extra-experimental-features 'nix-command flakes' profile install nixpkgs#bubblewrap
else
  echo STATUS raw=ok
fi
command -v bwrap
bwrap --version || true
