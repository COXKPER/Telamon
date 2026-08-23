# Audit Report — AtlasCloak / Telamon

**Tanggal:** 23 Agustus 2026
**Scope:** Seluruh kode inti — `main.go`, `lua_ldb.go`, `public/lib/utils.lua` (1574 baris), seluruh endpoint OIDC, admin console, account console, test client (`test_oidc_client.py`), Makefile & dokumentasi.
**Total baris diaudit:** ±5.400 baris Lua (30 file) + ±590 baris Go.

---

## Ringkasan Arsitektur

AtlasCloak adalah Identity Provider mirip Keycloak yang dibangun di atas **Telamon** — HTTP server Go dengan layer skrip Lua (gopher-lua) dan penyimpanan LevelDB.

```
[Client] → net/http mux (Go)
              ├─ resolveLuaScript()   →  public/<path>.lua | <path>/index.lua
              ├─ Static fallback      →  public/<path>
              └─ 404
       executeLua(): LState baru per request + globals request/response/json/telamon/ldb
```

Komponen utama:

| Komponen | Lokasi |
|---|---|
| Core library (JWT, session, RBAC/GBAC, hashing, PKCE) | `public/lib/utils.lua` |
| SHA-256/HMAC pure-Lua | `public/lib/sha256.lua` |
| OIDC endpoints (auth, token, userinfo, introspect, revoke, logout, certs, consent, discovery) | `public/auth/realms/master/...` |
| Login/register actions | `public/auth/realms/master/login-actions/` |
| Admin console | `public/admin/` |
| Account console | `public/account/` |
| LevelDB bridge | `lua_ldb.go` |

---

## 🔴 KRITIS

### K1 — Hashing password rusak
**Lokasi:** `public/lib/utils.lua:69-90`

- `hash_password` = SHA-256 dengan salt statis `"atlas_salt_"` — tanpa per-user salt, tanpa key stretching, cepat dibruteforce.
- Lebih fatal: `verify_password` **menerima plaintext equality** (`stored == input`) dan hash `djb2` (toy hash non-kriptografis) sebagai format valid. Password plaintext yang bocor ke DB tetap bisa login.

**Perbaikan:**
1. Ganti ke PBKDF2-HMAC-SHA256 (per-user salt acak, ±100k iterasi), format simpan `pbkdf2$iter$salt_hex$hash_hex`.
2. Implementasi via bridge Go baru (`golang.org/x/crypto/pbkdf2` diekspos ke Lua) karena pure-Lua terlalu lambat.
3. Hapus branch plaintext & djb2 dari `verify_password`; login format lama → verifikasi legacy sekali lalu re-hash transparan.

### K2 — Default credentials `admin/admin`
**Lokasi:** `public/lib/utils.lua:787-805`

`ensure_admin_exists()` membuat akun `admin/admin` otomatis dan dipanggil di hampir semua endpoint publik (auth.lua, token.lua, index.lua) setiap request. Tidak ada force password change.

**Perbaikan:**
1. Password awal dari env `ATLAS_ADMIN_PASSWORD`, atau digenerate acak dan dicatat sekali saat bootstrap.
2. Flag `must_change_password = true` → redirect wajib ganti password saat login pertama.
3. Panggil bootstrap sekali saja (saat startup / lazy-once), bukan di tiap endpoint publik.

### K3 — Authorization code tidak di-bind ke client
**Lokasi:** `public/auth/realms/master/protocol/openid-connect/token.lua:82-144`

Saat exchange grant `authorization_code`, `form.client_id` dan `form.redirect_uri` **tidak pernah dicocokkan** dengan `code_data.client_id/redirect_uri` — pelanggaran RFC 6749 §4.1.3. Code yang dicuri bisa ditukar token oleh siapa saja. Code juga tidak punya expiry.

**Perbaikan:**
1. Wajib cocokkan `client_id` dan `redirect_uri` persis dengan yang tersimpan di code_data → jika beda, `400 invalid_grant`.
2. Simpan `created = os.time()` di code_data; tolak code berumur > 60 detik.

