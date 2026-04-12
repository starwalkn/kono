local sandbox = require('sandbox')

local M = {}
local cache = {}

function M.get(path)
    if not cache[path] then
        local script, err = M.load(path)
        if not script then
            return nil, err
        end

        cache[path] = script
    end

    return cache[path], nil
end

function M.load(path)
    local chunk, err = loadfile(path)
    if not chunk then
        return nil, err
    end

    local env = sandbox.new()
    setfenv(chunk, env)

    local ok, err = pcall(chunk)
    if not ok then
        return nil, err
    end

    if type(env.handle) ~= "function" then
        return nil, "handle() not defined"
    end

    return {
        handle = env.handle,
        env = env
    }
end

function M.run(func, request)
    local ok, res = pcall(func, request)
    if not ok then
        return false, res
    end

    if type(res) ~= "table" or not res.action then
        return false, "invalid script response"
    end

    return true, res
end

return M
