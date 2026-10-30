#!/bin/sh
set -eu

exec su-exec mumu:mumu "$@"