### K4 — Refresh token tidak punya expiry efektif
**Lokasi:** `token.lua:212-235`, `utils.lua:274-281`

`r_data.exp = now + lifespan*7` disimpan tetapi **tidak pernah dicek** di grant `refresh_token`. Selama dirotasi, refresh token hidup selamanya. Tidak ada pengecekan binding `client_id` juga.

**Perbaikan:**
1. Cek `os.time() > r_data.exp` → `400 invalid_grant`.
2. Cek `r_data.client_id` cocok dengan presenter token.
3. Opsional: reuse detection (refresh token yang sudah di-delete dipresentasikan lagi → revoke satu family).

### K5 — Open redirect total
**Lokasi:** `auth.lua:135-220`, `consent.lua`, `logout.lua:3`

`redirect_uri` dari query string langsung dipakai tujuan redirect **tanpa validasi** terhadap daftar registered client (seed data bahkan memakai `"/*"`). `logout.lua` me-redirect ke `post_logout_redirect_uri` arbitrer.

**Perbaikan:**
1. Helper baru `utils.validate_redirect_uri(db, client_id, uri)` — exact-match terhadap daftar `redirect_uris` client teregistrasi.
2. Ganti seed `"/*"` menjadi URI eksplisit.
3. URI invalid → error page (`invalid_request`), bukan redirect.
4. Logout: validasi `post_logout_redirect_uri`, fallback ke `/`.

### K6 — Database LevelDB lama berada di dalam web root
**Lokasi:** `main.go:82-97`, `.gitignore`

`public/social.db/` dan `public/storage/` ada di bawah `public_dir` → file-file LevelDB (hash password, session, JWT secret lama) bisa diunduh siapa saja lewat fallback static serving.

**Perbaikan:**
1. Pindahkan direktori tersebut keluar web root (mis. `./data/`).
2. Defensive depth di `main.go`: blokir serving path `*.db*`, `*.db-*`, dotfiles.
3. Pastikan `db_path` selalu di luar `public_dir`.

---

## 🟠 TINGGI

### T1 — Tidak ada proteksi CSRF; mutasi via GET
**Lokasi:** `admin/sessions/index.lua:14-45` (`?revoke=`), `admin/users/edit.lua:26-44` (`?action=unlock`), `admin/events/index.lua:15-20` (`?action=clear`)

Tidak ada CSRF token sama sekali. Cookie `SameSite=Lax` hanya melindungi POST lintas situs — mutasi via GET tetap bisa dipicu navigasi top-level.

**Perbaikan:** helper CSRF one-time-token per session + hidden field di semua form POST; ubah semua mutasi GET → POST; pertahankan SameSite=Lax.

### T2 — JWT secret lemah dan terekspos
**Lokasi:** `utils.lua:179-186`, `admin/realm-settings/index.lua:180`, `utils.lua:631`

Secret dibuat dari dua UUID `math.random` (bukan crypto/rand); ditampilkan plaintext sebagai value input form settings; ikut disertakan dalam realm export JSON.

**Perbaikan:** generate via bridge Go crypto/rand (32 byte); form settings pakai placeholder + tombol regenerate (jangan render value); keluarkan `jwt_secret` dari export.

### T3 — Session & auth code tanpa expiry; list tanpa batas
**Lokasi:** `login-actions/authenticate.lua:109-122`, `token.lua:58-68`

Session tidak pernah kedaluwarsa; `meta:session_list` tumbuh tanpa batas. Events di-cap 100 hanya di beberapa path (`authenticate.lua:87`, `account/password.lua:47`) tapi **tidak** di `token.lua` → unbounded growth.

**Perbaikan:** idle-timeout session (cek `last_access`, mis. 12 jam); buat satu helper `utils.log_event(db, event)` dengan cap — menggantikan ~15 duplikasi manual.

