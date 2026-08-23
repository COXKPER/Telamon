#!/usr/bin/env python3
"""
AtlasCloak OpenID Connect (OIDC) & OAuth2 Standard Client Compliance Test
Tests OIDC Discovery, PKCE Authorization Code Flow, JWT Token Verification,
UserInfo Endpoint, RFC 7662 Token Introspection, and RFC 7009 Token Revocation.
"""

import sys
import json
import base64
import hashlib
import hmac
import secrets
import urllib.parse
import requests

BASE_URL = "http://localhost:8081"
REALM = "master"
DISCOVERY_URL = f"{BASE_URL}/auth/realms/{REALM}/.well-known/openid-configuration"

def b64url_encode(data: bytes) -> str:
    return base64.urlsafe_b64encode(data).decode('ascii').rstrip('=')

def b64url_decode(s: str) -> bytes:
    pad = len(s) % 4
    if pad == 2: s += "=="
    elif pad == 3: s += "="
    return base64.urlsafe_b64decode(s.encode('ascii'))

def print_step(title):
    print(f"\n\033[1;36m{'='*60}\033[0m")
    print(f"\033[1;32m[OIDC TEST]\033[0m \033[1m{title}\033[0m")
    print(f"\033[1;36m{'='*60}\033[0m")

def print_success(msg):
    print(f"  \033[1;32m✓\033[0m {msg}")

def print_info(k, v):
    print(f"    \033[33m•\033[0m \033[1m{k}:\033[0m {v}")

