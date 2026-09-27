#!/usr/bin/env python3
"""Create, update or delete local DNS A records on a UniFi gateway.

Usage:
  ./dream_router7_dns_manager.py [USER [PASSWORD]] HOSTNAME IP [--host 192.168.1.1] [--site default]
  ./dream_router7_dns_manager.py --delete [USER [PASSWORD]] HOSTNAME
  ./dream_router7_dns_manager.py --csv FILE [USER [PASSWORD]]
  ./dream_router7_dns_manager.py --delete --csv FILE [USER [PASSWORD]]

--csv FILE adds or updates every "hostname,ip" line in FILE with a single login.
With --delete it deletes every hostname in FILE instead; the IP column is then
optional, and when present the record is only deleted if it still has that IP.
--dry-run shows what would change without changing anything.

If USER is omitted it is read from the UNIFI_USER env var, defaulting to "admin".
If PASSWORD is omitted it is read from the UNIFI_PASS env var, otherwise prompted.

USER/PASSWORD must be a *local* UniFi OS admin account (not a UI.com SSO/2FA
account). Self-contained: Python 3 standard library only.
"""
import argparse
import csv
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


def read_csv(path, ip_optional=False):
    """Return [(hostname, ip)] from a "hostname,ip" file, exiting on the first bad line.

    With ip_optional, lines may be just "hostname" and ip is None for those.
    """
    records, seen = [], set()
    try:
        f = open(path, newline="")
    except OSError as e:
        sys.exit(f"cannot read {path}: {e.strerror}")
    with f:
        for lineno, row in enumerate(csv.reader(f, skipinitialspace=True), 1):
            if not row or not "".join(row).strip() or row[0].strip().startswith("#"):
                continue
            if len(row) != 2 and not (ip_optional and len(row) == 1):
                expected = "hostname[,ip]" if ip_optional else "hostname,ip"
                sys.exit(f"{path}:{lineno}: expected '{expected}', got {','.join(row)!r}")
            name = row[0].strip().rstrip(".")
            if not name:
                sys.exit(f"{path}:{lineno}: missing hostname")
            if ip_optional and (len(row) == 1 or not row[1].strip()):
                ip = None
            else:
                ip = parse_ip(row[1], f"{path}:{lineno}: ")
            if name in seen:
                sys.exit(f"{path}:{lineno}: duplicate hostname {name}")
            seen.add(name)
            records.append((name, ip))
    if not records:
        sys.exit(f"{path}: no records found")
    return records


def parse_ip(value, prefix=""):
    try:
        return str(ipaddress.IPv4Address(value.strip()))
    except ValueError:
        sys.exit(f"{prefix}invalid IPv4 address {value!r}")


def find_record(records, name):
    return next((r for r in records if r.get("record_type") == "A" and r.get("key") == name), None)


def upsert(api, records, name, ip, dry_run):
    """Create or update the A record for name; return "created", "updated" or "unchanged"."""
    would = "would " if dry_run else ""
    current = find_record(records, name)
    record = {"key": name, "record_type": "A", "value": ip, "enabled": True,
              "ttl": 0, "port": 0, "priority": 0, "weight": 0}
    if current is None:
        if not dry_run:
            api.create(record)
        print(f"{would}create {name} -> {ip}" if dry_run else f"created {name} -> {ip}")
        return "created"
    if current.get("value") != ip or not current.get("enabled", True):
        if not dry_run:
            api.update(current["_id"], {**current, **record})
        verb = f"{would}update" if dry_run else "updated"
        print(f"{verb} {name} -> {ip} (was {current.get('value')})")
        return "updated"
    print(f"unchanged {name} -> {ip}")
    return "unchanged"