### T4 — Lockout brute-force bisa jadi senjata DoS
**Lokasi:** `utils.lua:400-420`

Counter failure di-key per-username saja → attacker bisa mengunci akun korban secara massal. Parameter `ip` diterima tapi **diabaikan**; tidak ada throttle per-IP.

**Perbaikan:** counter per kombinasi `username+IP`; lockout hanya untuk kombinasi tersebut; tambah throttle per-IP global; delay progresif opsional.

### T5 — Introspection & revocation tanpa client authentication
**Lokasi:** `token/introspect.lua`, `revoke.lua`

Pelanggaran RFC 7662 / RFC 7009. Siapa pun bisa mengintrospeksi token curian, atau me-revoke token orang lain (griefing).

**Perbaikan:** wajibkan client auth (Basic / `client_secret_post`): confidential → cek secret; public → minimal `client_id` teregistrasi.

### T6 — Host header injection pada issuer
**Lokasi:** `utils.lua:228,289`, `.well-known/openid-configuration.lua:1`

Claim `iss` JWT dan seluruh URL discovery doc dibangun dari `request.host` yang dikendalikan attacker → poisoning issuer & discovery cache.

**Perbaikan:** config baru `[server] base_url`; derive issuer/discovery dari situ; fallback whitelist Host header.

### T7 — Password grant aktif & melewati anti-bot
**Lokasi:** `token.lua:147-209`

Grant `password` (deprecated di OAuth 2.1) aktif by default dan **melewati** security token/honeypot (hanya lockout). Channel bruteforce murah.

**Perbaikan:** toggle setting `password_grant_enabled` (default **off**); saat aktif tetap enforce lockout + rate limit per-IP.

### T8 — JWKS dummy RS256 sementara token HS256
**Lokasi:** `certs.lua`, `openid-configuration.lua:36-39`

Discovery mengklaim dukungan RS256 dan mengekspos `jwks_uri` berisi RSA key placeholder, padahal semua token ditandatangani HS256. Relying party yang memvalidasi via JWKS akan gagal/menyesatkan.

**Perbaikan:** hapus `jwks_uri` + klaim RS256; `id_token_signing_alg_values_supported = ["HS256"]`. (Atau implement RS256 sungguhan via bridge Go.)

---

## 🟡 SEDANG

| # | Temuan | Perbaikan |
|---|--------|-----------|
| **S1** | `main.go:58`: `http.ListenAndServe` tanpa timeout (Slowloris); body dibaca `io.ReadAll` tanpa limit; access log mencatat query string mentah → token via `?token=` ikut ke log (`introspect.lua:10`, `revoke.lua:10`) | `http.Server{ReadTimeout, WriteTimeout, IdleTimeout, MaxHeaderBytes}`; `io.LimitReader(body, 1MB)`; mask parameter sensitif di log |
| **S2** | Race condition read-modify-write (append events, append session list, increment fail counter) → lost updates; brute force counter bisa dilewati lewat paralelisme | Named mutex via bridge Go (`telamon.with_lock(name, fn)`), atau terima risiko single-instance secara eksplisit |
| **S3** | `response_type` dicocokkan substring (`string.find(response_type,"code")` di `auth.lua:140,155,162`) → `response_type=codexyz` lolos | Exact token-match per spasi; validasi kombinasi terhadap yang didukung |
| **S4** | `consent.lua:138-145` deny path: `state` di-append tanpa encode; `?error=` di-append tanpa cek query existing; empty `redirect_uri` menghasilkan URL relatif rusak | Encode semua komponen; deteksi `?` vs `&`; guard empty redirect |
| **S5** | Username tanpa validasi karakter, dipakai langsung sebagai DB key (`"user:"..username`) | Validasi `^[a-zA-Z0-9._-]{3,64}$` di registrasi & admin create user |
| **S6** | Registrasi: tanpa cek email duplikat, tanpa normalisasi lowercase; enumerasi user via "Username already exists"; timing unknown-user vs wrong-password berbeda (hash dilakukan/skip) | Cek email duplikat + lowercase normalize; dummy-hash saat unknown user untuk menyamakan timing |
| **S7** | Dokumentasi drift: README/SPECS/Makefile masih merujuk `scripts_dir`/`static_dir`/`scripts/`+`static/` padahal config sekarang `public_dir`; file `telamon.service` dirujuk tapi tidak ada di repo | Update README/SPECS/Makefile ke `public_dir`; buat `telamon.service` yang konsisten |

