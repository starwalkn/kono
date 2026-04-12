describe("manager.get", function()
    local manager = require("manager")

    it("loads and caches a script", function()
        local script, err = manager.get("spec/fixtures/continue.lua")
        assert.is_nil(err)
        assert.is_not_nil(script)

        -- Второй вызов должен вернуть из кеша
        local script2, _ = manager.get("spec/fixtures/continue.lua")
        assert.equal(script, script2)
    end)

    it("returns error for missing script", function()
        local script, err = manager.get("spec/fixtures/nonexistent.lua")
        assert.is_nil(script)
        assert.is_not_nil(err)
    end)

    it("returns error if handle() not defined", function()
        local script, err = manager.get("spec/fixtures/no_handle.lua")
        assert.is_nil(script)
        assert.matches("handle%(%) not defined", err)
    end)

    it("does not expose stale cache after error", function()
        local s1, err1 = manager.get("spec/fixtures/nonexistent.lua")
        local s2, err2 = manager.get("spec/fixtures/nonexistent.lua")
        assert.is_nil(s1)
        assert.is_nil(s2)
        assert.is_not_nil(err1)
        assert.is_not_nil(err2)
    end)

    it("returns error for script with runtime error in body", function()
        local script, err = manager.get("spec/fixtures/load_error.lua")
        assert.is_nil(script)
        assert.is_not_nil(err)
    end)
end)

describe("manager.run", function()
    local manager = require("manager")

    it("executes continue script", function()
        local script = manager.get("spec/fixtures/continue.lua")
        local ok, result = manager.run(script.handle, {
            request_id = "test-1",
            method     = "GET",
            path       = "/test",
            headers    = {},
            params     = {},
        })

        assert.is_true(ok)
        assert.equal("continue", result.action)
    end)

    it("handles abort action", function()
        local script = manager.get("spec/fixtures/abort.lua")
        local ok, result = manager.run(script.handle, {
            request_id = "test-2",
            method     = "GET",
            path       = "/test",
            headers    = {},
            params     = {},
        })

        assert.is_true(ok)
        assert.equal("abort", result.action)
        assert.equal(403, result.http_status)
    end)

    it("returns error for invalid response type", function()
        local script = manager.get("spec/fixtures/invalid_response.lua")
        local ok, err = manager.run(script.handle, {
            request_id = "test-3",
            method     = "GET",
            path       = "/test",
            headers    = {},
            params     = {},
        })
        assert.is_false(ok)
        assert.is_not_nil(err)
    end)

    it("returns error when handle() raises", function()
        local script = manager.get("spec/fixtures/runtime_error.lua")
        local ok, err = manager.run(script.handle, {
            request_id = "test-4",
            method     = "GET",
            path       = "/test",
            headers    = {},
            params     = {},
        })
        assert.is_false(ok)
        assert.is_not_nil(err)
    end)

    it("passes request fields to script", function()
        local script = manager.get("spec/fixtures/echo_path.lua")
        local ok, result = manager.run(script.handle, {
            request_id = "test-5",
            method     = "GET",
            path       = "/api/test",
            headers    = { ["X-Foo"] = "bar" },
            params     = { user_id = "42" },
        })
        assert.is_true(ok)
        assert.equal("continue", result.action)
        assert.equal("/api/test", result.path)
        assert.equal("bar", result.headers["X-Foo"])
    end)
end)

describe("sandbox", function()
    local sandbox = require("sandbox")

    it("blocks access to io", function()
        local env = sandbox.new()
        assert.is_nil(env.io)
    end)

    it("blocks access to os", function()
        local env = sandbox.new()
        assert.is_nil(env.os)
    end)

    it("allows cjson", function()
        local env = sandbox.new()
        assert.is_not_nil(env.cjson or env.require and env.require("cjson"))
    end)

    it("allows pcall", function()
        local env = sandbox.new()
        assert.is_not_nil(env.pcall)
    end)

    it("blocks require for non-whitelisted module", function()
        local env = sandbox.new()
        local ok, err = pcall(env.require, "os")
        assert.is_false(ok)
        assert.matches("not allowed", err)
    end)

    it("allows string operations", function()
        local env = sandbox.new()
        assert.is_not_nil(env.string)
        assert.is_not_nil(env.string.upper)
        assert.equal("HELLO", env.string.upper("hello"))
    end)

    it("allows table operations", function()
        local env = sandbox.new()
        local t = { 3, 1, 2 }
        env.table.sort(t)
        assert.equal(1, t[1])
        assert.equal(2, t[2])
        assert.equal(3, t[3])
    end)

    it("allows pcall inside script", function()
        local env = sandbox.new()
        assert.is_not_nil(env.pcall)
        local ok, val = env.pcall(function() return 42 end)
        assert.is_true(ok)
        assert.equal(42, val)
    end)
end)