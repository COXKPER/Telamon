package main

import (
	"strings"
	"sync"

	"github.com/syndtr/goleveldb/leveldb"
	lua "github.com/yuin/gopher-lua"
)

const ldbUserData = "leveldb_db"

var (
	globalDBsMutex sync.Mutex
	globalDBs      = make(map[string]*leveldb.DB)
)

func registerLdb(L *lua.LState) {
	mt := L.NewTypeMetatable(ldbUserData)
	L.SetField(mt, "__index", L.SetFuncs(L.NewTable(), map[string]lua.LGFunction{
		"put":    ldbPut,
		"get":    ldbGet,
		"delete": ldbDelete,
		"close":  ldbClose,
	}))

	ldbTable := L.NewTable()
	L.SetField(ldbTable, "create", L.NewFunction(ldbCreate))
	L.SetField(ldbTable, "execute", L.NewFunction(ldbGlobalExecute))
	L.SetGlobal("ldb", ldbTable)
}

func ldbCreate(L *lua.LState) int {
	var path string
	if L.GetTop() >= 2 && L.Get(1).Type() == lua.LTTable {
		path = L.CheckString(2)
	} else {
		path = L.CheckString(1)
	}

	globalDBsMutex.Lock()
	defer globalDBsMutex.Unlock()

	db, ok := globalDBs[path]
	if !ok {
		var err error
		db, err = leveldb.OpenFile(path, nil)
		if err != nil {
			L.RaiseError("failed to open leveldb at %s: %v", path, err)
			return 0
		}
		globalDBs[path] = db
	}

	ud := L.NewUserData()
	ud.Value = db
	L.SetMetatable(ud, L.GetTypeMetatable(ldbUserData))
	L.Push(ud)
	return 1
}

func checkDB(L *lua.LState, index int) *leveldb.DB {
	ud := L.CheckUserData(index)
	if v, ok := ud.Value.(*leveldb.DB); ok {
		return v
	}
	L.ArgError(index, "leveldb database expected")
	return nil
}

func ldbPut(L *lua.LState) int {
	db := checkDB(L, 1)
	key := L.CheckString(2)
	val := L.CheckString(3)
	if err := db.Put([]byte(key), []byte(val), nil); err != nil {
		L.RaiseError("leveldb put error: %v", err)
	}
	return 0
}

func ldbGet(L *lua.LState) int {
	db := checkDB(L, 1)
	key := L.CheckString(2)
	data, err := db.Get([]byte(key), nil)
	if err != nil {
		if err == leveldb.ErrNotFound {
			L.Push(lua.LNil)
			return 1
		}
		L.RaiseError("leveldb get error: %v", err)
		return 0
	}
	L.Push(lua.LString(string(data)))
	return 1
}

func ldbDelete(L *lua.LState) int {
	db := checkDB(L, 1)
	key := L.CheckString(2)
	if err := db.Delete([]byte(key), nil); err != nil {
		L.RaiseError("leveldb delete error: %v", err)
	}
	return 0
}

func ldbClose(L *lua.LState) int {
	// No-op: The connection is now shared and globally managed
	return 0
}

// ldbGlobalExecute implements ldb:execute("command", DB)
func ldbGlobalExecute(L *lua.LState) int {
	var cmdStr string
	var dbUd lua.LValue

	if L.GetTop() >= 3 && L.Get(1).Type() == lua.LTTable {
		cmdStr = L.CheckString(2)
		dbUd = L.CheckUserData(3)
	} else {
		cmdStr = L.CheckString(1)
		dbUd = L.CheckUserData(2)
	}

	ud, ok := dbUd.(*lua.LUserData)
	if !ok {
		L.RaiseError("leveldb database expected")
		return 0
	}
	db, ok := ud.Value.(*leveldb.DB)
	if !ok {
		L.RaiseError("leveldb database expected")
		return 0
	}

	parts := strings.SplitN(cmdStr, " ", 3)
	if len(parts) == 0 {
		L.RaiseError("empty command")
		return 0
	}

	cmd := strings.ToUpper(parts[0])
	switch cmd {
	case "PUT":
		if len(parts) < 3 {
			L.RaiseError("PUT requires key and value")
			return 0
		}
		if err := db.Put([]byte(parts[1]), []byte(parts[2]), nil); err != nil {
			L.RaiseError("PUT error: %v", err)
			return 0
		}
		L.Push(lua.LBool(true))
		return 1
	case "GET":
		if len(parts) < 2 {
			L.RaiseError("GET requires key")
			return 0
		}
		data, err := db.Get([]byte(parts[1]), nil)
		if err != nil {
			if err == leveldb.ErrNotFound {
				L.Push(lua.LNil)
				return 1
			}
			L.RaiseError("GET error: %v", err)
			return 0
		}
		L.Push(lua.LString(string(data)))
		return 1
	case "DEL", "DELETE":
		if len(parts) < 2 {
			L.RaiseError("DEL requires key")
			return 0
		}
		if err := db.Delete([]byte(parts[1]), nil); err != nil {
			L.RaiseError("DEL error: %v", err)
			return 0
		}
		L.Push(lua.LBool(true))
		return 1
	default:
		L.RaiseError("unknown command: %s", cmd)
		return 0
	}
}
