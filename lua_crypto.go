package main

import (
	"crypto/ecdsa"
	"crypto/hmac"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"math/big"

	lua "github.com/yuin/gopher-lua"
)

// pbkdf2SHA256 implements PBKDF2-HMAC-SHA256 (RFC 2898 / 8018) using only the
// Go standard library. keyLen is in bytes.
func pbkdf2SHA256(password, salt []byte, iter, keyLen int) []byte {
	prf := hmac.New(sha256.New, password)
	hashLen := prf.Size()
	numBlocks := (keyLen + hashLen - 1) / hashLen

	var buf [4]byte
	dk := make([]byte, 0, numBlocks*hashLen)
	U := make([]byte, hashLen)

	for block := 1; block <= numBlocks; block++ {
		prf.Reset()
		prf.Write(salt)
		buf[0] = byte(block >> 24)
		buf[1] = byte(block >> 16)
		buf[2] = byte(block >> 8)
		buf[3] = byte(block)
		prf.Write(buf[:4])
		dk = prf.Sum(dk)
		T := dk[len(dk)-hashLen:]
		copy(U, T)

		for n := 2; n <= iter; n++ {
			prf.Reset()
			prf.Write(U)
			U = U[:0]
			U = prf.Sum(U)
			for x := range U {
				T[x] ^= U[x]
			}
		}
	}
	return dk[:keyLen]
}

// registerCrypto exposes a small cryptographic bridge to Lua as the `crypto`
// global: PBKDF2-HMAC-SHA256 and CSPRNG helpers. Pure-Lua hashing is far too
// slow (~400ms per SHA-256 in GopherLua) to support meaningful iteration counts.
func registerCrypto(L *lua.LState) {
	t := L.NewTable()

	// crypto.pbkdf2_hex(password, salt, iterations) -> hex string (32 bytes)
	L.SetField(t, "pbkdf2_hex", L.NewFunction(func(L *lua.LState) int {
		password := L.CheckString(1)
		salt := L.CheckString(2)
		iter := int(L.CheckNumber(3))
		if iter < 1 || iter > 10_000_000 {
			L.ArgError(3, "iterations out of range")
		}
		dk := pbkdf2SHA256([]byte(password), []byte(salt), iter, 32)
		L.Push(lua.LString(hex.EncodeToString(dk)))
		return 1
	}))

	// crypto.random_hex(numBytes) -> lowercase hex string
	L.SetField(t, "random_hex", L.NewFunction(func(L *lua.LState) int {
		n := int(L.CheckNumber(1))
		if n < 1 || n > 1024 {
			L.ArgError(1, "byte count out of range (1..1024)")
		}
		b := make([]byte, n)
		if _, err := rand.Read(b); err != nil {
			L.RaiseError("crypto.random_hex: %v", err)
		}
		L.Push(lua.LString(hex.EncodeToString(b)))
		return 1
	}))

	// crypto.random_b64url(numBytes) -> unpadded base64url string
	L.SetField(t, "random_b64url", L.NewFunction(func(L *lua.LState) int {
		n := int(L.CheckNumber(1))
		if n < 1 || n > 1024 {
			L.ArgError(1, "byte count out of range (1..1024)")
		}
		b := make([]byte, n)
		if _, err := rand.Read(b); err != nil {
			L.RaiseError("crypto.random_b64url: %v", err)
		}
		L.Push(lua.LString(base64.RawURLEncoding.EncodeToString(b)))
		return 1
	}))

	// crypto.sha256_raw(message) -> raw 32-byte digest as a Go string (binary-safe)
	L.SetField(t, "sha256_raw", L.NewFunction(func(L *lua.LState) int {
		msg := []byte(L.CheckString(1))
		sum := sha256.Sum256(msg)
		L.Push(lua.LString(string(sum[:])))
		return 1
	}))

	// crypto.p256_verify(msg, rB64u, sB64u, xB64u, yB64u) -> boolean
	// Verifies an ECDSA P-256 (ES256) signature over SHA-256(message).
	// All big integers are unpadded base64url. Used by the WebAuthn bridge.
	L.SetField(t, "p256_verify", L.NewFunction(func(L *lua.LState) int {
		msg := []byte(L.CheckString(1))
		dec := func(s string) *big.Int {
			b, err := base64.RawURLEncoding.DecodeString(s)
			if err != nil {
				L.ArgError(2, "invalid base64url component")
			}
			return new(big.Int).SetBytes(b)
		}
		r := dec(L.CheckString(2))
		s := dec(L.CheckString(3))
		x := dec(L.CheckString(4))
		y := dec(L.CheckString(5))

		if !elliptic.P256().IsOnCurve(x, y) {
			L.Push(lua.LBool(false))
			return 1
		}
		digest := sha256.Sum256(msg)
		pub := &ecdsa.PublicKey{Curve: elliptic.P256(), X: x, Y: y}
		ok := ecdsa.Verify(pub, digest[:], r, s)
		L.Push(lua.LBool(ok))
		return 1
	}))

	L.SetGlobal("crypto", t)
}
