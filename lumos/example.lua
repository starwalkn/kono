function handle(request)
    local user_id = request.params.user_id

    if request.method == "POST" and request.body then
        local body = cjson.decode(request.body)
        if not body.name then
            request.action = "abort"
            request.http_status = 400
            return request
        end
    end

    -- Добавляем заголовок на основе параметра
    request.headers["X-User-Id"] = user_id
    request.action = "continue"

    return request
end
