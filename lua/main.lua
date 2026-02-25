local ffi = require("ffi")
local log = require("log")
local cjson = require("cjson")

log.usecolor = false

-- ================= C DEFINITIONS =================

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
    char sun_path[108];
};

/* sigaction */
typedef void (*sighandler_t)(int);

struct sigaction {
    sighandler_t sa_handler;
    unsigned long sa_flags;
    void (*sa_restorer)(void);
    unsigned long sa_mask;
};

int sigaction(int signum, const struct sigaction *act,
              struct sigaction *oldact);

int *__errno_location(void);
]]

-- ================= CONSTANTS =================

local AF_UNIX       = 1
local SOCK_STREAM   = 1
local SIGTERM       = 15
local SIGINT        = 2

local socket_path   = "/tmp/kono-lua.sock"
local WORKERS       = 5

-- Shared shutdown flag.
-- Must be allocated via FFI so it survives fork and is writable
-- from signal handler context.
local shutdown_flag = ffi.new("volatile int[1]", 0)

-- ================= SIGNAL HANDLING =================

-- Signal handler MUST be minimal and async-signal-safe.
-- We only set a flag here.
local function handle_signal(signum)
    shutdown_flag[0] = 1
end

-- Install signal handler WITHOUT SA_RESTART.
-- This ensures syscalls like pause() are interruptible.
local function setup_signal(signum)
    local act = ffi.new("struct sigaction")
    act.sa_handler = handle_signal
    act.sa_flags = 0
    ffi.C.sigaction(signum, act, nil)
end

setup_signal(SIGTERM)
setup_signal(SIGINT)

-- ================= MASTER INITIALIZATION =================

local master_pid = ffi.C.getpid()

-- Create a new process group.
-- All forked workers will inherit this PGID.
assert(ffi.C.setpgid(0, 0) == 0)

-- Remove stale socket file if exists.
ffi.C.unlink(socket_path)

-- Create UNIX domain socket.
local server_fd = ffi.C.socket(AF_UNIX, SOCK_STREAM, 0)
assert(server_fd >= 0, "socket failed")

-- Prepare sockaddr_un structure.
local addr = ffi.new("struct sockaddr_un")
addr.sun_family = AF_UNIX
ffi.copy(addr.sun_path, socket_path)

-- Bind socket.
assert(
    ffi.C.bind(
        server_fd,
        ffi.cast("const struct sockaddr*", addr),
        ffi.sizeof(addr)
    ) == 0,
    "bind failed"
)

-- Start listening.
assert(ffi.C.listen(server_fd, 128) == 0, "listen failed")

log.info("master pid:", master_pid)
log.info("listening on:", socket_path)

-- ================= WORKER LOOP =================

local function worker_loop()
    log.info("worker started:", ffi.C.getpid())

    -- Each worker blocks in accept().
    -- Shutdown is triggered when:
    --   1) master closes listening socket
    --   2) worker receives SIGTERM
    while shutdown_flag[0] == 0 do
        local client_fd = ffi.C.accept(server_fd, nil, nil)

        -- If accept fails and shutdown was requested,
        -- exit the loop.
        if client_fd < 0 then
            if shutdown_flag[0] == 1 then
                break
            end
        else
            -- Handle client request.
            local buf = ffi.new("char[1024]")
            local n = ffi.C.read(client_fd, buf, 1024)

            if n > 0 then
                local req_json = ffi.string(buf, n)
                local request = cjson.decode(req_json)

                log.info("worker accepted request with request_id " .. request.request_id)

                -- === REQUEST MODIFICATION START ===

                -- request looks like:
                --
                -- {
                --     "request_id": "abc123",
                --     "method": "POST",
                --     "path": "/api/user",
                --     "query": "a=1&b=2",
                --     "headers": { ... },
                --     "body": "...",
                --     "client_ip": "10.0.0.1"
                -- }

                request.headers["X-LuaWorker-Modified"] = 1

                -- example of body modifying

                -- if request.body and request.body ~= "" then
                --     local body_table = cjson.decode(request.body)
                --     body_table.modified_by = "luaworker"
                --     request.body = cjson.encode(body_table)
                -- end

                -- === REQUEST MODIFICATION END ===

                local resp_json = cjson.encode(request)

                ffi.C.write(client_fd, resp_json, #resp_json)
                log.info("worker returned repsonse for request with request_id " .. request.request_id)
            end

            ffi.C.close(client_fd)
        end
    end

    log.warn("worker", ffi.C.getpid(), "exiting")

    -- Close inherited listening socket.
    ffi.C.close(server_fd)

    os.exit(0)
end

-- ================= FORK WORKERS =================

local worker_pids = {}

for i = 1, WORKERS do
    local pid = ffi.C.fork()

    if pid == 0 then
        worker_loop()
    else
        table.insert(worker_pids, pid)
    end
end

-- ================= MASTER WAIT LOOP =================

-- Master sleeps until signal arrives.
while shutdown_flag[0] == 0 do
    ffi.C.pause()
end

log.warn("master shutting down")

-- IMPORTANT:
-- Close listening socket FIRST.
-- This forces accept() in workers to return with error
-- on macOS (BSD kernel behavior).
ffi.C.close(server_fd)

-- Send SIGTERM to entire process group.
ffi.C.kill(-master_pid, SIGTERM)

-- Reap all worker processes (prevent zombies).
while ffi.C.wait(nil) > 0 do end

-- Remove socket file.
ffi.C.unlink(socket_path)

log.info("master exited")
