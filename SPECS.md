# Telamon HTTP Lua Runtime — Specification (SPECS)

> Generated from the provided Go source.

## Overview

Telamon is a lightweight HTTP server written in Go that routes HTTP requests to Lua scripts or static files.

### Version
- **Version:** `1.0.0`

## Features

- TOML-based configuration.
- HTTP server with configurable host and port.
- URL routing to Lua scripts.
- Static file serving fallback.
- Optional Lua sandbox.
- Request/response API exposed to Lua.
- JSON encode/decode helpers.
- HTTP redirects.
- Custom response headers and status codes.
- Path traversal protection.
- Logging API (`telamon.log`).

## Configuration

```toml
[server]
host = "..."
port = 8080
scripts_dir = "..."
static_dir = "..."

[lua]
unsandboxed = false
```

## Request Resolution

1. Try `<scripts_dir>/<path>.lua`
2. Try `<scripts_dir>/<path>/index.lua`
3. Serve matching static file.
4. Otherwise return **404**.

## Lua Globals

### request
- method
- path
- query
- host
- remote_addr
- headers
- params
- body
- getParam(key)

### response
- write(text)
- writeln(text)
- setStatus(code)
- setHeader(key, value)
- redirect(url, code?)
- json(value)

### json
- encode(value)
- decode(text)

### telamon
- version
- log(...)

## Security

When `unsandboxed = false`, only a limited set of Lua standard libraries is exposed:
- package
- base
- table
- string
- math

When enabled, all standard Lua libraries are available.

## Internal Flow

```
HTTP Request
      │
      ▼
Resolve Lua Script
      │
 ┌────┴────┐
 │         │
Found    Not Found
 │         │
 ▼         ▼
Run Lua  Static File
 │         │
 └────┬────┘
      ▼
HTTP Response
```

## Source Snapshot

The specification was generated from the supplied Go implementation on 2026-06-27T09:09:46.574680 UTC.