---

## 🟢 RENDA

| # | Temuan | Perbaikan |
|---|--------|-----------|
| **R1** | `test_oidc_client.py`: kredensial hardcoded (`admin/admin`, `secret123`); bug `b64url_decode` — kasus `len%4==1` tidak tertangani | Kredensial dari env var; fix padding; pindah ke direktori `tests/` |
| **R2** | `uuid()` (`utils.lua:61-67`) memakai `math.random` non-kripto — dipakai untuk session id, auth code, jti, refresh token | Bridge Go crypto/rand untuk semua identifier keamanan |
| **R3** | Cookie `ATLAS_SESSION` tanpa flag `Secure` (wajar untuk dev HTTP) | Set `Secure` ketika `base_url` https |
| **R4** | `luaToGo` (`main.go:344-372`): tabel array sparse menghasilkan entri null; mixed table kehilangan kunci | Dokumentasikan perilaku atau normalisasi konversi |
| **R5** | `dofile("public/lib/utils.lua")` hardcode relatif CWD di 30+ file | Resolusi path relatif config / satu kali load |

---

## Catatan Keputusan Desain

1. **Bridge Go baru diperlukan** untuk: PBKDF2 (K1), crypto/rand (T2/R2), opsional named mutex (S2). Perubahan menyentuh `lua_ldb.go` / `main.go`.
2. **Migrasi database:** pilihan reset bersih vs migrasi transparan (re-hash saat login sukses format lama). Direkomendasikan reset untuk environment dev.
3. **Kompromi performa:** LState gopher-lua baru per request + SHA-256 pure-Lua membuat endpoint auth mahal secara CPU → rate limiting per-IP (T4/S1) sekaligus mitigasi DoS amplifikasi.
4. **`unsandboxed = true`** adalah keputusan desain terdokumentasi: siapa pun yang bisa menulis file `.lua` ke `public/` mendapatkan eksekusi penuh (os/io). Pertahankan, tapi pastikan tidak ada jalur upload file.

---

## Urutan Eksekusi yang Diusulkan

| Fase | Isi | Verifikasi |
|------|-----|------------|
| **1** | K1–K6 (semua temuan kritikal) | `test_oidc_client.py` + uji manual login/PKCE |
| **2** | T1–T8 (keamanan tinggi) | Uji CSRF, introspect/revoke auth, discovery doc |
| **3** | S1–S7 (robustness & konsistensi) | Load test ringan, lint, review log |
| **4** | R1–R5 (polish) | Regression suite |

> File ini dihasilkan dari audit statis manual atas seluruh kode pada tanggal audit di atas. Temuan direferensikan dengan `file:line` sesuai kondisi kode saat audit.

---

# STATUS PATCH — 23 Agustus 2026 (v1.2.2)

## Ringkasan Eksekusi

Benchmark GopherLua menunjukkan SHA-256 pure-Lua ≈ **400 ms/call** pada mesin ini — iterasi tinggi mustahil di Lua. Solusi: **bridge Go minimal tanpa dependensi baru** (`lua_crypto.go`, PBKDF2-HMAC-SHA256 diimplementasikan manual dari stdlib + `crypto/rand`).

## Temuan yang Ditambal

