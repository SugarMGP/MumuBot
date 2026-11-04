#!/bin/sh
set -eu

mkdir -p /nix/var/nix/daemon-socket
for home in /home/worker /home/worker2 /home/worker3 /home/worker4 /home/worker5; do
  owner=$(basename "$home")
  mkdir -p "$home/.cache" "$home/.tmp"
  chown -R "$owner:worker" "$home"
done

/nix/var/nix/profiles/default/bin/nix-daemon --daemon &
for _ in $(seq 1 50); do
  [ -S /nix/var/nix/daemon-socket/socket ] && break
  sleep 0.1
done

exec /usr/local/bin/worker-exec
