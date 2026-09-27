# Dream Router 7 DNS manager

If you have any experience using a Ubiquiti Edgerouter and local DNS, then you are in
for a surprise on the Dream Router. On the edgerouter you could add a ssh public
key, login passwordless, and add and remove dns entries easily, very easily scripted.


`dream_router7_dns_manager.py` creates, updates and deletes local DNS **A records** on a
UniFi Dream Router 7 (or other UniFi OS gateway) from the command line.

Records are created through the UniFi Network application's own API, so they are
exactly the same as records added under **Settings → Routing → DNS** in the web UI:
they appear there, survive reboots and firmware updates, and are pushed to the
router's DNS server within a few seconds.

The script is a single file and needs only Python 3 (standard library, no packages).

## Requirements

- Python 3 (the one that ships with macOS is fine).
- A **local** UniFi OS admin account on the router, with permission to change network
  settings. A UI.com cloud account with SSO/2FA will not work. Create one under
  **Settings → Admins & Users** if needed.

## Usage

```
./dream_router7_dns_manager.py [USER [PASSWORD]] HOSTNAME IP
./dream_router7_dns_manager.py --delete [USER [PASSWORD]] HOSTNAME
```

| Option | Default | Meaning |
|---|---|---|
| `USER` | `$UNIFI_USER`, else `admin` | Local UniFi OS account to log in with |
| `PASSWORD` | `$UNIFI_PASS`, else prompted | Password for that account |
| `--delete` | off | Delete the record for `HOSTNAME` instead of creating it |
| `--host` | `192.168.1.1` | Router address |
| `--site` | `default` | UniFi Network site name |

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

### Add many records from a file

Put one `hostname,ip` pair per line in a file:

```
nas.home.internal,192.168.1.50
printer.home.internal,192.168.1.60
media.home.internal,192.168.1.70
```

Then loop over it, with `UNIFI_PASS` set as above so you are not prompted per line:

```bash
while IFS=, read -r host ip; do
  ./dream_router7_dns_manager.py "$host" "${ip// /}"
done < records.csv
```

Existing records with the right IP are reported as `unchanged`, so the loop is safe to
re-run after adding lines to the file.

### Check a record

```bash
nslookup nas.home.internal 192.168.1.1
```

## Notes

- Only A records (hostname → IPv4 address) are supported.
- Hostnames are matched exactly and case-sensitively: `NAS.home.internal` and
  `nas.home.internal` are treated as different records. Stick to lowercase.
- The router uses a self-signed HTTPS certificate, so the script does not verify it.
  Only point `--host` at a router on a network you trust.
- The script uses the Network application's internal (undocumented) API, the same one
  the web UI uses. A future UniFi Network update could change it.