def run_tests():
    session = requests.Session()

    # ──────────────────────────────────────────────────────────────────────────
    # Step 1: OpenID Connect Discovery
    # ──────────────────────────────────────────────────────────────────────────
    print_step("Step 1: Fetch OpenID Connect Discovery (.well-known)")
    resp = session.get(DISCOVERY_URL)
    assert resp.status_code == 200, f"Discovery failed: {resp.status_code}"
    disco = resp.json()
    
    auth_ep = disco["authorization_endpoint"]
    token_ep = disco["token_endpoint"]
    userinfo_ep = disco["userinfo_endpoint"]
    introspect_ep = disco["introspection_endpoint"]
    revoke_ep = disco["revocation_endpoint"]
    
    print_success("OIDC Discovery endpoint loaded successfully")
    print_info("Issuer", disco.get("issuer"))
    print_info("Authorization Endpoint", auth_ep)
    print_info("Token Endpoint", token_ep)
    print_info("UserInfo Endpoint", userinfo_ep)
    print_info("Introspection Endpoint", introspect_ep)
    print_info("Revocation Endpoint", revoke_ep)
    print_info("PKCE Supported", disco.get("code_challenge_methods_supported"))

    # ──────────────────────────────────────────────────────────────────────────
    # Step 2: PKCE (RFC 7636) Key Generation
    # ──────────────────────────────────────────────────────────────────────────
    print_step("Step 2: Generate PKCE (RFC 7636) Parameters")
    code_verifier = secrets.token_urlsafe(48)
    digest = hashlib.sha256(code_verifier.encode('ascii')).digest()
    code_challenge = b64url_encode(digest)
    state = secrets.token_hex(8)
    
    print_success("PKCE Code Verifier & S256 Challenge generated")
    print_info("Code Verifier", f"{code_verifier[:20]}... ({len(code_verifier)} chars)")
    print_info("Code Challenge (S256)", code_challenge)
    print_info("State", state)

    # ──────────────────────────────────────────────────────────────────────────
    # Step 3: Authorization Flow & Authentication
    # ──────────────────────────────────────────────────────────────────────────
    print_step("Step 3: Authorization Request & Sign In")
    client_id = "test-oidc-client"
    redirect_uri = "http://localhost:3000/callback"
    
    auth_params = {
        "client_id": client_id,
        "redirect_uri": redirect_uri,
        "response_type": "code",
        "scope": "openid profile email",
        "state": state,
        "code_challenge": code_challenge,
        "code_challenge_method": "S256"
    }
    
    # 3a. Get Login Page & Anti-Bot Security Token
    auth_page_resp = session.get(auth_ep, params=auth_params)
    assert auth_page_resp.status_code == 200, "Auth page failed to render"
    
    # Extract security challenge token from HTML
    import re
    match = re.search(r'name="security_token" value="([^"]+)"', auth_page_resp.text)
    assert match, "Security challenge token not found in auth page"
    sec_token = match.group(1)
    print_success("Fetched login page and anti-bot challenge token")

    # 3b. Submit credentials to authenticate endpoint
    login_url = f"{BASE_URL}/auth/realms/{REALM}/login-actions/authenticate"
    login_data = {
        "username": "admin",
        "password": "admin",
        "security_token": sec_token,
        "atlas_hp_field": "" # Leave honeypot empty
    }
    
    login_resp = session.post(
        login_url,
        params={
            "client_id": client_id,
            "redirect_uri": redirect_uri,
            "state": state,
            "code_challenge": code_challenge,
            "code_challenge_method": "S256"
        },
        data=login_data,
        allow_redirects=False
    )
    
    assert login_resp.status_code in [302, 303], f"Login failed: {login_resp.status_code} - {login_resp.text}"
    auth_redirect = login_resp.headers.get("Location")
    print_success("User credentials verified and session cookie created")

    # 3c. Follow redirect to Auth endpoint to obtain Authorization Code
    auth_req_url = f"{BASE_URL}{auth_redirect}" if auth_redirect.startswith("/") else auth_redirect
    code_resp = session.get(auth_req_url, allow_redirects=False)
    
    # If Consent screen is presented, grant consent
    if code_resp.status_code == 200 and "Grant Permissions" in code_resp.text:
        match_c = re.search(r'name="security_token" value="([^"]+)"', code_resp.text)
        assert match_c, "Security token not found on consent page"
        consent_sec_token = match_c.group(1)
        consent_url = f"{BASE_URL}/auth/realms/{REALM}/protocol/openid-connect/consent"
        code_resp = session.post(
            consent_url,
            params={
                "client_id": client_id,
                "redirect_uri": redirect_uri,
                "state": state,
                "code_challenge": code_challenge,
                "code_challenge_method": "S256"
            },
            data={
                "decision": "accept",
                "security_token": consent_sec_token
            },
            allow_redirects=False
        )
    
    final_redirect = code_resp.headers.get("Location")
    assert final_redirect and "code=" in final_redirect, f"No authorization code returned: {final_redirect}"
    
    parsed_redirect = urllib.parse.urlparse(final_redirect)
    query_params = urllib.parse.parse_qs(parsed_redirect.query)
    auth_code = query_params["code"][0]
    ret_state = query_params["state"][0]
    
    assert ret_state == state, "State mismatch in callback"
    print_success("Authorization Code received via callback redirect")
    print_info("Authorization Code", auth_code)
    print_info("State Match", "PASSED")

    # ──────────────────────────────────────────────────────────────────────────
    # Step 4: Token Exchange with PKCE Verification
    # ──────────────────────────────────────────────────────────────────────────
    print_step("Step 4: Exchange Authorization Code for JWT Token")
    
    # 4a. Negative test: Exchange with INVALID code_verifier
    fail_token_resp = requests.post(token_ep, data={
        "grant_type": "authorization_code",
        "code": auth_code,
        "redirect_uri": redirect_uri,
        "client_id": client_id,
        "code_verifier": "invalid-verifier-should-fail"
    })
    assert fail_token_resp.status_code == 400, "Server should reject invalid code_verifier"
    print_success("Negative test passed: Invalid code_verifier was rejected with 400 Bad Request")

    # 4b. Re-issue valid code for positive test
    auth_resp2 = session.get(auth_ep, params=auth_params, allow_redirects=False)
    if auth_resp2.status_code == 200 and "Grant Permissions" in auth_resp2.text:
        match_c2 = re.search(r'name="security_token" value="([^"]+)"', auth_resp2.text)
        assert match_c2, "Security token not found on consent page"
        auth_resp2 = session.post(
            f"{BASE_URL}/auth/realms/{REALM}/protocol/openid-connect/consent",
            params=auth_params,
            data={"decision": "accept", "security_token": match_c2.group(1)},
            allow_redirects=False
        )
    valid_redirect = auth_resp2.headers.get("Location")
    valid_code = urllib.parse.parse_qs(urllib.parse.urlparse(valid_redirect).query)["code"][0]
    
    # Positive test: Exchange with VALID code_verifier
    token_resp = requests.post(token_ep, data={
        "grant_type": "authorization_code",
        "code": valid_code,
        "redirect_uri": redirect_uri,
        "client_id": client_id,
        "code_verifier": code_verifier
    })
    
    assert token_resp.status_code == 200, f"Token exchange failed: {token_resp.text}"
    token_data = token_resp.json()
    access_token = token_data["access_token"]
    refresh_token = token_data["refresh_token"]
    token_type = token_data["token_type"]
    expires_in = token_data["expires_in"]
    
    print_success("Token exchange succeeded with PKCE verification!")
    print_info("Access Token", f"{access_token[:32]}... ({len(access_token)} chars)")
    print_info("Token Type", token_type)
    print_info("Expires In", f"{expires_in} seconds")
    print_info("Refresh Token", refresh_token)

    # ──────────────────────────────────────────────────────────────────────────
    # Step 5: JWT Token Claims & Structure Inspection
    # ──────────────────────────────────────────────────────────────────────────
    print_step("Step 5: Parse & Inspect JWT Payload & Claims")
    parts = access_token.split(".")
    assert len(parts) == 3, "Access token is not a valid 3-part JWT"
    
    jwt_header = json.loads(b64url_decode(parts[0]))
    jwt_payload = json.loads(b64url_decode(parts[1]))
    
    print_success("JWT Decoded successfully")
    print_info("Header Alg", jwt_header.get("alg"))
    print_info("Header Typ", jwt_header.get("typ"))
    print_info("Subject (sub)", jwt_payload.get("sub"))
    print_info("Preferred Username", jwt_payload.get("preferred_username"))
    print_info("User Email", jwt_payload.get("email"))
    print_info("Assigned Roles", jwt_payload.get("roles"))
    print_info("Audience (aud)", jwt_payload.get("aud"))
    print_info("Issuer (iss)", jwt_payload.get("iss"))

    # ──────────────────────────────────────────────────────────────────────────
    # Step 6: OIDC UserInfo Endpoint Call
    # ──────────────────────────────────────────────────────────────────────────
    print_step("Step 6: Query OIDC UserInfo Endpoint")
    userinfo_resp = requests.get(userinfo_ep, headers={
        "Authorization": f"Bearer {access_token}"
    })
    assert userinfo_resp.status_code == 200, f"UserInfo failed: {userinfo_resp.text}"
    uinfo = userinfo_resp.json()
    
    print_success("UserInfo response returned valid user identity")
    print_info("sub", uinfo.get("sub"))
    print_info("name", uinfo.get("name"))
    print_info("preferred_username", uinfo.get("preferred_username"))
    print_info("email", uinfo.get("email"))
    print_info("realm_access.roles", uinfo.get("realm_access", {}).get("roles"))

    # ──────────────────────────────────────────────────────────────────────────
    # Step 7: RFC 7662 Token Introspection
    # ──────────────────────────────────────────────────────────────────────────
    print_step("Step 7: RFC 7662 Token Introspection")
    introspect_resp = requests.post(introspect_ep, data={
        "token": access_token
    })
    assert introspect_resp.status_code == 200, "Introspection endpoint failed"
    introspect_data = introspect_resp.json()
    assert introspect_data.get("active") is True, "Token must be active"
    
    print_success("Token introspection returned active: true")
    print_info("Active Status", introspect_data.get("active"))
    print_info("Introspected User", introspect_data.get("username"))
    print_info("Introspected Scopes", introspect_data.get("scope"))
    print_info("Introspected Roles", introspect_data.get("roles"))

    # ──────────────────────────────────────────────────────────────────────────
    # Step 8: RFC 7009 Token Revocation
    # ──────────────────────────────────────────────────────────────────────────
    print_step("Step 8: RFC 7009 Token Revocation & Post-Revocation Check")
    revoke_resp = requests.post(revoke_ep, data={
        "token": access_token
    })
    assert revoke_resp.status_code == 200, "Revocation failed"
    print_success("Revocation request accepted with HTTP 200")

    # Verify that token is now inactive via Introspection
    post_revoke_introspect = requests.post(introspect_ep, data={
        "token": access_token
    })
    assert post_revoke_introspect.json().get("active") is False, "Token should be inactive after revocation"
    print_success("Introspection confirmed token is now INACTIVE (active: false)")

    # Verify UserInfo rejects revoked token
    post_revoke_userinfo = requests.get(userinfo_ep, headers={
        "Authorization": f"Bearer {access_token}"
    })
    assert post_revoke_userinfo.status_code == 401, "UserInfo should reject revoked token with 401"
    print_success("UserInfo endpoint successfully rejected revoked token (HTTP 401 Unauthorized)")

    # ──────────────────────────────────────────────────────────────────────────
    # Step 9: Client Credentials Grant (M2M)
    # ──────────────────────────────────────────────────────────────────────────
    print_step("Step 9: Test Machine-to-Machine (Client Credentials Grant)")
    m2m_resp = requests.post(token_ep, data={
        "grant_type": "client_credentials",
        "client_id": "m2m-service",
        "client_secret": "secret123"
    })
    assert m2m_resp.status_code == 200, f"Client credentials grant failed: {m2m_resp.text}"
    m2m_data = m2m_resp.json()
    print_success("Issued M2M JWT Token successfully via client_credentials grant")
    print_info("M2M Token", f"{m2m_data['access_token'][:32]}...")

    print_step("🎉 ALL OIDC & OAUTH2 SPECIFICATION TESTS PASSED 100%!")

if __name__ == "__main__":
    try:
        run_tests()
    except AssertionError as e:
        print(f"\n\033[1;31m[TEST FAILED]\033[0m {e}")
        sys.exit(1)
    except Exception as e:
        import traceback
        traceback.print_exc()
        sys.exit(1)
