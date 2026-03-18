function handle(request)
    request.headers["X-Lua-Example"] = "1"
    request.action = "continue"
    
    return request
end