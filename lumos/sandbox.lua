local cjson = require('cjson')

local M = {}

local base_env = {
    pairs    = pairs,
    ipairs   = ipairs,
    next     = next,

    tostring = tostring,
    tonumber = tonumber,
    type     = type,

    select   = select,
    unpack   = unpack,
    rawget   = rawget,
    rawset   = rawset,
    rawequal = rawequal,
    pcall    = pcall,
    error    = error,

    string   = {
        sub    = string.sub,
        find   = string.find,
        match  = string.match,
        gsub   = string.gsub,
        lower  = string.lower,
        upper  = string.upper,
        format = string.format,
        rep    = string.rep,
        len    = string.len,
        byte   = string.byte,
        char   = string.char,
    },

    table    = {
        insert = table.insert,
        remove = table.remove,
        sort = table.sort,
        concat = table.concat,
        unpack = table.unpack,
    },

    math     = math,
    cjson    = cjson,

    require  = function(mod)
        local allowed = { cjson = cjson }
        if not allowed[mod] then
            error("module not allowed: " .. tostring(mod))
        end

        return allowed[mod]
    end
}

function M.new()
    local env = {}

    setmetatable(env, {
        __index = base_env,
    })

    return env
end

return M
