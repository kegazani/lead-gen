#!/usr/bin/env bash
set -Eeuo pipefail
ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
SCRIPT="$ROOT/deploy/dev/current-request-crawl.sh"

bash -n "$SCRIPT"
python3 - "$SCRIPT" "$ROOT/deploy/dev/current-request-crawl-nginx.conf" "$ROOT/backend/cmd/lead-gen/dev_schema_verify_test.go" <<'PY'
import pathlib, sys
runner = pathlib.Path(sys.argv[1]).read_text()
proxy = pathlib.Path(sys.argv[2]).read_text()
verifier = pathlib.Path(sys.argv[3]).read_text()
required = (
    "lead-management,request-fulfillment,platform-governance",
    "-api-bind-host 0.0.0.0",
    "-console-password-file",
    "LEADGEN_DEV_VERIFY_GREEN_DSN_FILE",
    "LEADGEN_DEV_VERIFY_RED_DSN_FILE",
    "LEADGEN_DEV_VERIFY_MIGRATION_ROOT",
    "TestDevHarnessVerifySchemas",
    "-db-job stage-secrets",
    "-db-job verify-secrets",
    "--mount \"type=bind,source=$ROOT/deploy/tofu/.secrets,target=/run/secret-source,readonly\"",
    "postgres:17.6-alpine",
    "temporalio/auto-setup:1.26.2",
    "DEFAULT_NAMESPACE",
    "--exclude='._*'",
    "prepare_migration_snapshot",
    "target=/backend/contexts/request-fulfillment/migrations/green,readonly",
    "start|resume",
    "retry)",
    "stop",
    "proxy-reload)",
    "Volumes, сеть, images и demo data сохранены",
    "--env-file \"$STATE/app.env\"",
)
missing = [item for item in required if item not in runner]
if missing:
    raise SystemExit("harness: отсутствуют обязательные wiring элементы: " + ", ".join(missing))
if "migrate.Verify(" not in verifier:
    raise SystemExit("harness: отсутствует read-only migrate.Verify")
start_body = runner.split("start_harness() {", 1)[1].split("\n}", 1)[0]
if start_body.index("prepare_private_configuration") > start_body.index("check_network_owner"):
    raise SystemExit("harness: private/security preflight выполняется после создания ресурсов")
for forbidden in ("docker compose", "compose down", "volume rm", "system prune", "-v --remove-orphans"):
    if forbidden in runner:
        raise SystemExit("harness: обнаружена запрещённая команда управления пользовательским Compose/данными")
for route in ("/register", "/admin/", "/lead-console/", "/intake/"):
    if route not in proxy:
        raise SystemExit("proxy harness: отсутствует существующий UI route " + route)
if "actor=" in proxy or "X-Actor" in proxy or "X-Tenant" in proxy:
    raise SystemExit("proxy harness: запрещён обход серверной авторизации через actor/tenant заголовки")
if 'location /intake/ {\n        proxy_pass' in proxy:
    raise SystemExit("proxy harness: широкое проксирование request owner API запрещено")
if "location /intake/ {\n        return 404;" not in proxy:
    raise SystemExit("proxy harness: закрытый по умолчанию request-префикс отсутствует")
print("dev harness static checks: OK")
PY

case "${1:-static}" in
  static) ;;
  live)
    python3 - <<'PY'
import urllib.error, urllib.request
from html.parser import HTMLParser
base = "http://127.0.0.1:29180"
class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None
client = urllib.request.build_opener(NoRedirect)
def request(path, method="GET", body=None):
    req = urllib.request.Request(base + path, data=body, method=method,
        headers={"Content-Type": "application/json"} if body is not None else {})
    try:
        response = client.open(req, timeout=5)
    except urllib.error.HTTPError as error:
        response = error
    content_type = response.headers.get("Content-Type", "").lower()
    location = response.headers.get("Location", "")
    response.close()
    return response.code, content_type, location

status, _, _ = request("/intake/login")
if status != 200:
    raise SystemExit("live proxy: request login screen is unavailable")
with client.open(base + "/admin/console/login", timeout=5) as response:
    platform_html = response.read(1024 * 256).decode("utf-8", "replace")
class Forms(HTMLParser):
    actions = []
    def handle_starttag(self, tag, attrs):
        values = dict(attrs)
        if tag == "form":
            self.actions.append((values.get("method", "get").lower(), values.get("action", "")))
forms = Forms()
forms.feed(platform_html)
if ("post", "/admin/console/login") not in forms.actions:
    raise SystemExit("live proxy: platform login form action does not retain the public prefix")
status, content_type, location = request("/intake/requests?tenant_id=lccrc-demo")
if status != 404 or "json" in content_type:
    raise SystemExit("live proxy: anonymous request list API is reachable")
for suffix in ("/workflow?tenant_id=lccrc-demo", "/email-drafts?tenant_id=lccrc-demo",
               "/fulfillment-status?tenant_id=lccrc-demo", ""):
    status, content_type, _ = request("/intake/requests/proxy-regression-sentinel" + suffix)
    if status != 404 or "json" in content_type:
        raise SystemExit("live proxy: anonymous request owner read API is reachable")
status, content_type, _ = request("/intake/readyz")
if status != 404 or "json" in content_type:
    raise SystemExit("live proxy: internal readiness endpoint is published")
for method, path, body in (
    ("POST", "/intake/requests", b"{"),
    ("POST", "/intake/requests/proxy-regression-sentinel/workflow", b"{"),
    ("POST", "/intake/requests/proxy-regression-sentinel/validation/start", b"{}"),
):
    status, content_type, _ = request(path, method, body)
    if status != 404 or "json" in content_type:
        raise SystemExit("live proxy: raw request mutation API is published")
status, _, location = request("/intake/requests/proxy-regression-sentinel/approval?tenant_id=lccrc-demo&actor=admin")
if status != 303 or not location.endswith("/intake/login"):
    raise SystemExit("live proxy: operator UI did not retain server-side session authentication")
status, _, location = request("/intake/approvals?tenant_id=lccrc-demo&actor=admin")
if status != 303 or not location.endswith("/intake/login"):
    raise SystemExit("live proxy: actor query bypassed operator authentication")
print("live proxy checks: UI reachable; owner APIs blocked; anonymous UI remains behind session auth")
PY
    ;;
  *)
    printf '%s\n' "Использование: $0 {static|live}" >&2
    exit 2
    ;;
esac
