# Dream Router 7 DNS manager

If you have any experience using a Ubiquiti Edgerouter and local DNS, then you are in
for a surprise on the Dream Router. On the edgerouter you could add a ssh public
key, login passwordless, and add and remove dns entries easily, very easily scripted.


`dream_router7_dns_manager` creates, updates and deletes local DNS **A records** on a
UniFi Dream Router 7 (or other UniFi OS gateway) from the command line, one at a time or
in bulk from a CSV file.

Records are created through the UniFi Network application's own API, so they are
exactly the same as records added under **Settings → Routing → DNS** in the web UI:
they appear there, survive reboots and firmware updates, and are pushed to the
router's DNS server within a few seconds.

It comes in two versions with identical arguments, output and exit codes:

| Version | File | Needs |
|---|---|---|
| Python | `dream_router7_dns_manager.py` | Python 3 only (standard library, no packages) |
| Go | `main.go` → `dream_router7_dns_manager` binary | Go to build; the binary runs on its own |

The examples below use the Python script. For the Go version, replace
`./dream_router7_dns_manager.py` with `./dream_router7_dns_manager`.

## Requirements

- Python 3 (the one that ships with macOS is fine), or Go 1.26+ to build the Go version.
- A **local** UniFi OS admin account on the router, with permission to change network
  settings. A UI.com cloud account with SSO/2FA will not work. Create one under
  **Settings → Admins & Users** if needed.

## Building the Go version

```bash
go build -o dream_router7_dns_manager .
./dream_router7_dns_manager --help
```

The binary is ignored by git (see `.gitignore`). Cross-compile for another machine with
e.g. `GOOS=linux GOARCH=arm64 go build -o dream_router7_dns_manager .`.

## Usage

```
./dream_router7_dns_manager.py [USER [PASSWORD]] HOSTNAME IP
./dream_router7_dns_manager.py --delete [USER [PASSWORD]] HOSTNAME
./dream_router7_dns_manager.py --csv FILE [USER [PASSWORD]]
./dream_router7_dns_manager.py --delete --csv FILE [USER [PASSWORD]]
```

| Option | Default | Meaning |
|---|---|---|
| `USER` | `$UNIFI_USER`, else `admin` | Local UniFi OS account to log in with |
| `PASSWORD` | `$UNIFI_PASS`, else prompted | Password for that account |
| `--delete` | off | Delete instead of create/update |
| `--csv FILE` | — | Process every line of `FILE` with a single login |
| `--dry-run` | off | Show what would change without changing anything |
| `--host` | `192.168.1.1` | Router address |
| `--site` | `default` | UniFi Network site name |

Options can appear anywhere on the command line.

Exit status: `0` success, `1` error (bad input, login failure, record not found for a
single `--delete`), `2` usage error.

## Examples

### Add a record

Log in as `admin` and be prompted for the password (nothing is echoed):

```bash
./dream_router7_dns_manager.py nas.home.internal 192.168.1.50
# Password for admin:
# created nas.home.internal -> 192.168.1.50
```

Use a different account:

```bash
./dream_router7_dns_manager.py dnsadmin printer.home.internal 192.168.1.60
```

### Change a record's IP

Running the same command with a new IP updates the existing record. It never creates a
duplicate:

```bash
./dream_router7_dns_manager.py nas.home.internal 192.168.1.51
# updated nas.home.internal -> 192.168.1.51 (was 192.168.1.50)
```

Re-running with the IP it already has changes nothing:

```bash
./dream_router7_dns_manager.py nas.home.internal 192.168.1.51
# unchanged nas.home.internal -> 192.168.1.51
```

### Delete a record

```bash
./dream_router7_dns_manager.py --delete nas.home.internal
# deleted nas.home.internal (was 192.168.1.51)

./dream_router7_dns_manager.py --delete nas.home.internal
# no A record found for nas.home.internal      (exit status 1)
```

### Preview a change first

Add `--dry-run` to any command to see what it would do. Nothing is changed:

```bash
./dream_router7_dns_manager.py --dry-run nas.home.internal 192.168.1.52
# would update nas.home.internal -> 192.168.1.52 (was 192.168.1.51)
```