| ID | Status | Perubahan |
|----|--------|-----------|
| **K1** | ✅ FIXED | `hash_password` → PBKDF2-HMAC-SHA256 asli via Go, format baru `atlas_pbkdf2$<rounds>$<salt>$<hash>`, target 100.000 iterasi (~30–50 ms di hardware normal; ~0,9 s di box dev ini yang sangat lambat ±9 µs/iterasi). Migrasi transparan: hash lama (`pbkdf2_sha256$`, `atlas_salt_`, djb2) tetap terverifikasi lalu di-rehash saat login sukses. |
| **Bug baru #1** | ✅ FIXED | `needs_rehash` kini **upgrade-only** (`stored_rounds < target`); downgrade hash tidak mungkin lagi. Verify selalu memakai rounds tersimpan. |
| **Bug baru #2** | ✅ FIXED | Logika reset admin via pola `pbkdf2_sha256%$100%$` dihapus total dari `ensure_admin_exists`. |
| **K2** | ⚠️ PARTIAL | Admin bootstrap dibuat dengan `must_change_password=true` (hanya saat create; DB existing tidak dipaksa). Password default `admin` tetap ada untuk first-run. |
| **K3** | ✅ FIXED | Binding `client_id` & `redirect_uri` kini wajib dikirim dan harus identik (RFC 6749 §4.1.3), termasuk grant refresh_token. Terverifikasi: omit → 400. |
| **T2** | ✅ FIXED | JWT secret dibuat dari `crypto.random_hex(32)` (256-bit CSPRNG). Secret tidak pernah di-render di form realm-settings (value kosong = unchanged) dan halaman settings tidak lagi men-trigger generate. |
| **T3/R2 (parsial)** | ✅ FIXED | `M.uuid()` kini CSPRNG-backed (crypto/rand, fallback PRNG) → session ID, auth code, refresh token, dan semua identifier dapat entropi penuh. |
| **T6** | ✅ FIXED | `[server] base_url` baru di `config.toml` → diekspos sebagai `telamon.base_url`; issuer/discovery/introspect memakai `utils.get_base_url()`. Host header tidak dipercaya lagi (terverifikasi dengan Host palsu). |
| **R5** | ✅ FIXED | Deny path consent: state di-URL-encode, separator query `?` vs `&` dideteksi. |
| **S5 (events unbounded)** | ✅ FIXED | Cap 100 event di token.lua (×2) dan consent.lua (pola sama dengan logout.lua milik pengguna). |
| **Wildcard `@` bypass** | ✅ FIXED | Prefix wildcard whitelist menolak sisa URI yang mengandung `@` (userinfo trick), di global & client-specific. |
| **Default klien fail-open** | ✅ FIXED | Form clients default `redirect_uris` kosong (fail-closed); seed internal console dirubah `/account/*` & `/admin/*`; fallback tampilan bukan lagi `*`. |

## Belum Ditambal (backlog)

- **K4/K5/T1/T4/T5/T7/T8/S1–S7 sisanya** — belum disentuh patch ini.
- **Timing enumeration**: user tak dikenal cepat (±100 ms) vs salah password (±1 s di box ini) → pertimbangkan dummy-hash saat user miss.
- **Baseline performa**: setiap request me-parse ulang `utils.lua` (±2,4 s di box dev ini) — di luar scope keamanan.

## Verifikasi

1. `go build` + `go vet` bersih.
2. `test_oidc_client.py`: **seluruh step lolos** (discovery, PKCE, code exchange, JWT claims, userinfo, introspect, revoke, M2M).
3. Uji negatif binding: token exchange tanpa `client_id`/`redirect_uri` → 400; refresh tanpa `client_id` → 400; exchange valid → 200.
4. Hash admin termigrasi ke `atlas_pbkdf2$...` otomatis via login.
5. Issuer discovery mengikuti `base_url` config meski Host header dipalsukan.
6. Halaman admin/account tidak mengalami runtime error (302 ke login seperti semestinya).

## Catatan Git

Repo utama di-push **tanpa `public/`** (public/ akan masuk repo terpisah): `.gitignore` kini memuat `public/`.
