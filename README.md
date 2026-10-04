# drctl: Dream Router 7 DNS and DHCP manager

If you have any experience using a Ubiquiti Edgerouter and local DNS, then you are in
for a surprise on the Dream Router. On the edgerouter you could add a ssh public
key, login passwordless, and add and remove dns entries easily, very easily scripted.

`drctl` makes that easy again. It manages a UniFi Dream Router 7's (or other UniFi OS
gateway's) local network settings from the command line, one at a time or in bulk
from CSV files:

- **Static DNS records** of every type the router supports: A, AAAA, CNAME, MX, NS,
  SRV and TXT.
- **DHCP reservations** (fixed IPs) for devices, by MAC address.
- **Hosts**: a device's reservation and its DNS name, set together in one command.
- **Network settings**: network boot (PXE) and the TFTP server handed out by DHCP.
- **Leases**: which device has which address, and turning a current address into a
  reservation.
- **Clients**: which devices are connected and how, naming them, adding notes, and
  blocking, unblocking or forgetting them.
- **Port forwarding**: opening ports to the internet for a device on your network, with
  the checks the router itself doesn't make.
- **Status**: the router's versions, internet connection, load, clients and firmware at a
  glance.
- **Backups and restores** of all the router's network settings, and the automatic
  backup schedule.
- **SSH**: switching SSH to the router, or to access points, on and off.
- **Networks and Wi-Fi networks**: e.g. a Wi-Fi network for children with its own DNS.

Changes go through the UniFi Network application's own API, so they are exactly the
same as changes made in the web UI (**Settings → Routing → DNS**, and a client's fixed
IP and local DNS record settings): they appear there, survive
reboots and firmware updates, and reach the router's DHCP and DNS server (dnsmasq)
within about 10–20 seconds.

## Requirements

- Go 1.26+ to install or build it. The result is a single binary with no dependencies.
- A **local** UniFi OS admin account on the router, with permission to change network
  settings. A UI.com cloud account with SSO/2FA will not work. Create one under
  **Settings → Admins & Users** if needed.

## Installing

```bash
go install github.com/liketed/drctl/cmd/drctl@latest   # latest release, into $(go env GOPATH)/bin (usually ~/go/bin)
drctl --help
```

Or build from a clone:

```bash
git clone https://github.com/liketed/drctl.git
cd drctl
go build -o drctl ./cmd/drctl
./drctl --help
```

Put the binary somewhere on your `PATH` (e.g. `sudo mv drctl /usr/local/bin/`). It is
ignored by git. Cross-compile for another machine with e.g.
`GOOS=linux GOARCH=arm64 go build -o drctl ./cmd/drctl`. To stamp a version, build with
`-ldflags "-X github.com/liketed/drctl/internal/cli.Version=0.1.0"`.

`drctl version` shows which version, or which source commit, the binary was built from:

```
drctl v0.1.0 (go1.26.5)                                                  # a release
drctl dev (commit 71d2dbb, committed 2026-09-28 22:03 UTC, go1.26.5)    # a commit
```