def delete(api, records, name, expected_ip, dry_run, missing_ok):
    """Delete the A record for name; return "deleted", "not found" or "skipped".

    If expected_ip is given, the record is only deleted while it still has that IP.
    """
    current = find_record(records, name)
    if current is None:
        if not missing_ok:
            sys.exit(f"no A record found for {name}")
        print(f"not found {name}")
        return "not found"
    if expected_ip is not None and current.get("value") != expected_ip:
        print(f"skipped {name} (is {current.get('value')}, file says {expected_ip})")
        return "skipped"
    if dry_run:
        print(f"would delete {name} (was {current.get('value')})")
    else:
        api.delete(current["_id"])
        print(f"deleted {name} (was {current.get('value')})")
    return "deleted"


def main():
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter,
                                 usage="%(prog)s [-h] [--host HOST] [--site SITE] [--dry-run] [USER [PASSWORD]] HOSTNAME IP\n"
                                       "       %(prog)s [-h] [--host HOST] [--site SITE] [--dry-run] --delete [USER [PASSWORD]] HOSTNAME\n"
                                       "       %(prog)s [-h] [--host HOST] [--site SITE] [--dry-run] [--delete] --csv FILE [USER [PASSWORD]]")
    ap.add_argument("args", nargs="*", metavar="[USER [PASSWORD]] HOSTNAME [IP]")
    ap.add_argument("--delete", action="store_true", help="delete the A record for HOSTNAME instead of creating it")
    ap.add_argument("--csv", metavar="FILE", help='add or update every "hostname,ip" line in FILE with a single login '
                                                 '(with --delete: delete every hostname in FILE)')
    ap.add_argument("--dry-run", action="store_true", help="show what would change without changing anything")
    ap.add_argument("--host", default="192.168.1.1")
    ap.add_argument("--site", default="default")
    args = ap.parse_args()

    positional = list(args.args)
    if args.csv is not None:
        if len(positional) > 2:
            ap.error(f"expected {'--delete ' if args.delete else ''}--csv FILE [USER [PASSWORD]]")
        batch = read_csv(args.csv, ip_optional=args.delete)
    else:
        # The last one (or two) positionals are HOSTNAME [IP]; anything before is USER [PASSWORD].
        ip_arg = None if args.delete else (positional.pop() if len(positional) >= 2 else None)
        if not positional or len(positional) > 3 or (ip_arg is None and not args.delete):
            ap.error("expected --delete [USER [PASSWORD]] HOSTNAME" if args.delete
                     else "expected [USER [PASSWORD]] HOSTNAME IP")
        name = positional.pop().strip().rstrip(".")
        if ip_arg is not None:
            ip = parse_ip(ip_arg)
    user = positional[0] if positional else os.environ.get("UNIFI_USER") or "admin"
    password = positional[1] if len(positional) == 2 else None
    if password is None:
        password = os.environ.get("UNIFI_PASS") or getpass.getpass(f"Password for {user}: ")

    api = UniFi(args.host, args.site)
    api.login(user, password)
    records = api.list_records()

    if args.csv is not None and args.delete:
        counts = {"deleted": 0, "not found": 0, "skipped": 0}
        for name, ip in batch:
            counts[delete(api, records, name, ip, args.dry_run, missing_ok=True)] += 1
        summary = f"deleted {counts['deleted']}, not found {counts['not found']}, skipped {counts['skipped']}"
        print(f"\ndry run: would {summary.replace('deleted', 'delete', 1)}" if args.dry_run else f"\n{summary}")
        return

    if args.csv is not None:
        counts = {"created": 0, "updated": 0, "unchanged": 0}
        for name, ip in batch:
            counts[upsert(api, records, name, ip, args.dry_run)] += 1
        if args.dry_run:
            print(f"\ndry run: would create {counts['created']}, would update {counts['updated']}, "
                  f"unchanged {counts['unchanged']}")
        else:
            print(f"\ncreated {counts['created']}, updated {counts['updated']}, unchanged {counts['unchanged']}")
        return

    if args.delete:
        delete(api, records, name, None, args.dry_run, missing_ok=False)
        return

    upsert(api, records, name, ip, args.dry_run)


if __name__ == "__main__":
    main()
