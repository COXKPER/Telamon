package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
	lua "github.com/yuin/gopher-lua"
)

const Version = "1.0.0"

// ─── Config ───────────────────────────────────────────────────────────────────

type Config struct {
	Server ServerConfig `toml:"server"`
	Lua    LuaConfig    `toml:"lua"`
}

type ServerConfig struct {
	Port       int    `toml:"port"`
	Host       string `toml:"host"`
	ScriptsDir string `toml:"scripts_dir"`
	StaticDir  string `toml:"static_dir"`
}

type LuaConfig struct {
	Unsandboxed bool `toml:"unsandboxed"`
}

var cfg Config

// ─── Entry Point ──────────────────────────────────────────────────────────────

func main() {
	configPath := flag.String("config", "config.toml", "path to config.toml")
	flag.Parse()

	if _, err := toml.DecodeFile(*configPath, &cfg); err != nil {
		log.Fatalf("[Telamon] Failed to load %s: %v", *configPath, err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", handleRequest)

	addr := fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port)
	log.Printf("[Telamon] v%s  →  http://%s", Version, addr)
	log.Printf("[Telamon] Scripts : ./%s", cfg.Server.ScriptsDir)
	log.Printf("[Telamon] Static  : ./%s", cfg.Server.StaticDir)
	log.Printf("[Telamon] Lua     : unsandboxed=%v", cfg.Lua.Unsandboxed)

	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatalf("[Telamon] Fatal: %v", err)
	}
}

// ─── Routing ──────────────────────────────────────────────────────────────────

func handleRequest(w http.ResponseWriter, r *http.Request) {
	log.Printf("[%s] %s %s", r.RemoteAddr, r.Method, r.URL.Path)

	// 1. Try a matching Lua script
	if scriptPath := resolveLuaScript(r.URL.Path); scriptPath != "" {
		executeLua(w, r, scriptPath)
		return
	}

	// 2. Fall back to static files
	if cfg.Server.StaticDir != "" {
		staticPath := filepath.Join(cfg.Server.StaticDir, filepath.Clean(r.URL.Path))
		if info, err := os.Stat(staticPath); err == nil && !info.IsDir() {
			http.ServeFile(w, r, staticPath)
			return
		}
	}

	http.NotFound(w, r)
}

// resolveLuaScript maps a URL path → .lua file inside the scripts directory.
// Resolution order:
//  1. scripts/<path>.lua
//  2. scripts/<path>/index.lua
func resolveLuaScript(urlPath string) string {
	scriptsAbs, err := filepath.Abs(cfg.Server.ScriptsDir)
	if err != nil {
		return ""
	}

	clean := filepath.Clean(urlPath)
	if clean == "." || clean == string(filepath.Separator) {
		clean = "index"
	}
	clean = strings.TrimPrefix(clean, string(filepath.Separator))

	for _, candidate := range []string{
		filepath.Join(cfg.Server.ScriptsDir, clean+".lua"),
		filepath.Join(cfg.Server.ScriptsDir, clean, "index.lua"),
	} {
		abs, err := filepath.Abs(candidate)
		if err != nil {
			continue
		}
		// Guard against path traversal
		if !strings.HasPrefix(abs, scriptsAbs+string(filepath.Separator)) &&
			abs != scriptsAbs {
			continue
		}
		if _, err := os.Stat(abs); err == nil {
			return abs
		}
	}

	return ""
}

// ─── Lua Execution ────────────────────────────────────────────────────────────

