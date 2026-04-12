local ffi     = require("ffi")
local log     = require("log")
local cjson   = require("cjson")
local manager = require("manager")

log.usecolor = false

-- =============================================================
-- C DEFINITIONS
-- =============================================================

ffi.cdef [[
int socket(int domain, int type, int protocol);
int bind(int sockfd, const struct sockaddr *addr, unsigned int addrlen);
int listen(int sockfd, int backlog);
int accept(int sockfd, struct sockaddr *addr, unsigned int *addrlen);
int read(int fd, void *buf, int count);
int write(int fd, const void *buf, int count);
int close(int fd);
int unlink(const char *pathname);

int fork(void);
int getpid(void);
int setpgid(int pid, int pgid);
int kill(int pid, int sig);
int wait(int *status);
int pause(void);

typedef unsigned short sa_family_t;

struct sockaddr_un {
    sa_family_t sun_family;
    char        sun_path[108];
};

typedef void (*sighandler_t)(int);

struct sigaction {
    sighandler_t  sa_handler;
    unsigned long sa_flags;
    void        (*sa_restorer)(void);
    unsigned long sa_mask;
};

int sigaction(int signum, const struct sigaction *act,
              struct sigaction *oldact);
]]

-- =============================================================
-- CONSTANTS
-- =============================================================

local AF_UNIX     = 1
local SOCK_STREAM = 1
local SIGTERM     = 15
local SIGINT      = 2

local SOCKET_PATH = "/tmp/lumos.sock"
local WORKERS     = tonumber(os.getenv("LUMOS_WORKERS")) or 5
local MAX_MSG     = tonumber(os.getenv("LUMOS_MAX_MSG")) or 4 * 1024 * 1024

-- Shared shutdown flag — FFI-allocated so it survives fork
-- and is writable from signal handler context.
local shutdown_flag = ffi.new("volatile int[1]", 0)

-- =============================================================
-- SIGNAL HANDLING
-- =============================================================

-- Signal handler MUST be async-signal-safe — only set a flag.
local function handle_signal()
    shutdown_flag[0] = 1
end

-- Install handler WITHOUT SA_RESTART so pause() is interruptible.
local function setup_signal(signum)
    local act = ffi.new("struct sigaction")
    act.sa_handler = handle_signal
    act.sa_flags   = 0
    ffi.C.sigaction(signum, act, nil)
end

setup_signal(SIGTERM)
setup_signal(SIGINT)

-- =============================================================
-- LENGTH-PREFIX PROTOCOL
--
-- Wire format (both directions):
--   [4 bytes: uint32 big-endian message length][N bytes: JSON]
-- =============================================================

-- Read exactly n bytes from fd into a new buffer.
-- Returns ffi buffer + byte count, or nil on error/EOF.
local function read_exactly(fd, n)
    local buf  = ffi.new("char[?]", n)
    local done = 0

    while done < n do
        local got = ffi.C.read(fd, buf + done, n - done)
        if got <= 0 then
            return nil
        end
        done = done + got
    end

    return buf, done
end

-- Read a length-prefixed message from fd.
-- Returns JSON string or nil + error string.
local function recv_message(fd)
    -- Read 4-byte big-endian length prefix.
    local hdr, _ = read_exactly(fd, 4)
    if not hdr then
        return nil, "failed to read length prefix"
    end

    -- Decode big-endian uint32.
    local len = hdr[0] * 16777216
              + hdr[1] * 65536
              + hdr[2] * 256
              + hdr[3]

    if len == 0 then
        return nil, "zero-length message"
    end

    if len > MAX_MSG then
        return nil, string.format("message too large: %d bytes (limit %d)", len, MAX_MSG)
    end

    -- Read exactly len bytes of JSON payload.
    local body, _ = read_exactly(fd, len)
    if not body then
        return nil, "failed to read message body"
    end

    return ffi.string(body, len)
end

-- Write a length-prefixed message to fd.
-- Returns true or nil + error string.
local function send_message(fd, json_str)
    local len = #json_str

    -- Encode big-endian uint32 length prefix.
    local hdr = ffi.new("uint8_t[4]")
    hdr[0] = math.floor(len / 16777216) % 256
    hdr[1] = math.floor(len / 65536)    % 256
    hdr[2] = math.floor(len / 256)      % 256
    hdr[3] = len                         % 256

    local hw = ffi.C.write(fd, hdr, 4)
    if hw ~= 4 then
        return nil, "failed to write length prefix"
    end

    local bw = ffi.C.write(fd, json_str, len)
    if bw ~= len then
        return nil, "failed to write message body"
    end

    return true
