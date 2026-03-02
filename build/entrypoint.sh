#!/bin/sh
set -e

# Start LuaWorker in background
luajit /usr/local/lib/kono/luaworker/main.lua &

LUA_PID=$!

# Forward signals to LuaWorker
term_handler() {
  echo "Shutting down..."
  kill -TERM "$LUA_PID" 2>/dev/null
  wait "$LUA_PID"
  exit 0
}

trap term_handler INT TERM

# Start Kono in foreground
exec kono "$@"