### Avoid typing the password every time

Set it once for the current terminal session. `read -s` keeps it off the screen and out
of your shell history:

```bash
read -s UNIFI_PASS; export UNIFI_PASS
./dream_router7_dns_manager.py media.home.internal 192.168.1.70
./dream_router7_dns_manager.py camera.home.internal 192.168.1.71
```

Avoid passing the password as an argument (`USER PASSWORD HOSTNAME IP`) except for quick
tests, because it ends up in your shell history and is visible in the process list.

### Add many records from a CSV file

Put one `hostname,ip` pair per line in a file. Blank lines and lines starting with `#`
are ignored, and spaces after the comma are fine:

```
# records.csv
nas.home.internal,192.168.1.50
printer.home.internal,192.168.1.60
media.home.internal, 192.168.1.70
```

Preview, then apply:

```bash
./dream_router7_dns_manager.py --csv records.csv --dry-run
# would create nas.home.internal -> 192.168.1.50
# would create printer.home.internal -> 192.168.1.60
# unchanged media.home.internal -> 192.168.1.70
#
# dry run: would create 2, would update 0, unchanged 1

./dream_router7_dns_manager.py --csv records.csv
# ...
# created 2, updated 0, unchanged 1
```

The whole file is checked before logging in: a bad IP address, a duplicate hostname or
a malformed line stops the run with `records.csv:LINE: ...` and nothing is changed.
Existing records with the right IP are reported as `unchanged`, so it is safe to re-run
after adding lines to the file.

### Delete many records from a CSV file

With `--delete`, each line can be just a hostname, or `hostname,ip`, so the same file
used to add records can be used to remove them:

```
# old.csv
nas.home.internal
printer.home.internal,192.168.1.60
media.home.internal,192.168.1.99
camera.home.internal
```

```bash
./dream_router7_dns_manager.py --delete --csv old.csv
# deleted nas.home.internal (was 192.168.1.50)
# deleted printer.home.internal (was 192.168.1.60)
# skipped media.home.internal (is 192.168.1.70, file says 192.168.1.99)
# not found camera.home.internal
#
# deleted 2, not found 1, skipped 1
```

When a line includes an IP, the record is only deleted if it still has that IP. This
protects records that were changed since the file was written. Missing records are
reported and skipped, so re-running is harmless (exit status `0`).

### Loop over a file in the shell

You can also run the tool once per line from a shell loop, e.g. to combine it with other
commands. Set `UNIFI_PASS` first (see above) so you are not prompted for every line:

```bash
while IFS=, read -r host ip; do
  ./dream_router7_dns_manager.py "$host" "${ip// /}"
  sleep 20    # stay under the router's default login rate limit
done < records.csv
```

Unlike `--csv`, this logs in **once per record**, so with the router's default settings
it runs into the login rate limit (see below) unless you pause between calls. The
`sleep 20` keeps it safely under the default limit, but it makes the loop slow (about
15 minutes for 45 records). If you have raised the limit on your router, you can shorten
or drop the `sleep`. For more than a few records, `--csv` is faster and checks the whole
file before changing anything.

### Login rate limiting

By default the router allows **5 successful logins per minute**. Each run of the tool
logs in once, so the sixth run within a minute fails with:

```
POST https://192.168.1.1/api/auth/login failed: HTTP 429 {"message":"You've reached the login attempt limit","code":"AUTHENTICATION_FAILED_LIMIT_REACHED","level":"debug"}
```

The tool exits with status `1` and nothing is changed. Waiting a minute or two clears
it; logins spaced about 20 seconds apart never hit the default limit. To avoid it:

- Use `--csv` for bulk changes: one login for the whole file, however many lines.
- In shell loops or scripts that call the tool repeatedly, pause between calls.
- Check the password with a single command before starting a long run. A wrong
  password on every call would waste the whole run.

#### Where the limit is set

The limit is enforced by **ulp-go**, UniFi OS's local user and identity service
(`/usr/sbin/ulp-go-app`, run as `ulp-go.service`), not by the Network application or
nginx. A login travels like this:

