#!/bin/sh
set -eu

config_dir=/app/config
defaults_dir=/app/config-defaults

mkdir -p "$config_dir"
config_owner=$(stat -c '%u:%g' "$config_dir")

for name in config.yaml persona.prompt mcp.json; do
	target="$config_dir/$name"
	if [ -e "$target" ]; then
		if [ ! -f "$target" ]; then
			echo "configuration path is not a file: $target" >&2
			exit 1
		fi
		continue
	fi

	cp "$defaults_dir/$name" "$target"
	chown "$config_owner" "$target"
	echo "created configuration file: $target"
done

exec su-exec mumu:mumu "$@"
