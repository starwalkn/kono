function handle(request)
    request.action = "abort"
    request.http_status = 403
    return request
end