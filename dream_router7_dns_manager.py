#!/usr/bin/env python3
"""Create, update or delete a single local DNS A record on a UniFi gateway.

Usage:
  ./dream_router7_dns_manager.py [USER [PASSWORD]] HOSTNAME IP [--host 192.168.1.1] [--site default]
  ./dream_router7_dns_manager.py --delete [USER [PASSWORD]] HOSTNAME

If USER is omitted it is read from the UNIFI_USER env var, defaulting to "admin".
If PASSWORD is omitted it is read from the UNIFI_PASS env var, otherwise prompted.

USER/PASSWORD must be a *local* UniFi OS admin account (not a UI.com SSO/2FA
account). Self-contained: Python 3 standard library only.
"""
import argparse
import getpass
import http.cookiejar
import ipaddress
import json
import os
import ssl
import sys
import urllib.error
import urllib.request


class UniFi:
    def __init__(self, host, site):
        self.base = f"https://{host}"
        self.dns_url = f"{self.base}/proxy/network/v2/api/site/{site}/static-dns"
        self.csrf = None
        # The gateway uses a self-signed certificate.
        ctx = ssl.create_default_context()
        ctx.check_hostname = False
        ctx.verify_mode = ssl.CERT_NONE
        self.opener = urllib.request.build_opener(
            urllib.request.HTTPSHandler(context=ctx),
            urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()),
        )

    def request(self, method, url, body=None):
        headers = {"Content-Type": "application/json", "Accept": "application/json"}
        if self.csrf:
            headers["X-CSRF-Token"] = self.csrf
        data = json.dumps(body).encode() if body is not None else None
        req = urllib.request.Request(url, data=data, headers=headers, method=method)
        try:
            with self.opener.open(req, timeout=15) as resp:
                self.csrf = resp.headers.get("X-Updated-CSRF-Token") or resp.headers.get("X-CSRF-Token") or self.csrf
                raw = resp.read()
                return json.loads(raw) if raw else None
        except urllib.error.HTTPError as e:
            sys.exit(f"{method} {url} failed: HTTP {e.code} {e.read().decode(errors='replace')}")

    def login(self, username, password):
        self.request("POST", f"{self.base}/api/auth/login",
                     {"username": username, "password": password, "rememberMe": False})

    def list_records(self):
        return self.request("GET", self.dns_url) or []

    def create(self, record):
        return self.request("POST", self.dns_url, record)

    def update(self, record_id, record):
        return self.request("PUT", f"{self.dns_url}/{record_id}", record)

    def delete(self, record_id):
        return self.request("DELETE", f"{self.dns_url}/{record_id}")


def main():
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter,
                                 usage="%(prog)s [-h] [--host HOST] [--site SITE] [USER [PASSWORD]] HOSTNAME IP\n"
                                       "       %(prog)s [-h] [--host HOST] [--site SITE] --delete [USER [PASSWORD]] HOSTNAME")
    ap.add_argument("args", nargs="+", metavar="[USER [PASSWORD]] HOSTNAME [IP]")
    ap.add_argument("--delete", action="store_true", help="delete the A record for HOSTNAME instead of creating it")
    ap.add_argument("--host", default="192.168.1.1")
    ap.add_argument("--site", default="default")
    args = ap.parse_args()

    # The last one (or two) positionals are HOSTNAME [IP]; anything before is USER [PASSWORD].
    positional = list(args.args)
    ip_arg = None if args.delete else (positional.pop() if len(positional) >= 2 else None)
    if not positional or len(positional) > 3 or (ip_arg is None and not args.delete):
        ap.error("expected --delete [USER [PASSWORD]] HOSTNAME" if args.delete
                 else "expected [USER [PASSWORD]] HOSTNAME IP")
    hostname = positional.pop()
    user = positional[0] if positional else os.environ.get("UNIFI_USER") or "admin"
    password = positional[1] if len(positional) == 2 else None

    name = hostname.strip().rstrip(".")
    if ip_arg is not None:
        try:
            ip = str(ipaddress.IPv4Address(ip_arg.strip()))
        except ValueError:
            sys.exit(f"invalid IPv4 address {ip_arg!r}")
    if password is None:
        password = os.environ.get("UNIFI_PASS") or getpass.getpass(f"Password for {user}: ")

    api = UniFi(args.host, args.site)
    api.login(user, password)
    current = next((r for r in api.list_records()
                    if r.get("record_type") == "A" and r.get("key") == name), None)

    if args.delete:
        if current is None:
            sys.exit(f"no A record found for {name}")
        api.delete(current["_id"])
        print(f"deleted {name} (was {current.get('value')})")
        return

    record = {"key": name, "record_type": "A", "value": ip, "enabled": True,
              "ttl": 0, "port": 0, "priority": 0, "weight": 0}
    if current is None:
        api.create(record)
        print(f"created {name} -> {ip}")
    elif current.get("value") != ip or not current.get("enabled", True):
        api.update(current["_id"], {**current, **record})
        print(f"updated {name} -> {ip} (was {current.get('value')})")
    else:
        print(f"unchanged {name} -> {ip}")


if __name__ == "__main__":
    main()
