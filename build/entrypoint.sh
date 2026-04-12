#!/bin/sh
set -e

LUMOS_WORKERS=${LUMOS_WORKERS:-5}
LUMOS_MAX_MSG=${LUMOS_MAX_MSG:-4194304}

# Start Lumos in background
LUMOS_WORKERS=$LUMOS_WORKERS \
LUMOS_MAX_MSG=$LUMOS_MAX_MSG \
luajit /usr/local/lib/kono/lumos/worker.lua &

LUA_PID=$!

# Forward signals to Lumos
term_handler() {
  echo "Shutting down..."
  kill -TERM "$LUA_PID" 2>/dev/null
  wait "$LUA_PID"
  exit 0
}

trap term_handler INT TERM

# Start Kono in foreground
exec kono "$@"