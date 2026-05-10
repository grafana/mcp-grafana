"""End-to-end test for the OAuth 2.1 / Mode C flow.

This test drives the AS endpoints directly (DCR, /authorize, /callback,
/bootstrap, /token) and then makes a tools/list call with the resulting
MCP access token to verify the credential is passed through to Grafana.

The Grafana container is the same one the rest of the integration suite uses;
its admin/admin credentials are used to provision the SA token that gets pasted
into /bootstrap.
"""
import base64
import hashlib
import os
import secrets
import urllib.parse

import pytest
import requests


# Override the async autouse cleanup_sessions fixture from conftest with a
# sync no-op. This file's tests are sync (driving HTTP via requests), and the
# conftest fixture is intended for async MCP-client tests.
@pytest.fixture(autouse=True)
def cleanup_sessions():
    yield


def _pkce_pair():
    verifier = secrets.token_urlsafe(64)
    challenge = base64.urlsafe_b64encode(hashlib.sha256(verifier.encode()).digest()).rstrip(b"=").decode()
    return verifier, challenge


def _provision_grafana_sa_token():
    """Create a Grafana service account + token using admin/admin and return the token."""
    base = os.environ.get("GRAFANA_URL", "http://localhost:3000")
    auth = ("admin", "admin")
    sa = requests.post(f"{base}/api/serviceaccounts", auth=auth, json={"name": f"mcp-test-{secrets.token_hex(4)}", "role": "Admin"}).json()
    sa_id = sa["id"]
    tok = requests.post(f"{base}/api/serviceaccounts/{sa_id}/tokens", auth=auth, json={"name": f"tok-{secrets.token_hex(4)}"}).json()
    return tok["key"]


def test_full_oauth_flow(auth_mcp):
    base = auth_mcp["base"]
    verifier, challenge = _pkce_pair()

    # 1. DCR
    dcr = requests.post(f"{base}/register", json={
        "client_name": "test client",
        "redirect_uris": ["http://localhost:55555/cb"],
    }, allow_redirects=False)
    assert dcr.status_code == 201, dcr.text
    client_id = dcr.json()["client_id"]

    # 2. /authorize -> 302 to upstream
    auth_resp = requests.get(f"{base}/authorize", params={
        "response_type": "code",
        "client_id": client_id,
        "redirect_uri": "http://localhost:55555/cb",
        "code_challenge": challenge,
        "code_challenge_method": "S256",
        "state": "client-state",
    }, allow_redirects=False)
    assert auth_resp.status_code == 302
    upstream_url = auth_resp.headers["Location"]

    # 3. Drive the upstream IdP -> 302 back to /callback
    upstream_resp = requests.get(upstream_url, allow_redirects=False)
    assert upstream_resp.status_code == 302
    callback_url = upstream_resp.headers["Location"]

    # 4. Hit /callback -> 302 to /bootstrap?flow=...
    cb = requests.get(callback_url, allow_redirects=False)
    assert cb.status_code == 302, f"callback status={cb.status_code} body={cb.text}"
    boot = urllib.parse.urlparse(cb.headers["Location"])
    boot_query = urllib.parse.parse_qs(boot.query)
    assert "flow" in boot_query, f"callback redirected to {cb.headers['Location']!r}"
    flow = boot_query["flow"][0]

    # 5. Provision a Grafana SA token, paste into /bootstrap.
    sa_token = _provision_grafana_sa_token()
    boot_resp = requests.post(f"{base}/bootstrap", data={"flow": flow, "grafana_token": sa_token}, allow_redirects=False)
    assert boot_resp.status_code == 302, boot_resp.text
    final = urllib.parse.urlparse(boot_resp.headers["Location"])
    assert final.netloc == "localhost:55555"
    code = urllib.parse.parse_qs(final.query)["code"][0]

    # 6. Redeem at /token
    tok = requests.post(f"{base}/token", data={
        "grant_type": "authorization_code",
        "code": code,
        "redirect_uri": "http://localhost:55555/cb",
        "client_id": client_id,
        "code_verifier": verifier,
    })
    assert tok.status_code == 200, tok.text
    access_token = tok.json()["access_token"]

    # 7. Make a tools/list call with the MCP token. The session's SA token
    # gets used to call Grafana, so the response should include real tools.
    list_resp = requests.post(f"{base}/mcp", json={
        "jsonrpc": "2.0", "id": 1, "method": "tools/list",
    }, headers={
        "Authorization": f"Bearer {access_token}",
        "Content-Type": "application/json",
    })
    assert list_resp.status_code == 200, list_resp.text
    body = list_resp.json()
    assert "result" in body, body
    tool_names = [t["name"] for t in body["result"]["tools"]]
    assert "search_dashboards" in tool_names

    # 8. Refresh test
    refresh = tok.json()["refresh_token"]
    rotated = requests.post(f"{base}/token", data={
        "grant_type": "refresh_token", "refresh_token": refresh, "client_id": client_id,
    })
    assert rotated.status_code == 200
    assert rotated.json()["refresh_token"] != refresh

    # 9. Old token must no longer work for refresh
    again = requests.post(f"{base}/token", data={
        "grant_type": "refresh_token", "refresh_token": refresh, "client_id": client_id,
    })
    assert again.status_code == 400