func executeLua(w http.ResponseWriter, r *http.Request, scriptPath string) {
	// Create Lua state
	var L *lua.LState
	if cfg.Lua.Unsandboxed {
		// Full standard library access (os, io, debug, etc.)
		L = lua.NewState()
	} else {
		// Safe subset only
		L = lua.NewState(lua.Options{SkipOpenLibs: true})
		for _, lib := range []struct {
			n string
			f lua.LGFunction
		}{
			{lua.LoadLibName, lua.OpenPackage},
			{lua.BaseLibName, lua.OpenBase},
			{lua.TabLibName, lua.OpenTable},
			{lua.StringLibName, lua.OpenString},
			{lua.MathLibName, lua.OpenMath},
		} {
			L.Push(L.NewFunction(lib.f))
			L.Push(lua.LString(lib.n))
			L.Call(1, 0)
		}
	}
	defer L.Close()

	// Response state
	var buf bytes.Buffer
	statusCode := 200
	headers := map[string]string{"Content-Type": "text/html; charset=utf-8"}
	redirected := false

	// ── request global ────────────────────────────────────────────────────────
	reqT := L.NewTable()
	L.SetField(reqT, "method", lua.LString(r.Method))
	L.SetField(reqT, "path", lua.LString(r.URL.Path))
	L.SetField(reqT, "query", lua.LString(r.URL.RawQuery))
	L.SetField(reqT, "host", lua.LString(r.Host))
	L.SetField(reqT, "remote_addr", lua.LString(r.RemoteAddr))

	// request.headers["<lowercase-name>"]
	hdrT := L.NewTable()
	for k, v := range r.Header {
		L.SetField(hdrT, strings.ToLower(k), lua.LString(strings.Join(v, ", ")))
	}
	L.SetField(reqT, "headers", hdrT)

	// request.params["<key>"]
	paramsT := L.NewTable()
	for k, v := range r.URL.Query() {
		L.SetField(paramsT, k, lua.LString(strings.Join(v, ", ")))
	}
	L.SetField(reqT, "params", paramsT)

	// request.body
	var bodyBytes []byte
	if r.Body != nil {
		bodyBytes, _ = io.ReadAll(r.Body)
		r.Body.Close()
	}
	L.SetField(reqT, "body", lua.LString(string(bodyBytes)))

	// request:getParam("key") → value string
	L.SetField(reqT, "getParam", L.NewFunction(func(L *lua.LState) int {
		L.Push(lua.LString(r.URL.Query().Get(L.CheckString(2))))
		return 1
	}))

	// ── response global ───────────────────────────────────────────────────────

	resT := L.NewTable()

	// response:write(str)
	L.SetField(resT, "write", L.NewFunction(func(L *lua.LState) int {
		buf.WriteString(L.CheckString(2))
		return 0
	}))

	// response:writeln(str)
	L.SetField(resT, "writeln", L.NewFunction(func(L *lua.LState) int {
		buf.WriteString(L.CheckString(2) + "\n")
		return 0
	}))

	// response:setStatus(code)
	L.SetField(resT, "setStatus", L.NewFunction(func(L *lua.LState) int {
		statusCode = L.CheckInt(2)
		return 0
	}))

	// response:setHeader(key, value)
	L.SetField(resT, "setHeader", L.NewFunction(func(L *lua.LState) int {
		headers[L.CheckString(2)] = L.CheckString(3)
		return 0
	}))

	// response:redirect(url [, code=302])
	L.SetField(resT, "redirect", L.NewFunction(func(L *lua.LState) int {
		http.Redirect(w, r, L.CheckString(2), L.OptInt(3, 302))
		redirected = true
		return 0
	}))

	// response:json(value)  — encodes as JSON + sets Content-Type
	L.SetField(resT, "json", L.NewFunction(func(L *lua.LState) int {
		encoded, err := json.Marshal(luaToGo(L.CheckAny(2)))
		if err != nil {
			L.RaiseError("response:json encode error: %v", err)
			return 0
		}
		headers["Content-Type"] = "application/json; charset=utf-8"
		buf.Write(encoded)
		return 0
	}))

	// ── json global ───────────────────────────────────────────────────────────

	jsonT := L.NewTable()

	// json.encode(value) → string
	L.SetField(jsonT, "encode", L.NewFunction(func(L *lua.LState) int {
		encoded, err := json.Marshal(luaToGo(L.CheckAny(1)))
		if err != nil {
			L.RaiseError("json.encode: %v", err)
			return 0
		}
		L.Push(lua.LString(string(encoded)))
		return 1
	}))

	// json.decode(string) → table
	L.SetField(jsonT, "decode", L.NewFunction(func(L *lua.LState) int {
		var v interface{}
		if err := json.Unmarshal([]byte(L.CheckString(1)), &v); err != nil {
			L.RaiseError("json.decode: %v", err)
			return 0
		}
		L.Push(goToLua(L, v))
		return 1
	}))

	// ── telamon global ────────────────────────────────────────────────────────

	telaT := L.NewTable()
	L.SetField(telaT, "version", lua.LString(Version))

	// telamon.log(...) → writes to server stdout, not to HTTP response
	L.SetField(telaT, "log", L.NewFunction(func(L *lua.LState) int {
		parts := make([]string, L.GetTop())
		for i := range parts {
			parts[i] = L.ToStringMeta(L.Get(i + 1)).String()
		}
		log.Printf("[Lua] %s", strings.Join(parts, " "))
		return 0
	}))

	// ── override print → response buffer ─────────────────────────────────────

	L.SetGlobal("print", L.NewFunction(func(L *lua.LState) int {
		n := L.GetTop()
		parts := make([]string, n)
		for i := 1; i <= n; i++ {
			parts[i-1] = L.ToStringMeta(L.Get(i)).String()
		}
		buf.WriteString(strings.Join(parts, "\t") + "\n")
		return 0
	}))

	L.SetGlobal("request", reqT)
	L.SetGlobal("response", resT)
	L.SetGlobal("json", jsonT)
	L.SetGlobal("telamon", telaT)

	// ── execute ───────────────────────────────────────────────────────────────

	if err := L.DoFile(scriptPath); err != nil {
		log.Printf("[Telamon] Lua error in %s: %v", scriptPath, err)
		if !redirected {
			http.Error(w,
				fmt.Sprintf("500 Internal Server Error\n\n%v", err),
				http.StatusInternalServerError,
			)
		}
		return
	}

	if redirected {
		return
	}

	for k, v := range headers {
		w.Header().Set(k, v)
	}
	w.WriteHeader(statusCode)
	_, _ = w.Write(buf.Bytes())
}

