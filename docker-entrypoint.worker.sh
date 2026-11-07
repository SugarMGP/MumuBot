#!/bin/sh
set -eu

mkdir -p /nix/var/nix/daemon-socket
mkdir -p /home/worker/.cache /home/worker/.tmp
chown -R worker:worker /home/worker

NIX_REMOTE=local /nix/var/nix/profiles/default/bin/nix-daemon --daemon &
for _ in $(seq 1 50); do
  [ -S /nix/var/nix/daemon-socket/socket ] && break
  sleep 0.1
done

exec sudo -n -u worker -- /usr/local/bin/worker-exec
