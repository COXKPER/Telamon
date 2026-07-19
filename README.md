# Telamon

A lightweight HTTP server written in Go that uses Lua (`.lua`) files as its scripting layer. Routes map directly to files — no framework, no boilerplate.

```
GET /         →  scripts/index.lua
GET /api/hello →  scripts/api/hello.lua
```

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

# Run (defaults to port 80 — needs root, or change port in config.toml)
sudo ./telamon

# Run with a custom config
./telamon --config /path/to/config.toml
```

Visit `http://localhost` — you should see the Telamon welcome page.

---

## Project Layout

```
telamon/
├── main.go                  # Server source
├── go.mod / go.sum
├── config.toml              # Default configuration
├── telamon.service          # systemd unit file
├── SPECS.md                 # Full technical specification
├── scripts/                 # Lua route scripts
│   ├── index.lua            # GET /
│   └── api/
│       └── hello.lua        # GET /api/hello
└── static/                  # Static files (CSS, JS, images)
```

---

## Configuration

Edit `config.toml`:

```toml
[server]
port        = 80          # Port to listen on
host        = "0.0.0.0"  # Bind address
scripts_dir = "scripts"   # Lua scripts root
static_dir  = "static"    # Static files root

[lua]
unsandboxed = true        # Full os/io/debug access
```

---

## Writing Scripts

Create a `.lua` file inside `scripts/`. The filename is the route.

```lua
-- scripts/greet.lua  →  GET /greet?name=Alice

local name = request:getParam("name")
if name == "" then name = "stranger" end

response:setStatus(200)
response:write("<h1>Hello, " .. name .. "!</h1>")
```

### Returning JSON

```lua
-- scripts/api/status.lua  →  GET /api/status

response:json({
    ok      = true,
    server  = "Telamon v" .. telamon.version,
    time    = os.time(),
})
```

### Full Lua API

| Global | What it does |
|---|---|
| `request.method / .path / .query / .host / .body` | Inspect the request |
| `request.headers["key"]` | Read a request header (lowercase key) |
| `request.params["key"]` | Read a query-string value |
| `request:getParam("key")` | Same as above, method form |
| `response:write(str)` | Append to response body |
| `response:writeln(str)` | Append + newline |
| `response:setStatus(code)` | Set HTTP status code |
| `response:setHeader(k, v)` | Set a response header |
| `response:json(value)` | JSON-encode and send with correct Content-Type |
| `response:redirect(url [, code])` | HTTP redirect (default 302) |
| `json.encode(value)` | Lua value → JSON string |
| `json.decode(str)` | JSON string → Lua table |
| `ldb.create(path)` | Open LevelDB at `path` and return `db` object |
| `ldb.execute(cmd, db)` | Run command (`"PUT key val"`, `"GET key"`, `"DEL key"`) on `db` |
| `db:put(k, v) / db:get(k) / db:delete(k)`| Native LevelDB KV operations |
| `telamon.log(...)` | Log to server console (not HTTP response) |
| `print(...)` | Write to HTTP response body |

> **Tip:** `request` and `response` use colon (`:`) method syntax.  
> `json` and `telamon` use dot (`.`) function syntax.

---

## URL Routing

Telamon resolves routes in this order:

1. `scripts/<path>.lua`
2. `scripts/<path>/index.lua`
3. `static/<path>` (served as a file)
4. `404 Not Found`

The root `/` always resolves to `scripts/index.lua`.

---

## Production — systemd

```bash
# 1. Build and install the binary
make build
sudo make install

# 2. Set up config and scripts
sudo mkdir -p /etc/telamon/scripts /etc/telamon/static
sudo cp config.toml /etc/telamon/
sudo cp -r scripts/* /etc/telamon/scripts/

# 3. Install and start the service
sudo make service-install
sudo systemctl enable --now telamon

# 4. Check it's running
sudo systemctl status telamon
sudo journalctl -u telamon -f
```

The service reads its config from `/etc/telamon/config.toml` and its scripts from `/etc/telamon/scripts/`.

---

## Makefile Targets

| Target | Description |
|---|---|
| `make deps` | Download Go module dependencies |
| `make build` | Build `./telamon` binary |
| `make run` | Build and run with `config.toml` |
| `make install` | Install binary to `/usr/local/bin/telamon` |
| `make service-install` | Install `telamon.service` to systemd |
| `make service-remove` | Stop and remove the systemd service |
| `make clean` | Remove the built binary |

---

## Dependencies

| Package | Version |
|---|---|
| [`github.com/yuin/gopher-lua`](https://github.com/yuin/gopher-lua) | v1.1.1 |
| [`github.com/BurntSushi/toml`](https://github.com/BurntSushi/toml) | v1.4.0 |

---

## License

MIT
