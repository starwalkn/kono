local cjson = require('cjson')

local M = {}

local base_env = {
    pairs = pairs,
    ipairs = ipairs,
    next = next,

    tostring = tostring,
    tonumber = tonumber,
    type = type,

    string = {
        sub = string.sub,
        find = string.find,
        match = string.match,
        gsub = string.gsub,
        lower = string.lower,
        upper = string.upper,
    },

    table = {
        insert = table.insert,
        remove = table.remove,
        sort = table.sort,
    },

    math = math,
    cjson = cjson,
}

function M.new()
    local env = {}

    setmetatable(env, {
        __index = base_env,
    })

    return env
end

return M