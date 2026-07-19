# Telamon — Specification

## Overview

Telamon is a lightweight HTTP server written in Go that uses Lua (`.lua`) files as its scripting layer. Each URL maps directly to a Lua script, making routing file-based and zero-config. The Lua VM runs unsandboxed by default, giving scripts full access to the Go standard library bridges and all Lua standard libraries.

---

## Architecture

```
[HTTP Request]
      │
      ▼
  net/http mux  (Go)
      │
      ├─ resolveLuaScript()  →  scripts/<path>.lua
      │                      →  scripts/<path>/index.lua
      │
      ├─ Static fallback     →  static/<path>
      │
      └─ 404 Not Found
            │
            ▼
      executeLua()
        ├─ Creates gopher-lua LState
        ├─ Injects globals: request, response, json, telamon
        ├─ Overrides print() → response buffer
        ├─ DoFile(scriptPath)
        └─ Flushes buffer → http.ResponseWriter
```

---

## Configuration — `config.toml`

Default path: `/etc/telamon/config.toml` (when run as a systemd service).  
Override with: `--config <path>`

```toml
[server]
port        = 80          # TCP port to listen on
host        = "0.0.0.0"  # Bind address
scripts_dir = "scripts"   # Root directory for .lua route scripts
static_dir  = "static"    # Root directory for static file serving

[lua]
unsandboxed = true        # true = full stdlib; false = safe subset only
```

### `lua.unsandboxed`

| Value | Available Lua libraries |
|-------|------------------------|
| `true` | base, package, table, io, os, string, math, debug, channel |
| `false` | base, package, table, string, math |

---

## URL → Script Resolution

Given a request for `GET /foo/bar`:

1. Check `scripts/foo/bar.lua` — serve if found.
2. Check `scripts/foo/bar/index.lua` — serve if found.
3. Check `static/foo/bar` — serve file if found.
4. Return `404 Not Found`.

Root path `/` resolves to `scripts/index.lua`.

Path traversal is blocked: resolved paths must stay inside `scripts_dir`.

---

## Lua Globals

Every script receives the following globals injected before execution.

### `request`

| Field / Method | Type | Description |
|---|---|---|
| `request.method` | string | HTTP verb — `"GET"`, `"POST"`, etc. |
| `request.path` | string | URL path — e.g. `"/api/hello"` |
| `request.query` | string | Raw query string — e.g. `"name=World"` |
| `request.host` | string | Host header value |
| `request.remote_addr` | string | Client IP and port |
| `request.body` | string | Raw request body |
| `request.headers["key"]` | string | Lowercase header name lookup |
| `request.params["key"]` | string | Query-string value lookup |
| `request:getParam("key")` | string | Method form of params lookup |

### `response`

| Method | Description |
|---|---|
| `response:write(str)` | Append `str` to the response body |
| `response:writeln(str)` | Append `str` + newline to the response body |
| `response:setStatus(code)` | Set HTTP status code (default: `200`) |
| `response:setHeader(key, value)` | Set a response header |
| `response:json(value)` | JSON-encode `value`, write to body, set `Content-Type: application/json` |
| `response:redirect(url [, code])` | Send HTTP redirect (default code: `302`) |

Default `Content-Type` is `text/html; charset=utf-8` unless overridden.

### `json`

| Function | Description |
|---|---|
| `json.encode(value)` | Encode Lua value → JSON string |
| `json.decode(str)` | Decode JSON string → Lua table / value |

Lua tables with consecutive integer keys from `1` are encoded as JSON arrays.  
All other tables are encoded as JSON objects.

### `telamon`

| Field / Method | Description |
|---|---|
| `telamon.version` | Server version string (e.g. `"1.0.0"`) |
| `telamon.log(...)` | Print to server stdout / journal — **not** to the HTTP response |

### `ldb` (LevelDB)

Provides embedded Key-Value storage via LevelDB.

| Method | Description |
|---|---|
| `ldb.create(path)` | Opens a database at `path` and returns a `db` object |
| `ldb.execute("cmd", db)` | Executes a simple command (e.g. `"PUT key val"`, `"GET key"`, `"DEL key"`) on the `db` object |
| `db:put(key, val)` | Saves string `val` to `key` |
| `db:get(key)` | Returns the value of `key` as a string, or `nil` if not found |
| `db:delete(key)` | Deletes `key` from the database |
| `db:close()` | Closes the database connection |

Example:
```lua
local db = ldb:create("./public/data.db")
db:put("user", "alice")
-- or using execute:
ldb:execute("PUT role admin", db)

local user = ldb:execute("GET user", db)
db:close()
```

### `print`

`print(...)` is overridden to write tab-separated values followed by a newline to the HTTP response body (same as `response:writeln`).

---

## Error Handling

| Situation | Behaviour |
|---|---|
| Lua syntax / runtime error | `500 Internal Server Error` with error text in body; error logged to journal |
| Script not found | Falls through to static files, then `404 Not Found` |
| `response:redirect()` called | Redirect is sent immediately; any buffered body is discarded |

---

## Systemd Service

**Unit file:** `/etc/systemd/system/telamon.service`  
**Binary:** `/usr/local/bin/telamon`  
**Config:** `/etc/telamon/config.toml`  
**Working directory:** `/etc/telamon`

The server reads `scripts_dir` and `static_dir` **relative to `WorkingDirectory`**, so set those in `config.toml` as relative paths (e.g. `scripts_dir = "scripts"`) and place the script tree under `/etc/telamon/scripts/`.

```
/etc/telamon/
├── config.toml
├── scripts/
│   ├── index.lua
│   └── api/
│       └── hello.lua
└── static/
    └── ...
```

### Install

```bash
# 1. Build and install binary
go build -o /usr/local/bin/telamon .

# 2. Create config directory and copy files
sudo mkdir -p /etc/telamon/scripts /etc/telamon/static
sudo cp config.toml /etc/telamon/
sudo cp -r scripts/* /etc/telamon/scripts/

# 3. Install and start service
sudo cp telamon.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now telamon

# 4. Check status
sudo systemctl status telamon
sudo journalctl -u telamon -f
```

### Manage

```bash
sudo systemctl start   telamon
sudo systemctl stop    telamon
sudo systemctl restart telamon
sudo systemctl reload  telamon   # not supported; use restart
```

---

## Dependencies

| Package | Version | Purpose |
|---|---|---|
| `github.com/yuin/gopher-lua` | v1.1.1 | Lua 5.1 VM in pure Go |
| `github.com/BurntSushi/toml` | v1.4.0 | TOML config parsing |

Go standard library handles HTTP, JSON encoding, and file serving.

---

## Notation Conventions

Method calls on `request` and `response` use **colon notation** (`:`) — the table is passed as the implicit first argument:

```lua
response:write("hello")   -- correct
response.write("hello")   -- incorrect; self not passed
```

Utility functions on `json` and `telamon` use **dot notation** (`.`) — they are plain functions, not methods:

```lua
json.encode({ ok = true })   -- correct
telamon.log("started")       -- correct
```

`print()` is a global function — no table prefix needed:

```lua
print("Hello, World!")
```