// ─── Type Converters ──────────────────────────────────────────────────────────

// luaToGo converts Lua values to Go types for JSON encoding.
func luaToGo(val lua.LValue) interface{} {
	switch v := val.(type) {
	case *lua.LNilType:
		return nil
	case lua.LBool:
		return bool(v)
	case lua.LNumber:
		return float64(v)
	case lua.LString:
		return string(v)
	case *lua.LTable:
		// Detect array-like table (consecutive integer keys from 1)
		if n := v.MaxN(); n > 0 {
			arr := make([]interface{}, n)
			for i := 1; i <= n; i++ {
				arr[i-1] = luaToGo(v.RawGetInt(i))
			}
			return arr
		}
		// Otherwise treat as object
		obj := make(map[string]interface{})
		v.ForEach(func(k, val lua.LValue) {
			obj[k.String()] = luaToGo(val)
		})
		return obj
	default:
		return val.String()
	}
}

// goToLua converts Go values (from JSON decode) back to Lua values.
func goToLua(L *lua.LState, val interface{}) lua.LValue {
	if val == nil {
		return lua.LNil
	}
	switch v := val.(type) {
	case bool:
		return lua.LBool(v)
	case float64:
		return lua.LNumber(v)
	case string:
		return lua.LString(v)
	case []interface{}:
		t := L.NewTable()
		for i, item := range v {
			t.RawSetInt(i+1, goToLua(L, item))
		}
		return t
	case map[string]interface{}:
		t := L.NewTable()
		for k, item := range v {
			L.SetField(t, k, goToLua(L, item))
		}
		return t
	default:
		return lua.LString(fmt.Sprintf("%v", v))
	}
}