Releases are listed on the [GitHub releases page](https://github.com/liketed/drctl/tags).

That is the commit and its date (Go records those, not the time of the build), plus
"with uncommitted changes" for a local build of a modified checkout. Compare the commit
with the repository's latest to see whether your installed drctl is up to date. `@latest`
installs the newest release; to try unreleased changes, install the main branch with
`GOPROXY=direct go install github.com/liketed/drctl/cmd/drctl@main`.

## Logging in

```bash
read -s UNIFI_PASS; export UNIFI_PASS     # once per terminal session; nothing is echoed
drctl dns list
```

The password is read from `$DREAMROUTER_PASSWORD` or `$UNIFI_PASS`; if neither is set,
drctl prompts for it. `read -s` keeps it off the screen and out of your shell history.
The rest can be set per command or in the environment:

| Option | Environment | Default | Meaning |
|---|---|---|---|
| `--host HOST` | `DREAMROUTER_HOST` | `192.168.1.1` | Router address |
| `--user USER` | `DREAMROUTER_USERNAME`, `UNIFI_USER` | `admin` | Local UniFi OS account |
| `--site SITE` | – | `default` | UniFi Network site |
| `--dry-run` | – | off | Show what would change without changing anything |
| `--login-retry DUR` | – | `2m` | Keep retrying this long when the router's login limit is reached; `0` fails at once |

Options can go anywhere on the command line. Every command logs in once.

Exit status: `0` success, `1` error (bad input, conflict, not found, login failure),
`2` usage error.

## Commands

```
drctl dns  list   [--type T] [--name N] [--static] [--format table|csv|json]
drctl dns  add    NAME VALUE [--type A] [--ttl N] [--priority N] [--weight N] [--port N] [--disabled] [--append]
drctl dns  delete NAME [--type T] [--value V] [--all]
drctl dns  import FILE [--delete]

drctl dhcp list   [--format table|csv|json]
drctl dhcp add    MAC IP [--name NAME] [--network NET]
drctl dhcp delete MAC [--forget]
drctl dhcp import FILE [--delete]

drctl host list   [--format table|csv|json]
drctl host add    NAME IP --mac MAC [--network NET] [--device-name NAME]
drctl host delete NAME [--keep-reservation]

drctl leases list    [--network NET] [--format table|csv|json]
drctl leases reserve IP|MAC [--name NAME] [--dns-name NAME]

drctl clients list    [--offline | --all | --blocked] [--wired | --wifi] [--days N] [--format table|csv|json]
drctl clients show    MAC|IP|NAME
drctl clients name    MAC NAME
drctl clients note    MAC TEXT
drctl clients block   MAC
drctl clients unblock MAC
drctl clients forget  MAC

drctl network list   [--format table|csv|json]
drctl network show   [NETWORK] [--format table|csv|json]
drctl network boot   [NETWORK] --server IP --file NAME [--tftp-server HOST | --no-tftp]
drctl network boot   [NETWORK] --off [--no-tftp]
drctl network dhcp   [NETWORK] [--dns IP,IP | --dns auto] [--lease 12h | --lease default] [--ntp IP | --ntp off] [--domain NAME]
drctl network create NAME --vlan N --subnet ROUTER-IP/PREFIX [--dhcp-range START-STOP] [--dns IP,IP]
drctl network delete NAME

drctl wifi list     [--format table|csv|json]
drctl wifi create   NAME --network NET [--password-file FILE | --password-stdin] [--bands 2g,5g,6g] [--hidden] [--disabled]
drctl wifi password NAME [--password-file FILE | --password-stdin]
drctl wifi enable   NAME
drctl wifi disable  NAME
drctl wifi delete   NAME

drctl portforward list    [--format table|csv|json]
drctl portforward add     NAME PORT IP[:PORT] [--proto tcp|udp|both] [--from CIDR] [--disabled] [--log]
drctl portforward delete  NAME
drctl portforward enable  NAME
drctl portforward disable NAME

drctl backup list      [--format table|csv|json]
drctl backup download  [FILE] [--auto NAME | --latest] [--history DAYS] [--force]
drctl backup schedule  [--daily | --weekly | --monthly | --off] [--at HH:MM] [--history DAYS]
drctl backup delete    NAME
drctl backup restore   FILE [--yes] [--no-safety-backup]

drctl ssh [--format text|json]
drctl ssh router  on|off
drctl ssh devices on|off

drctl status [--format text|json]
```

`drctl COMMAND SUBCOMMAND --help` shows the options of each command. `FILE` may be `-`
to read from stdin.

## DNS records

### Add, change and delete

```bash
drctl dns add nas.home.internal 192.168.1.50
# created A nas.home.internal -> 192.168.1.50

drctl dns add nas.home.internal 192.168.1.51          # same type and name: updated in place
# updated A nas.home.internal -> 192.168.1.51 (was 192.168.1.50)

drctl dns add nas.home.internal 192.168.1.51          # already so: nothing to do
# unchanged A nas.home.internal -> 192.168.1.51

drctl dns delete nas.home.internal
# deleted A nas.home.internal -> 192.168.1.51
```

Other record types:

```bash
drctl dns add nas.home.internal fd00::50 --type AAAA --ttl 300
drctl dns add files.home.internal nas.home.internal --type CNAME
drctl dns add home.internal mail.home.internal --type MX --priority 10
drctl dns add _sip._tcp.home.internal pbx.home.internal --type SRV --priority 10 --weight 5 --port 5060
drctl dns add home.internal "v=spf1 -all" --type TXT
drctl dns add lab.home.internal 192.168.1.2 --type NS
```

What `value` means for each type, and the rules drctl checks before sending anything:

| Type | `VALUE` | Options | Notes |
|---|---|---|---|
| A | IPv4 address | `--ttl` | |
| AAAA | IPv6 address | `--ttl` | |
| CNAME | target hostname | `--ttl` | Can't share its name with another record or point to itself. |
| MX | mail server | `--priority` | |
| NS | **IP address** of a DNS server | | The router implements NS records as conditional forwarders: queries for the name and its subdomains go to that server. |
| SRV | target server | `--priority`, `--weight`, `--port` | `NAME` must be `_service._protocol.domain`. |
| TXT | text | | Double quotes only around the whole value; at most 255 characters per line. |

`--ttl` is in seconds; `0` (the default) means automatic. `--disabled` stores a record
without serving it.

`add` updates an existing record of the same type and name rather than adding a second
one. To add another deliberately (round-robin A records, a backup MX), use `--append`:

```bash
drctl dns add rr.home.internal 192.168.1.11 --append
```

`delete` removes the record with that name. If several match, add `--type` and/or
`--value` to pick one, or `--all` to remove them all.

### List

```bash
drctl dns list
# TYPE  NAME                     VALUE              DETAILS
# A     nas.home.internal        192.168.1.51
# A     printer.home.internal    192.168.1.60       host (aa:bb:cc:dd:ee:02, printer)
# SRV   _sip._tcp.home.internal  pbx.home.internal  priority=10 weight=5 port=5060

drctl dns list --type MX --format json
```

`dns list` shows every name the router answers for: static records, and devices' DNS
names set with [`drctl host`](#hosts-reservation-and-dns-name-together), which are marked
`host` with the device's MAC and name (and have `"source": "host"` in JSON output).
`--static` shows only static records.

The CSV output (`--format csv`) lists static records only, because it is meant for
`drctl dns import`; importing a device's name as a static record would clash with the
device. A note on stderr says how many device names were left out; back those up with
`drctl host list --format csv`.

`dns delete` only deletes static records. For a device's name it tells you to use
`drctl host delete` instead.

### Import and bulk delete

`drctl dns import FILE` adds or updates every record in a CSV file with one login. Two
formats are accepted; blank lines and lines starting with `#` are ignored:

```
# Simple: name,ip for A records
nas.home.internal,192.168.1.50
printer.home.internal, 192.168.1.60
```

```
type,name,value,ttl,priority,weight,port,enabled
A,nas.home.internal,192.168.1.50,0,0,0,0,true
MX,home.internal,mx1.home.internal,0,10,0,0,true
MX,home.internal,mx2.home.internal,0,20,0,0,true
```

The second format, with a header row, supports every type; columns after `value` are
optional. It is exactly what `drctl dns list --format csv` prints. Several records with
the same type and name in one file are all kept, as with `--append`.

```bash
drctl dns import records.csv --dry-run
# would create A nas.home.internal -> 192.168.1.50
# unchanged A printer.home.internal -> 192.168.1.60
#
# dry run: would create 1, would update 0, unchanged 1

drctl dns import records.csv
```

The whole file is checked before anything is sent: a bad value, a duplicate or a
malformed line stops the run with `records.csv:LINE: ...` and nothing is changed.
Re-running is safe; records that already match are reported as `unchanged`.

With `--delete`, the records in the file are deleted instead. The value is optional; if
given, a record is only deleted if it still has that value, which protects records
changed since the file was written:

```bash
drctl dns import old.csv --delete
# deleted A nas.home.internal -> 192.168.1.50
# skipped A media.home.internal -> 192.168.1.99 (is 192.168.1.70)
# not found A camera.home.internal
#
# deleted 1, not found 1, skipped 1
```

## DHCP reservations

A reservation gives a device (identified by its MAC address) a fixed IP address.

```bash
drctl dhcp add aa:bb:cc:dd:ee:01 192.168.1.50 --name nas
# created aa:bb:cc:dd:ee:01 -> 192.168.1.50 (nas)

drctl dhcp add aa:bb:cc:dd:ee:01 192.168.1.51
# updated aa:bb:cc:dd:ee:01 -> 192.168.1.51 (nas) (was 192.168.1.50)

drctl dhcp list
# MAC                IP            NAME  NETWORK  DNS NAME
# aa:bb:cc:dd:ee:01  192.168.1.51  nas   Default

drctl dhcp delete aa:bb:cc:dd:ee:01
# removed reservation aa:bb:cc:dd:ee:01 (nas) -> 192.168.1.51
```

- MAC addresses can be written with colons or dashes, in any case.
- The network is chosen from the IP address; `--network NAME` picks one explicitly.
- drctl refuses an IP outside the network's subnet, the router's own address, the
  network or broadcast address, and an IP already reserved for another device.
- If the IP is currently used by a different device, drctl warns: that device will get
  another address when its lease renews. (On a default network the DHCP pool covers
  nearly the whole subnet, so reserved IPs are usually inside the pool; the router
  allows that.)
- A device the router already knows keeps its name and history; drctl only adds the
  reservation. `--name` sets the name shown in the web UI.
- `delete` removes only the reservation (and the device's DNS name, which needs one).
  `delete --forget` removes the device from the router entirely, including its name
  and history.

### Import and bulk delete

```
# reservations.csv: mac,ip[,name[,network]]
aa:bb:cc:dd:ee:01,192.168.1.50,nas
aa:bb:cc:dd:ee:02,192.168.1.60,printer,Default
```

```bash
drctl dhcp import reservations.csv --dry-run
drctl dhcp import reservations.csv
```

As with DNS, the whole file is checked first, including for IPs reserved by other
devices, and nothing is changed if there is a problem. Moving an IP from one device to
another within the file works regardless of line order; swapping the IPs of two devices
in one run is rejected (remove one reservation first). `drctl dhcp list --format csv`
prints the same format, with a header row. `--delete` removes the reservations in a
file (lines `mac[,ip]`; with an IP, only if the reservation still has it).

## Hosts: reservation and DNS name together

The router can give a device a DNS name of its own, but only while the device has a
fixed IP. `drctl host` manages the two together:

```bash
drctl host add nas.home.internal 192.168.1.50 --mac aa:bb:cc:dd:ee:01
# created host nas.home.internal -> 192.168.1.50 (aa:bb:cc:dd:ee:01)

drctl host list
# NAME               IP            MAC                DEVICE             NETWORK
# nas.home.internal  192.168.1.50  aa:bb:cc:dd:ee:01  nas.home.internal  Default

drctl host delete nas.home.internal                    # removes the name and the reservation
drctl host delete nas.home.internal --keep-reservation # removes only the name
```

A host name can't also be a static DNS record (or another device's name); drctl checks
this and says which record is in the way. A new device is labelled with its host name
in the web UI unless `--device-name` is given.

Host names are served like A records. They appear in `drctl dns list` marked `host`,
in `drctl host list`, and in the DNS NAME column of `drctl dhcp list`.

## Leases

`drctl leases list` shows the router's current DHCP leases: which device has which
address, whether it is online, when its lease expires, and whether it has a reservation.

```bash
drctl leases list
# IP             MAC                NAME                VENDOR             STATUS   EXPIRES  RESERVED
# 192.168.1.6    aa:bb:cc:00:00:06  macbook             Apple              online   23h 41m
# 192.168.1.12   aa:bb:cc:00:00:12  pxe                 Raspberry Pi       online   -        yes (pxe.home.internal)
# 192.168.1.37   aa:bb:cc:00:00:37  living-room-tv      Samsung            online   17h 05m
# 40 leases (3 reserved)
```

- Sorted by IP. Names are the ones set in the web UI, else the router's own name for the
  device, else the device's host name. The vendor comes from the MAC address.
- `--network NET` shows one network's leases. The summary line goes to stderr.
- `--format csv|json` adds the host name, connection (wired or wireless), the expiry as a
  timestamp, the DNS name and the network.

`drctl leases reserve` turns a device's **current** address into a reservation, so it keeps
that address. Name the device by its IP or MAC address:

```bash
drctl leases reserve 192.168.1.37 --name "Living room TV" --dry-run
# would create aa:bb:cc:00:00:37 -> 192.168.1.37 (Living room TV)

drctl leases reserve 192.168.1.37 --name "Living room TV"
# created aa:bb:cc:00:00:37 -> 192.168.1.37 (Living room TV)

drctl leases reserve aa:bb:cc:00:00:06 --dns-name macbook.home.internal   # reservation + DNS name
# created host macbook.home.internal -> 192.168.1.6 (aa:bb:cc:00:00:06)
```

It does exactly what `drctl dhcp add` (or, with `--dns-name`, `drctl host add`) would do for
that device and address, with the same checks and output, in one login. A device that is
already reserved at that address is reported as `unchanged`.

## Clients

`drctl clients list` shows the devices connected now: how they connect, the signal
strength of Wi-Fi devices, how long they have been connected and their traffic.

```bash
drctl clients list
# NAME          IP             MAC                CONNECTION    VIA                     SIGNAL   UPTIME   DOWN     UP       FLAGS
# Windows PC    192.168.1.4    aa:bb:cc:00:00:04  wired 1G      Dream Router 7 port 1            6d 13h   61.1 GB  5.4 GB
# macbook       192.168.1.6    aa:bb:cc:00:00:06  wifi 6GHz ax  home @ U7 Pro           -61 dBm  21h 43m  4.2 GB   2.0 GB   reserved
# Printer       192.168.1.10   aa:bb:cc:00:00:10  wifi 2.4GHz n home @ U7 Pro           -49 dBm  11d 13h  21.7 MB  86.4 MB  note
# 40 devices (40 online)
```

- **DOWN** is what the device downloaded, **UP** what it uploaded, since it connected.
- **VIA** is the Wi-Fi network and access point, or the switch (or router) and port.
- **FLAGS**: `reserved` (DHCP reservation), `blocked`, `guest`, `note`.
- `--offline` lists devices that are offline but were seen in the last 7 days
  (`--days N` to change), `--all` lists both, and `--blocked` lists every blocked device
  however long ago it was seen. `--wired` and `--wifi` filter by connection.
- `--format csv|json` gives every field: network, Wi-Fi network, band, standard, channel,
  signal, uplink, port, link speed, uptime and traffic in seconds and bytes, vendor,
  host name, last seen, blocked, reservation, DNS name and note.

`drctl clients show` describes one device, found by MAC address, IP address or name:

```bash
drctl clients show macbook
# name:            macbook
# mac:             aa:bb:cc:00:00:06
# vendor:          Apple, Inc.
# model:           Apple MacBook Pro 16" - 2021
# status:          online for 1d 19h
# ip:              192.168.1.6 (reserved)
# dns name:        macbook.home.internal
# connection:      wifi 5GHz ax, "home", channel 44, signal -60 dBm
# via:             home @ Dream Router 7
# wifi experience: 99%
# traffic:         113.5 MB down, 86.7 MB up
# first seen:      2026-08-16 21:30
# blocked:         no
```

Names and host names need not be unique (several devices may call themselves `wlan0`);
if more than one device matches, drctl lists them so you can use the MAC address.

The other commands change a device and take its **MAC address only**. All of them accept
`--dry-run`:

```bash
drctl clients name    aa:bb:cc:00:00:10 "Office printer"   # "" removes the name
drctl clients note    aa:bb:cc:00:00:10 "toner ordered"    # "" removes the note
drctl clients block   aa:bb:cc:00:00:37                    # disconnect it and keep it off
drctl clients unblock aa:bb:cc:00:00:37
drctl clients forget  aa:bb:cc:00:00:37
# forgot aa:bb:cc:00:00:37 (Living room TV) (name "Living room TV", reservation 192.168.1.37, history)
```

- **block** disconnects the device and stops it reconnecting, on Wi-Fi and wired ports.
  drctl refuses to block the machine it is running on (recognised by its MAC or IP
  address), so you can't cut yourself off.
- **forget** removes everything the router knows about the device: its name, note,
  reservation, DNS name and history. If it connects again, it appears as a new device.
  drctl refuses to forget a blocked device; unblock it first.
- **Unknown devices.** All five commands also accept a MAC address the router doesn't
  know yet (a device that has never connected, or was forgotten), with a warning:
  - `block` blocks it before it ever joins; `--blocked` then lists it.
  - `name` and `note` create the router's record with that name or note, so the device
    shows up named when it first connects.
  - `unblock` just reports that it isn't blocked. (The router would accept the request,
    but create an empty record, so drctl doesn't send it.)
  - `forget` is sent anyway: there is nothing stored, but it's harmless.
- MAC addresses are always checked: the router itself accepts a block for any value,
  even text that isn't a MAC address, and creates a junk entry for it.

## Port forwarding

A port forward opens a port on the router's internet side and passes connections to a
device on your network. `add` says exactly what it opens:

```bash
drctl portforward add web 8443 192.168.1.20:443 --proto tcp
# created port forward "web": opens TCP 8443 on the internet, forwarded to 192.168.1.20:443

drctl portforward add games 27000-27010 192.168.1.51 --proto udp --from 203.0.113.0/24 --disabled
# created port forward "games" (disabled): UDP 27000-27010 -> 192.168.1.51:27000-27010 from 203.0.113.0/24; nothing is open until "drctl portforward enable games"

drctl portforward list
# NAME   PROTOCOL  PORT         FORWARD TO                FROM            STATE
# games  UDP       27000-27010  192.168.1.51:27000-27010  203.0.113.0/24  disabled
# web    TCP       8443         192.168.1.20:443          any             enabled

drctl portforward enable games
drctl portforward disable web
drctl portforward delete web
```

- `PORT` is a port, an ascending range (`27000-27010`) or a list (`80,443`). Only a single
  port can be forwarded to a different port (`8443` to `:443`); a range or list always
  goes to the same ports, so leave `:PORT` off.
- `--proto` is `tcp`, `udp` or `both` (the default). `--from` limits who may connect
  (default `any`). `--disabled` creates the rule without opening anything. All commands
  accept `--dry-run`.
- drctl checks what the router doesn't: the address must be a device on one of your
  networks (not the router), names are unique, ranges are ascending, and no two
  **enabled** rules may forward the same port and protocol. A disabled duplicate is
  allowed, but `enable` refuses while the other one is enabled.
- Rules are named, so `delete`, `enable` and `disable` take the name (case-insensitive).

## SSH

drctl itself never uses SSH: it talks to the router's HTTPS API, like the web UI. It can
switch the router's two SSH settings on and off:

```bash
drctl ssh
# router:  on   (SSH to the router itself)
# devices: on, username "admin"   (SSH to adopted devices such as access points)

drctl ssh router off      # turned off SSH to the router; new SSH logins to the router are refused
drctl ssh router on       # turned on SSH to the router (port 22, with the root password set before)
drctl ssh devices off
```

- **router** is SSH to the router's own shell (UniFi OS: Control Plane → Console → SSH).
  **devices** is SSH to adopted devices such as access points, with a shared username and
  password (Network: Device SSH Authentication).
- Passwords aren't changed: turning router SSH on keeps the root password set before.
- Nothing is written if a setting is already as asked. (Every write of the device
  setting makes the router issue its devices a new internal token.) `--dry-run` works.

## Status

`drctl status` shows the router at a glance:

```bash
drctl status
# Dream Router 7 (UDMA67A), UniFi OS 5.1.33, Network 10.6.106, up 6d 17h
# internet:   ok, 203.0.113.7, Example ISP (AS64500)
#             eth3 2.5 Gbps, PPPoE, latency 5 ms, 100% available, 2 drops, up 2d 6h
# system:     CPU 9.9%, memory 58.1% of 3.0 GB, CPU temperature 59.8°C, load 3.96
# clients:    41 connected (20 wired, 21 Wi-Fi)
# devices:    Dream Router 7 5.1.33.34087, U7 Pro 8.7.11.19419
# updates:    none available
# speed test: never run
```

- **internet**: whether the internet is reachable, the public (WAN) IP address and the
  provider; then the WAN port and its link speed, latency, the router's availability
  measurement, connection drops, and how long the connection has been up.
- **devices**: the router, access points and switches with their firmware. **updates**
  lists any firmware (or Network application) updates available.
- A **health** line appears only when part of the network reports a problem.
- `--format json` gives every value, with units in the field names, for scripts:
  `drctl status --format json | jq -r .internet.ip` prints the WAN IP address.

## Network settings

`drctl network list` and `drctl network show` show the router's networks (LANs, not WAN
connections) and their DHCP settings. With a single network (the usual `Default`), the
`NETWORK` argument can be left out.

```bash
drctl network list
# NAME     SUBNET          DHCP  POOL                         DOMAIN       NETWORK BOOT
# Default  192.168.1.0/24  on    192.168.1.6 - 192.168.1.254  localdomain  off

drctl network show
# Network Default (192.168.1.0/24)
#   DHCP             on, pool 192.168.1.6 - 192.168.1.254
#   Domain           localdomain
#   Network boot     off
#   TFTP server      (not set)
```

### New networks and Wi-Fi networks

A network is a VLAN with its own subnet and DHCP, so it can hand out its own DNS servers.
A Wi-Fi network's devices join one network. For example, a Wi-Fi network for children
using AdGuard DNS Family:

```bash
drctl network create Kids --vlan 30 --subnet 192.168.30.1/24 --dns 94.140.14.15,94.140.15.16
# created network Kids: VLAN 30, 192.168.30.0/24 (router 192.168.30.1), DHCP 192.168.30.6 - 192.168.30.254, DNS 94.140.14.15, 94.140.15.16

drctl wifi create home-kids --network Kids          # asks for the password twice, without showing it
# created Wi-Fi network "home-kids" on network Kids (2.4/5 GHz, WPA2/WPA3)
# Wi-Fi devices on every Wi-Fi network disconnect for about 15-30 seconds while the access points apply this

drctl wifi list
# NAME       NETWORK  BANDS        SECURITY   HIDDEN  STATE    CLIENTS
# home       Default  2.4/5/6 GHz  WPA2/WPA3          enabled  22
# home-kids  Kids     2.4/5 GHz    WPA2/WPA3          enabled  0
```

- `--subnet` is the router's address on the new network with its prefix. The subnet must
  be private and overlap no other network; the DHCP range defaults to `.6` to the last
  address but one. drctl refuses duplicate names and public subnets, which the router
  accepts.
- **Passwords are never command-line arguments** (they'd end up in shell history and
  process lists): drctl asks for them, or reads them with `--password-file FILE` or
  `--password-stdin`. drctl refuses duplicate Wi-Fi names (the router accepts even the
  name of an existing Wi-Fi network) and passwords devices couldn't use.
- **Creating, changing or deleting a Wi-Fi network briefly disconnects all Wi-Fi devices**
  while the access points apply it. Wired devices aren't affected.
- drctl refuses to disable or delete the Wi-Fi network this machine is connected
  through, to delete the network this machine is on, the router's own network, or a
  network a Wi-Fi network still uses.
- **DNS from DHCP is a default, not a lock:** a device can still use other DNS (set by
  hand, "Private DNS" on Android, DNS over HTTPS in browsers, iCloud Private Relay).

### DHCP options: DNS servers, lease time, NTP, domain

`drctl network dhcp` sets what DHCP hands out on a network. `drctl network show` shows it:

```bash
drctl network show
#   DNS servers      the router (default)
#   Lease time       1d (default)
#   NTP servers      none

drctl network dhcp --dns 192.168.1.1,1.1.1.1 --lease 12h
# changed DNS servers from the router (default) to 192.168.1.1, 1.1.1.1 on network Default
# changed lease time from 1d (default) to 12h on network Default
# devices pick up the change when they renew their lease (within 1d)

drctl network dhcp --ntp 192.168.1.1 --domain home.internal
drctl network dhcp --dns auto --lease default --ntp off     # back to the defaults
```

- `--dns`: up to 4 IPv4 addresses, or `auto` for the router itself (the default).
- `--lease`: e.g. `30m`, `12h`, `7d`, `1d12h` or seconds, from 2 minutes to a year, or
  `default` (24 hours).
- `--ntp`: up to 2 IPv4 addresses, or `off`. `--domain`: the search domain handed out.
- Only what changes is written, and `--dry-run` shows it first. The router itself accepts
  DNS "servers" that DHCP can't hand out (host names, several in one field, IPv6) and
  lease times down to 0 seconds; drctl refuses them.
- Devices pick up changes when they renew their lease, so a shorter lease time takes
  effect only after the current leases expire.

### Network boot (PXE)

Network boot lets a machine with no operating system start over the network: DHCP tells
it which server to contact and which file to load (e.g. `netboot.xyz.efi` or a Linux
installer). Devices that aren't network-booting ignore these settings.

```bash
drctl network boot --server 192.168.1.20 --file netboot.xyz.efi --dry-run
# would update network boot on Default:
#   network boot  off -> on
#   boot server   - -> 192.168.1.20
#   boot file     - -> netboot.xyz.efi

drctl network boot --server 192.168.1.20 --file netboot.xyz.efi
# updated network boot on Default: on, server 192.168.1.20, file netboot.xyz.efi

drctl network boot --off
# updated network boot on Default: off (server 192.168.1.20 and file netboot.xyz.efi kept, not active)
```

These are the web UI's **Network Boot**, **Network Boot Server IP** and **Network Boot
Filename** settings; the router hands them out as dnsmasq's `dhcp-boot`. Devices pick up a
change the next time they ask for an address, which a network-booting machine does when it
starts. Running a command again with the same values changes nothing.

- `--server` must be an IPv4 address (a server outside the network's subnet is allowed,
  with a warning). `--file` may include a path, e.g. `efi64/syslinux.efi`.
- `--off` keeps the server and file stored, as the web UI does, so turning network boot
  back on is one command. (The router doesn't allow clearing a stored boot file.)
- The router itself accepts values that would break its dnsmasq configuration (commas,
  spaces, a host name as the server); drctl rejects them before sending anything.

### TFTP server (DHCP option 66)

`--tftp-server HOST` hands out a TFTP server name or address, which some devices (e.g. IP
phones, embedded boards) ask for separately. It is **independent of network boot**: the
router keeps handing it out when network boot is off, and drctl says so after `--off`.
`--no-tftp` stops handing it out.

```bash
drctl network boot --server 192.168.1.20 --file pxelinux.0 --tftp-server tftp.home.internal
drctl network boot --off --no-tftp
```

### Limitations

- **One boot file per network.** UEFI and BIOS machines usually need different files,
  but the router only exposes a single file name. The usual answer is a boot loader that
  handles both, such as iPXE or netboot.xyz.
- **No per-device boot settings.** The router's reservations only hold a MAC address and
  an IP, so boot settings can't differ per device. To boot machines differently, hand
  them all a boot loader that decides per machine on the server, e.g. an iPXE script:

  ```
  #!ipxe
  chain http://192.168.1.20/boot/${net0/mac:hexhyp}.ipxe || chain http://192.168.1.20/boot/default.ipxe
  ```

  or put the machines on their own network (VLAN) with its own boot settings.

## Backups of the router

`drctl backup` works with the Network application's own backups: one `.unf` file holds
**all** its settings (networks, Wi-Fi, DNS records, reservations, device names, port
forwards and so on), the same file as **Settings → System → Backups** in the web UI.

```bash
drctl backup download                    # a new backup, e.g. dreamrouter-2026-10-03-2046-10.6.106.unf
drctl backup list                        # the router's automatic backups
drctl backup download --latest           # download the newest automatic backup
drctl backup schedule                    # automatic backups: monthly on the 1st at 00:30 (...)
drctl backup schedule --weekly --at 03:00
drctl backup restore dreamrouter-2026-10-03-2046-10.6.106.unf
```

**Backup files contain passwords, Wi-Fi keys and your network layout.** drctl saves them
readable only by you and never overwrites a file (unless `--force`). `--history DAYS`
adds that many days of statistics (the default, 0, is settings only). `FILE` may be `-`
to write to stdout.

**Restoring** replaces all the network settings with the backup's:

```
$ drctl backup restore dreamrouter-2026-10-03-2046-10.6.106.unf
dreamrouter-2026-10-03-2046-10.6.106.unf: backup made 2026-10-03 20:46 by Network 10.6.106 (the router runs 10.6.106), checked by the router
saved the current settings to dreamrouter-2026-10-03-2046-10.6.106-before-restore.unf first
This replaces ALL of Dream Router 7's network settings (networks, Wi-Fi, DNS, reservations, port forwards, ...)
with the backup's. The Network application restarts; routing carries on.
Type the router's name (Dream Router 7) to restore: Dream Router 7
restoring; waiting for the Network application to restart...
restored Dream Router 7 from dreamrouter-2026-10-03-2046-10.6.106.unf; the Network application is back after 58s
```

- The router checks the file first; anything that isn't a backup is refused, and so is a
  backup from a **newer** Network version than the router's.
- drctl saves a backup of the current settings next to the file first, so a restore can
  be undone (`--no-safety-backup` skips it). `--dry-run` only checks the file.
- It asks you to type the router's name; `--yes` skips the question (for scripts).
- The Network application restarts and its API and web UI are unavailable for about a
  minute. Routing, the internet connection and connected devices carry on.
- These are backups of the Network application. Backups of the whole console (all apps
  and UniFi OS users) need the console owner's account and aren't supported.

## CSV export and import

The CSV output of the list commands is the input format of the import commands:

```bash
drctl dns list --format csv  > dns-backup.csv
drctl dhcp list --format csv > dhcp-backup.csv

# later, e.g. after a reset, or on another router:
drctl dns import dns-backup.csv --dry-run && drctl dns import dns-backup.csv
drctl dhcp import dhcp-backup.csv
```

Taking a backup before a bulk import or delete is a cheap safety net. Host names are
not part of these backups (`dns list --format csv` leaves them out). Save them with
`drctl host list --format csv > hosts.csv` and re-create them with `drctl host add`.

## Loop over a file in the shell

You can also run drctl once per line from a shell loop, e.g. to combine it with other
commands. Set `UNIFI_PASS` first (see above) so you are not prompted for every line:

```bash
while IFS=, read -r host ip; do
  drctl dns add "$host" "${ip// /}"
  sleep 20    # stay under the router's default login rate limit
done < records.csv
```

Unlike `import`, this logs in **once per record**, so with the router's default settings
it runs into the login rate limit (see below) unless you pause between calls. drctl
waits and retries when that happens, but the `sleep 20` avoids the waits; it makes the
loop slow (about 15 minutes for 45 records). If you have raised the limit on your
router, you can shorten or drop the `sleep`. For more than a few records, `import` is
faster and checks the whole file before changing anything.

## Login rate limiting

By default the router allows **5 successful logins per minute**. Each drctl command
logs in once, so the sixth command within a minute is refused with HTTP 429. drctl
then waits and retries (after 5 s, 10 s, then every 20 s) for up to `--login-retry`
(default 2 minutes), printing:

```
drctl: router login limit reached, retrying in 5s
```

Waiting a minute clears the limit; commands spaced about 20 seconds apart never hit it.
With `--login-retry 0`, or if the limit is still reached after the retry time, drctl
exits with status `1`, nothing is changed, and the error names the setting below. To
avoid the limit:

- Use `import` for bulk changes: one login for the whole file, however many lines.
- In shell loops or scripts that call drctl repeatedly, pause between calls.
- Check the password with a single command before starting a long run.

### Where the limit is set

The limit is enforced by **ulp-go**, UniFi OS's local user and identity service
(`/usr/sbin/ulp-go-app`, run as `ulp-go.service`), not by the Network application or
nginx. A login travels like this:

1. drctl posts to `https://<router>/api/auth/login`, which is handled by
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
| `success.login.limit.count` | `5` | Successful logins allowed per minute. This is the one drctl can run into. |
| `http.limit.count` / `http.limit.second` | `20` / `1` | General limit of 20 requests per second to ulp-go's API. drctl never comes close. |

It isn't clear from the config whether the login limit is counted per user or per
client address, and failed logins don't appear to be controlled by this setting.

### Changing the limit

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
for i in 1 2 3 4 5 6 7; do drctl dns list --login-retry 0 >/dev/null && echo ok; done
```

Caveats:

- **Logins stop for a few seconds during the restart**: the web UI, the mobile app and
  drctl. Routing, DNS and Wi-Fi are not affected. unifi-core depends on ulp-go, so
  the web UI may briefly disconnect.
- **A UniFi OS firmware update resets the file**, and the limit goes back to 5. Re-check
  it after updates with `grep success.login /usr/lib/ulp-go/config.props`. `import`
  works regardless of the limit, so prefer it for bulk changes.
- ulp-go handles every login to the router. If it fails to start after an edit, restore
  the backup and restart:

  ```bash
  cp -p /usr/lib/ulp-go/config.props.bak /usr/lib/ulp-go/config.props && systemctl restart ulp-go
  ```

## Check the result

```bash
nslookup nas.home.internal 192.168.1.1
```

Remember that devices only see these names if they use the router (e.g. `192.168.1.1`)
as their DNS server.

## Notes

- Names are matched exactly and case-sensitively: `NAS.home.internal` and
  `nas.home.internal` are different records. Stick to lowercase.
- **If you raised the login limit, a UniFi OS firmware update will quietly undo it.**
  `/usr/lib/ulp-go/config.props` is part of the firmware image, so every update puts
  `success.login.limit.count` back to `5`. After each update, check it with
  `ssh root@192.168.1.1 grep success.login /usr/lib/ulp-go/config.props` and repeat
  [Changing the limit](#changing-the-limit) if needed.
- The router uses a self-signed HTTPS certificate, so drctl does not verify it. Only
  point `--host` at a router on a network you trust.
- drctl uses the Network application's internal (undocumented) API, the same one the
  web UI uses. A future UniFi Network update could change it.
- A Terraform/OpenTofu provider manages the same DNS records, DHCP reservations and
  hosts: [liketed/terraform-provider-dreamrouter](https://github.com/liketed/terraform-provider-dreamrouter).
  Both use the same API client and validation, from
  [liketed/dreamrouter-go](https://github.com/liketed/dreamrouter-go).

## Development

```bash
go vet ./... && go test ./...
```

The tests run every command in-process against an in-memory fake of the router's API
(login and its limit, static DNS, clients and networks, with the router's error codes);
no router or network is needed.

| Path | Contents |
|---|---|
| `cmd/drctl` | Entry point. |
| `internal/cli` | Commands, argument parsing, CSV import/export, output formats. |

The router API client (`unifi`), validation (`check`) and fake router (`fakerouter`) are
in [dreamrouter-go](https://github.com/liketed/dreamrouter-go), shared with the
Terraform provider.

## License

Licensed under the [Apache License, Version 2.0](LICENSE) (SPDX: `Apache-2.0`), like
[dreamrouter-go](https://github.com/liketed/dreamrouter-go) and the
[Terraform provider](https://github.com/liketed/terraform-provider-dreamrouter).

You may use, modify and distribute drctl, including in commercial settings, provided you
keep the license and any copyright notices, and state significant changes you make to
the files. It is provided "as is", without warranties or conditions of any kind; see the
[LICENSE](LICENSE) file for the full terms.
