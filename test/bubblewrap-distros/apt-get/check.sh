#!/bin/sh
set +e
env DEBIAN_FRONTEND=noninteractive sh -c 'apt-get update && apt-get install -y bubblewrap'
raw=$?
set -e
if [ "$raw" -ne 0 ]; then
  echo STATUS raw=fail
  apt-get update
  env DEBIAN_FRONTEND=noninteractive sh -c 'apt-get update && apt-get install -y bubblewrap'
else
  echo STATUS raw=ok
fi
command -v bwrap
bwrap --version || true