end

-- =============================================================
-- REQUEST PROCESSING
-- =============================================================

local function abort_response(request_id, status, err)
    return {
        request_id  = request_id or "",
        action      = "abort",
        http_status = status,
        error       = tostring(err),
    }
end

local function process_request(request)
    local script, err = manager.get(request.script_path)
    if not script then
        return abort_response(request.request_id, 500, err)
    end

    local ok, result = manager.run(script.handle, request)
    if not ok then
        return abort_response(request.request_id, 500, result)
    end

    if type(result) ~= "table" or not result.action then
        return abort_response(request.request_id, 500, "invalid script response")
    end

    return result
end

-- Handle a single client connection: recv → process → send.
local function handle_client(client_fd)
    local json_in, err = recv_message(client_fd)
    if not json_in then
        log.error("recv error:", err)
        return
    end

    local ok, request = pcall(cjson.decode, json_in)
    if not ok then
        log.error("json decode error:", request)
        local resp = abort_response("", 400, "invalid JSON")
        send_message(client_fd, cjson.encode(resp))
        return
    end

    log.info("request:", request.request_id)

    local response  = process_request(request)
    local json_out  = cjson.encode(response)

    local sent, serr = send_message(client_fd, json_out)
    if not sent then
        log.error("send error:", serr)
    end
end

-- =============================================================
-- WORKER LOOP
-- =============================================================

local function worker_loop()
    log.info("worker started:", ffi.C.getpid())

    while shutdown_flag[0] == 0 do
        local client_fd = ffi.C.accept(server_fd, nil, nil)

        if client_fd < 0 then
            -- accept() interrupted — check shutdown flag.
            if shutdown_flag[0] == 1 then
                break
            end
        else
            handle_client(client_fd)
            ffi.C.close(client_fd)
        end
    end

    log.warn("worker", ffi.C.getpid(), "exiting")
    ffi.C.close(server_fd)
    os.exit(0)
end

-- =============================================================
-- MASTER: SOCKET SETUP
-- =============================================================

local master_pid = ffi.C.getpid()

-- Create a new process group so we can signal all workers at once.
assert(ffi.C.setpgid(0, 0) == 0, "setpgid failed")

-- Remove stale socket file.
ffi.C.unlink(SOCKET_PATH)

-- Create and bind Unix domain socket.
server_fd = ffi.C.socket(AF_UNIX, SOCK_STREAM, 0)
assert(server_fd >= 0, "socket failed")

local addr = ffi.new("struct sockaddr_un")
addr.sun_family = AF_UNIX
ffi.copy(addr.sun_path, SOCKET_PATH)

assert(
    ffi.C.bind(server_fd, ffi.cast("const struct sockaddr*", addr), ffi.sizeof(addr)) == 0,
    "bind failed"
)
assert(ffi.C.listen(server_fd, 128) == 0, "listen failed")

log.info("master pid:", master_pid)
log.info("listening on:", SOCKET_PATH)

-- =============================================================
-- MASTER: FORK WORKERS
-- =============================================================

local worker_pids = {}

for _ = 1, WORKERS do
    local pid = ffi.C.fork()

    if pid == 0 then
        -- Child — enter worker loop (never returns).
        worker_loop()
    elseif pid > 0 then
        table.insert(worker_pids, pid)
    else
        log.error("fork failed")
    end
end

-- =============================================================
-- MASTER: WAIT FOR SHUTDOWN
-- =============================================================

while shutdown_flag[0] == 0 do
    ffi.C.pause()
end

log.warn("master shutting down")

-- Close listening socket first — forces accept() in workers
-- to return with an error (BSD/macOS kernel behavior).
ffi.C.close(server_fd)

-- Signal all workers via process group.
ffi.C.kill(-master_pid, SIGTERM)

-- Reap all worker processes to prevent zombies.
while ffi.C.wait(nil) > 0 do end

ffi.C.unlink(SOCKET_PATH)

log.info("master exited")