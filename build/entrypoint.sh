#!/bin/sh
set -e

# Start Lumos in background
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