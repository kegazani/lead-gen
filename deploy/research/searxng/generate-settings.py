#!/usr/bin/env python3
"""Создаёт локальные настройки SearXNG с JSON и существующим proxy.

JSON является подмножеством YAML. Дополнительных Python dependencies не нужно.
Секрет и proxy URL пишутся только в ignored runtime-файл, не выводятся в stdout.
"""
import argparse
import json
import os
from pathlib import Path
import secrets


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--output", default="deploy/research/searxng/.runtime/settings.yml")
    args = parser.parse_args()
    target = Path(args.output)
    target.parent.mkdir(parents=True, exist_ok=True)
    previous = {}
    if target.exists():
        previous = json.loads(target.read_text())
    engines = [value.strip() for value in os.environ.get("RESEARCH_SEARXNG_ENGINES", "duckduckgo,brave").split(",") if value.strip()]
    if not engines:
        parser.error("RESEARCH_SEARXNG_ENGINES не должен быть пустым")
    outgoing = {
        "request_timeout": 8.0,
        "max_request_timeout": 15.0,
        "pool_connections": 8,
        "retries": 0,
        "max_redirects": 5,
    }
    proxy = os.environ.get("SEARXNG_OUTGOING_PROXY") or os.environ.get("HTTPS_PROXY") or os.environ.get("HTTP_PROXY")
    if proxy:
        outgoing["proxies"] = {"all://": [proxy]}
    if os.environ.get("SEARXNG_CA_BUNDLE"):
        outgoing["verify"] = os.environ["SEARXNG_CA_BUNDLE"]
    settings = {
        "use_default_settings": {"engines": {"keep_only": engines}},
        "general": {"debug": False, "instance_name": "Lead research metasearch"},
        "server": {
            "bind_address": "0.0.0.0", "port": 8080,
            "secret_key": os.environ.get("SEARXNG_SECRET") or previous.get("server", {}).get("secret_key") or secrets.token_hex(32),
            "limiter": False, "image_proxy": False,
        },
        "search": {"formats": ["html", "json"], "safe_search": 0},
        "outgoing": outgoing,
        "engines": [{"name": engine, "disabled": False} for engine in engines],
    }
    # Контейнерный uid читает bind mount; секрет не покидает локальный runtime.
    target.write_text(json.dumps(settings, ensure_ascii=False, indent=2) + "\n")
    target.chmod(0o644)
    print(f"Созданы настройки {target}; engines={','.join(engines)}; proxy={'configured' if proxy else 'environment default'}")


if __name__ == "__main__":
    main()
