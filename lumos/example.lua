function handle(request)
    local cjson = require('cjson')

    local function replace_key(tbl, target_key, new_value)
        for k, v in pairs(tbl) do
            if k == target_key then
                tbl[k] = new_value
            elseif type(v) == "table" then
                replace_key(v, target_key, new_value)
            end
        end
    end

    if request.body then
        local body_table = cjson.decode(request.body)

        replace_key(body_table, "user_id", 123)

        request.body = cjson.encode(body_table)
    end

    request.headers["X-Lua-Example"] = "1"
    request.action = "continue"
    
    return request
end