1. The tool posts to `https://<router>/api/auth/login`, which is handled by
   **unifi-core**, the UniFi OS web service.
2. unifi-core forwards the credentials to ulp-go on `127.0.0.1` (`/api/v2/login_v2`).
3. ulp-go applies its rate limits. Over the limit, it returns error code `-19`.
4. unifi-core turns `-19` into the HTTP 429 `AUTHENTICATION_FAILED_LIMIT_REACHED`
   response above (the mapping lives in `/usr/share/unifi-core/app/service.js`).

The settings are in `/usr/lib/ulp-go/config.props`:

```properties
# http rate limit (Limit 20 requests per second)
http.limit.second = 1
http.limit.count = 20
# success login rate limit (limit 5 request pre minute)
success.login.limit.count = 5
```

| Setting | Default | Meaning |
|---|---|---|
| `success.login.limit.count` | `5` | Successful logins allowed per minute. This is the one the tool runs into. |
| `http.limit.count` / `http.limit.second` | `20` / `1` | General limit of 20 requests per second to ulp-go's API. The tool never comes close. |

It isn't clear from the config whether the login limit is counted per user or per
client address, and failed logins don't appear to be controlled by this setting.

#### Changing the limit

This is an unsupported change to a file on the firmware image, so read the caveats
below first. Over SSH as root:

```bash
ssh root@192.168.1.1
F=/usr/lib/ulp-go/config.props
cp -p $F $F.bak                                              # back up first
sed -i 's/^success\.login\.limit\.count = .*/success.login.limit.count = 60/' $F
grep -n '^success.login.limit.count' $F                      # check the edit
systemctl restart ulp-go
systemctl is-active ulp-go unifi-core                        # both should say "active"
journalctl -u ulp-go -n 20 --no-pager                        # check it started cleanly
```

Use a **restart**, not `systemctl reload ulp-go`. The reload script
(`/usr/lib/ulp-go/scripts/service/reload.sh`) only posts to an internal ulp-go endpoint
and always reports success, and there is no sign that it re-reads `config.props`. The
file is read when the service starts.

To check the new limit, run more logins within a minute than the old limit allowed,
e.g. seven harmless no-op runs:

```bash
for i in 1 2 3 4 5 6 7; do ./dream_router7_dns_manager.py nas.home.internal 192.168.1.50; done
```

Caveats:

- **Logins stop for a few seconds during the restart**: the web UI, the mobile app and
  this tool. Routing, DNS and Wi-Fi are not affected. unifi-core depends on ulp-go, so
  the web UI may briefly disconnect.
- **A UniFi OS firmware update resets the file**, and the limit goes back to 5. Re-check
  it after updates with `grep success.login /usr/lib/ulp-go/config.props`. `--csv`
  works regardless of the limit, so prefer it for bulk changes.
- ulp-go handles every login to the router. If it fails to start after an edit, restore
  the backup and restart:

  ```bash
  cp -p /usr/lib/ulp-go/config.props.bak /usr/lib/ulp-go/config.props && systemctl restart ulp-go
  ```

### Check a record

```bash
nslookup nas.home.internal 192.168.1.1
```

## Notes

- Only A records (hostname → IPv4 address) are supported.
- Hostnames are matched exactly and case-sensitively: `NAS.home.internal` and
  `nas.home.internal` are treated as different records. Stick to lowercase.
- Each command logs in once, and the router rate-limits logins. See
  [Login rate limiting](#login-rate-limiting).
- **If you raised the login limit, a UniFi OS firmware update will quietly undo it.**
  `/usr/lib/ulp-go/config.props` is part of the firmware image, so every update puts
  `success.login.limit.count` back to `5`. After each update, check it with
  `ssh root@192.168.1.1 grep success.login /usr/lib/ulp-go/config.props` and repeat
  [Changing the limit](#changing-the-limit) if needed.
- The router uses a self-signed HTTPS certificate, so the tool does not verify it.
  Only point `--host` at a router on a network you trust.
- The tool uses the Network application's internal (undocumented) API, the same one
  the web UI uses. A future UniFi Network update could change it.
