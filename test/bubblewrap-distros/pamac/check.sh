#!/bin/sh
set +e
pamac install --no-confirm bubblewrap
raw=$?
set -e
if [ "$raw" -ne 0 ]; then
  echo STATUS raw=fail
  pamac update --no-confirm
  pamac install --no-confirm bubblewrap
else
  echo STATUS raw=ok
fi
# ai-jail only runs a bwrap owned by root with no group or world write, or
# one in /nix/store; a bwrap that merely runs is not enough.
bin=$(readlink -f "$(command -v bwrap)")
owner=$(stat -c %u "$bin")
mode=$(stat -c %a "$bin")
trusted=no
case $bin in
  /nix/store/*) trusted=yes ;;
  *) if [ "$owner" -eq 0 ] && [ $((0$mode & 022)) -eq 0 ]; then trusted=yes; fi ;;
esac
echo "TRUST path=$bin uid=$owner mode=$mode trusted=$trusted"
[ "$trusted" = yes ]
bwrap --version || true
