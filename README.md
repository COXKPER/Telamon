# Telamon

A lightweight HTTP server written in Go that uses Lua (`.lua`) files as its scripting layer. Routes map directly to files — no framework, no boilerplate.

```
GET /            →  public/index.lua
GET /admin/users →  public/admin/users/index.lua
```

Telamon powers [AtlasCloak](https://github.com/COXKPER/AtlasCloak), a full OpenID Connect identity provider — but it is a general-purpose scriptable HTTP server on its own.

---

## Requirements

- Go 1.22+
- Linux (systemd optional, for production)

---

## Quick Start

```bash
# Clone / copy the project
cd telamon

# Download dependencies
make deps

# Build
make build

# Run (defaults to config.toml in the working directory)
./telamon --config config.toml
```

Visit `http://localhost:8081` — you should see the response from `public/index.lua`.

---

## Configuration — `config.toml`

```toml
[server]
port        = 8081          # Port to listen on
host        = "0.0.0.0"     # Bind address
public_dir  = "public"      # Root for .lua routes AND static assets
base_url    = ""            # Externally visible origin (e.g. "https://id.example.com");
                            # empty = derive from the request Host header (dev only)

[lua]
unsandboxed = true          # true = full stdlib (os, io, debug…); false = safe subset
```

> **Security note:** set `base_url` in production behind a reverse proxy so URLs are never derived from the untrusted `Host` header.

---

## Writing Scripts

Create a `.lua` file inside `public_dir`. The filename is the route.

```lua
-- public/greet.lua  →  GET /greet?name=Alice

local name = request:getParam("name")
if name == "" then name = "stranger" end

response:setStatus(200)
response:write("<h1>Hello, " .. name .. "!</h1>")
```

### Returning JSON

```lua
-- public/api/status.lua  →  GET /api/status

response:json({
    ok     = true,
    server = "Telamon v" .. telamon.version,
    time   = os.time(),
})
```

---

## URL Routing

Given a request for `GET /foo/bar`, Telamon resolves in this order:

1. `public/foo/bar.lua`
2. `public/foo/bar/index.lua`
3. Static file fallback: `public/foo/bar` (served if it exists and is a file)
4. `404 Not Found`

The root `/` resolves to `public/index.lua`. Requests whose path ends in `.lua` are rejected with 404 so scripts are never served as source. Resolved paths must stay inside `public_dir` (path traversal is blocked).

---

## Full Lua API

| Global | What it does |
|---|---|
| `request.method / .path / .query / .host / .remote_addr / .body` | Inspect the request |
| `request.headers["key"]` | Read a request header (lowercase key) |
| `request.params["key"]` | Query-string value lookup |
| `request:getParam("key")` | Method form of params lookup |
| `response:write(str)` | Append to response body |
| `response:writeln(str)` | Append + newline |
| `response:setStatus(code)` | Set HTTP status code |
| `response:setHeader(k, v)` | Set a response header |
| `response:json(value)` | JSON-encode and send with correct Content-Type |
| `response:redirect(url [, code])` | HTTP redirect (default 302) |
| `json.encode(value)` / `json.decode(str)` | JSON encode / decode |
| `ldb.create(path)` | Open a LevelDB at `path` and return a `db` object |
| `db:put(k, v) / db:get(k) / db:delete(k) / db:close()` | Key-value operations |
| `crypto.pbkdf2_hex(pw, salt, iter)` | PBKDF2-HMAC-SHA256 (Go) → hex digest |
| `crypto.random_hex(n)` / `crypto.random_b64url(n)` | CSPRNG bytes (Go `crypto/rand`) as hex / base64url |
| `crypto.sha256_raw(msg)` | Raw 32-byte SHA-256 digest (binary-safe string) |
| `crypto.p256_verify(msg, r, s, x, y)` | Verify an ECDSA P-256 signature over SHA-256(`msg`) — WebAuthn/FIDO2 ready |
| `telamon.version` | Server version string |
| `telamon.log(...)` | Log to server console (not the HTTP response) |
| `print(...)` | Write to the HTTP response body |

> **Tip:** `request` / `response` / `db` use colon (`:`) method syntax.
> `json`, `telamon`, `crypto`, `ldb` use dot (`.`) function syntax.

See [SPECS.md](SPECS.md) for the complete technical specification.

---

## Production — systemd

```bash
# 1. Build and install the binary
sudo make install

# 2. Install and start the service
sudo make service-install
sudo systemctl enable --now telamon

# 3. Check it's running
sudo systemctl status telamon
sudo journalctl -u telamon -f
```

---

## Makefile Targets

| Target | Description |
|---|---|
| `make deps` | Download Go module dependencies |
| `make build` | Build `./telamon` binary |
| `make run` | Build and run with `config.toml` |
| `make install` | Install binary to `/usr/local/bin/telamon` |
| `make service-install` | Install the systemd unit |
| `make service-remove` | Stop and remove the systemd service |
| `make clean` | Remove the built binary |

---

## Dependencies

| Package | Purpose |
|---|---|
| [`github.com/yuin/gopher-lua`](https://github.com/yuin/gopher-lua) | Lua 5.1 VM in pure Go |
| [`github.com/BurntSushi/toml`](https://github.com/BurntSushi/toml) | TOML config parsing |
| [`github.com/syndtr/goleveldb`](https://github.com/syndtr/goleveldb) | Embedded LevelDB key-value store |

Go standard library handles HTTP, JSON encoding, SHA-256, and ECDSA P-256.

---

## License

MIT — see [LICENSE](LICENSE).
