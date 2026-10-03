package cli

import (
	"strings"
	"testing"
	"time"

	"github.com/liketed/dreamrouter-go/fakerouter"
)

// clientsRouter returns a fake router with a connected Wi-Fi laptop (named,
// reserved, with a DNS name), a connected wired NAS, an offline phone, a
// device blocked long ago, and two devices both called "wlan0".
func clientsRouter(t *testing.T) *fakerouter.Router {
	t.Helper()
	r := fakerouter.New()
	t.Cleanup(r.Close)
	nowUnix := float64(now().Unix())
	r.PutClient(fakerouter.Client{MAC: "aa:bb:cc:00:00:10", Name: "Laptop", Hostname: "laptop", UseFixedIP: true, FixedIP: "192.168.1.10",
		NetworkID: fakerouter.NetworkID, LocalDNSRecord: "laptop.home.internal", LocalDNSRecordEnabled: true})
	r.PutActive(fakerouter.Status{MAC: "aa:bb:cc:00:00:10", IP: "192.168.1.10", DisplayName: "Laptop", Hostname: "laptop", Type: "WIRELESS",
		ESSID: "home", Radio: "na", RadioProto: "ax", Channel: 36, Signal: -61, UplinkName: "U7 Pro", TxBytes: 2.5e9, RxBytes: 1.2e8,
		Uptime: 3*3600 + 12*60, OUI: "Apple, Inc.", NetworkName: "Default", FirstSeen: nowUnix - 86400*40})
	r.PutClient(fakerouter.Client{MAC: "aa:bb:cc:00:00:20", Hostname: "nas"})
	r.PutActive(fakerouter.Status{MAC: "aa:bb:cc:00:00:20", IP: "192.168.1.20", DisplayName: "nas", Hostname: "nas", Type: "WIRED", IsWired: true,
		UplinkName: "Dream Router 7", SwitchPort: 3, WiredRateMbps: 1000, Uptime: 5 * 86400, NetworkName: "Default"})
	r.PutClient(fakerouter.Client{MAC: "aa:bb:cc:00:00:30", Hostname: "phone"})
	r.PutOffline(fakerouter.Status{MAC: "aa:bb:cc:00:00:30", LastIP: "192.168.1.30", DisplayName: "Phone 00:30", Hostname: "phone",
		Type: "WIRELESS", ESSID: "home", Radio: "ng", RadioProto: "ng", LastSeen: nowUnix - 2*3600})
	r.PutClient(fakerouter.Client{MAC: "aa:bb:cc:00:00:40", Name: "Old tablet", Blocked: true})
	r.PutClient(fakerouter.Client{MAC: "aa:bb:cc:00:00:51", Hostname: "wlan0"})
	r.PutActive(fakerouter.Status{MAC: "aa:bb:cc:00:00:51", IP: "192.168.1.51", Hostname: "wlan0", Type: "WIRELESS", Radio: "6e", RadioProto: "be"})
	// The router uses the MAC address as the display name when it knows nothing else.
	r.PutClient(fakerouter.Client{MAC: "aa:bb:cc:00:00:60"})
	r.PutActive(fakerouter.Status{MAC: "aa:bb:cc:00:00:60", IP: "192.168.1.60", DisplayName: "aa:bb:cc:00:00:60", Type: "WIRED"})
	r.PutClient(fakerouter.Client{MAC: "aa:bb:cc:00:00:52", Hostname: "wlan0"})
	r.PutActive(fakerouter.Status{MAC: "aa:bb:cc:00:00:52", IP: "192.168.1.52", Hostname: "wlan0", Type: "WIRELESS"})
	return r
}

// fakeLocal pretends drctl runs on a machine with the given addresses.
func fakeLocal(t *testing.T, mac, ip string) {
	old := localAddrs
	localAddrs = func() ([]string, []string) { return []string{mac}, []string{"127.0.0.1", ip} }
	t.Cleanup(func() { localAddrs = old })
}

func TestClientsList(t *testing.T) {
	r := clientsRouter(t)
	fakeLocal(t, "02:00:00:00:00:01", "192.168.1.99")

	res := drctl(t, r, "clients", "list")
	res.ok(t)
	res.says(t, "Laptop", "192.168.1.10", "wifi 5GHz ax", "home @ U7 Pro", "-61 dBm", "3h 12m", "2.5 GB", "120.0 MB", "reserved",
		"nas", "wired 1G", "wifi 6GHz be", "Dream Router 7 port 3", "5d 0h")
	if strings.Count(res.stdout, "aa:bb:cc:00:00:60") != 1 {
		t.Fatalf("a MAC-only device should show its MAC once, not as its name too:\n%s", res.stdout)
	}
	if strings.Contains(res.stdout, "phone") || strings.Contains(res.stdout, "Old tablet") {
		t.Fatalf("offline devices listed by default:\n%s", res.stdout)
	}
	if !strings.Contains(res.stderr, "5 devices (5 online)") {
		t.Fatalf("summary: %q", res.stderr)
	}

	res = drctl(t, r, "clients", "list", "--offline")
	res.ok(t)
	res.says(t, "Phone", "192.168.1.30", "offline", "2h 0m ago", "wifi 2.4GHz n ")
	if strings.Contains(res.stdout, "Laptop") || strings.Contains(res.stdout, "Old tablet") {
		t.Fatalf("--offline listed other devices:\n%s", res.stdout)
	}

	res = drctl(t, r, "clients", "list", "--all", "--wifi")
	res.ok(t)
	res.says(t, "Laptop", "Phone", "now")
	if strings.Contains(res.stdout, "nas") {
		t.Fatalf("--wifi listed the wired NAS:\n%s", res.stdout)
	}

	res = drctl(t, r, "clients", "list", "--wired")
	res.ok(t)
	if strings.Contains(res.stdout, "Laptop") || !strings.Contains(res.stdout, "nas") {
		t.Fatalf("--wired:\n%s", res.stdout)
	}

	res = drctl(t, r, "clients", "list", "--blocked")
	res.ok(t)
	res.says(t, "Old tablet", "aa:bb:cc:00:00:40", "blocked")
	if !strings.Contains(res.stderr, "1 device (0 online)") {
		t.Fatalf("summary: %q", res.stderr)
	}
	if strings.Contains(res.stdout, "Laptop") {
		t.Fatalf("--blocked listed an unblocked device:\n%s", res.stdout)
	}

	res = drctl(t, r, "clients", "list", "--format", "csv")
	res.ok(t)
	res.says(t, "name,ip,mac,status,connection,network,ssid,band,wifi_standard,signal_dbm,uplink,port,link_mbps,uptime_seconds,download_bytes,upload_bytes,vendor,hostname,last_seen,blocked,reserved,dns_name,note",
		"Laptop,192.168.1.10,aa:bb:cc:00:00:10,online,wifi,Default,home,5GHz,ax,-61,U7 Pro,,,11520,2.5e+09,1.2e+08,\"Apple, Inc.\",laptop,,false,true,laptop.home.internal,")

	drctl(t, r, "clients", "list", "--all", "--offline").fails(t, 2, "only one of --offline, --all and --blocked")
	drctl(t, r, "clients", "list", "--wired", "--wifi").fails(t, 2, "only one of --wired and --wifi")
	drctl(t, r, "clients", "list", "--days", "0").fails(t, 2, "--days must be at least 1")
}

func TestClientsShow(t *testing.T) {
	r := clientsRouter(t)
	for _, key := range []string{"aa:bb:cc:00:00:10", "AA-BB-CC-00-00-10", "192.168.1.10", "laptop", "LAPTOP"} {
		res := drctl(t, r, "clients", "show", key)
		res.ok(t)
		res.says(t, "name:            Laptop", "status:          online for 3h 12m", "ip:              192.168.1.10 (reserved)",
			"dns name:        laptop.home.internal", `connection:      wifi 5GHz ax, "home", channel 36, signal -61 dBm`,
			"via:             home @ U7 Pro", "traffic:         2.5 GB down, 120.0 MB up", "vendor:          Apple, Inc.", "blocked:         no")
	}
	drctl(t, r, "clients", "show", "phone").ok(t).says(t, "status:          offline, last seen 2h 0m ago", "ip:              192.168.1.30 (last)")
	drctl(t, r, "clients", "show", "Old tablet").ok(t).says(t, "blocked:         yes", "status:          offline, never connected")
	drctl(t, r, "clients", "show", "wlan0").fails(t, 1, `2 devices match "wlan0", use the MAC address: aa:bb:cc:00:00:51 (wlan0), aa:bb:cc:00:00:52 (wlan0)`)
	drctl(t, r, "clients", "show", "printer").fails(t, 1, `no device named "printer"`)
	drctl(t, r, "clients", "show", "192.168.1.200").fails(t, 1, "no device has (or recently had) IP 192.168.1.200")
	drctl(t, r, "clients", "show", "aa:bb:cc:00:00:99").fails(t, 1, "doesn't know a device with MAC aa:bb:cc:00:00:99")
}

func TestClientsNameAndNote(t *testing.T) {
	r := clientsRouter(t)
	drctl(t, r, "clients", "name", "aa:bb:cc:00:00:20", "NAS", "--dry-run").ok(t).says(t, `would name aa:bb:cc:00:00:20 (nas) "NAS"`)
	if d, _ := r.Client("aa:bb:cc:00:00:20"); d.Name != "" {
		t.Fatal("--dry-run changed the name")
	}
	drctl(t, r, "clients", "name", "aa:bb:cc:00:00:20", "NAS").ok(t).says(t, `named aa:bb:cc:00:00:20 (nas) "NAS"`)
	if d, _ := r.Client("aa:bb:cc:00:00:20"); d.Name != "NAS" {
		t.Fatalf("name: %+v", d)
	}
	drctl(t, r, "clients", "name", "aa:bb:cc:00:00:20", "NAS").ok(t).says(t, "already has that name")
	drctl(t, r, "clients", "name", "aa:bb:cc:00:00:20", "").ok(t).says(t, "removed name of aa:bb:cc:00:00:20 (NAS)")

	drctl(t, r, "clients", "note", "aa:bb:cc:00:00:20", "in the cupboard").ok(t).says(t, "set note of aa:bb:cc:00:00:20 (nas): in the cupboard")
	drctl(t, r, "clients", "show", "nas").ok(t).says(t, "note:            in the cupboard")
	drctl(t, r, "clients", "list").ok(t).says(t, "note")
	drctl(t, r, "clients", "note", "aa:bb:cc:00:00:20", "").ok(t).says(t, "removed note of aa:bb:cc:00:00:20 (nas)")
	if d, _ := r.Client("aa:bb:cc:00:00:20"); d.Note != "" {
		t.Fatalf("note not removed: %+v", d)
	}

	drctl(t, r, "clients", "name", "nas", "x").fails(t, 1, `invalid MAC address "nas"`)

	// Unknown devices get a record with the name or note, ready for when they join.
	before := len(r.Clients())
	res := drctl(t, r, "clients", "name", "aa:bb:cc:00:00:99", "Visitor laptop", "--dry-run")
	res.ok(t).says(t, `would name aa:bb:cc:00:00:99 "Visitor laptop"`)
	warned(t, res, "aa:bb:cc:00:00:99")
	if len(r.Clients()) != before {
		t.Fatal("--dry-run created a record")
	}
	res = drctl(t, r, "clients", "name", "aa:bb:cc:00:00:99", "Visitor laptop")
	res.ok(t).says(t, `named aa:bb:cc:00:00:99 "Visitor laptop"`)
	warned(t, res, "aa:bb:cc:00:00:99")
	if d, ok := r.Client("aa:bb:cc:00:00:99"); !ok || d.Name != "Visitor laptop" {
		t.Fatalf("record not created: %+v", d)
	}
	res = drctl(t, r, "clients", "note", "aa:bb:cc:00:00:98", "expected on Friday")
	res.ok(t).says(t, "set note of aa:bb:cc:00:00:98: expected on Friday")
	warned(t, res, "aa:bb:cc:00:00:98")
	if d, ok := r.Client("aa:bb:cc:00:00:98"); !ok || d.Note != "expected on Friday" {
		t.Fatalf("record not created: %+v", d)
	}
	// Now known: changes go to the existing record, without a warning.
	res = drctl(t, r, "clients", "note", "aa:bb:cc:00:00:98", "arrived")
	res.ok(t).says(t, "set note of aa:bb:cc:00:00:98: arrived")
	if res.stderr != "" {
		t.Fatalf("unexpected warning: %q", res.stderr)
	}
	// Removing the name or note of an unknown device does nothing.
	before = len(r.Clients())
	drctl(t, r, "clients", "name", "aa:bb:cc:00:00:97", "").ok(t).says(t, "aa:bb:cc:00:00:97 has no name")
	drctl(t, r, "clients", "note", "aa:bb:cc:00:00:97", "").ok(t).says(t, "aa:bb:cc:00:00:97 has no note")
	if len(r.Clients()) != before {
		t.Fatal("removing from an unknown device created a record")
	}
	drctl(t, r, "clients", "note", "nope", "x").fails(t, 1, `invalid MAC address "nope"`)
}

func TestClientsBlock(t *testing.T) {
	r := clientsRouter(t)
	fakeLocal(t, "02:00:00:00:00:01", "192.168.1.99")
	before := len(r.Clients())

	drctl(t, r, "clients", "block", "aa:bb:cc:00:00:30", "--dry-run").ok(t).says(t, "would block aa:bb:cc:00:00:30 (Phone)")
	if d, _ := r.Client("aa:bb:cc:00:00:30"); d.Blocked {
		t.Fatal("--dry-run blocked the device")
	}
	drctl(t, r, "clients", "block", "aa:bb:cc:00:00:30").ok(t).says(t, `blocked aa:bb:cc:00:00:30 (Phone); it can't connect until "drctl clients unblock aa:bb:cc:00:00:30"`)
	if d, _ := r.Client("aa:bb:cc:00:00:30"); !d.Blocked {
		t.Fatal("not blocked")
	}
	drctl(t, r, "clients", "block", "aa:bb:cc:00:00:30").ok(t).says(t, "already blocked")
	drctl(t, r, "clients", "unblock", "aa:bb:cc:00:00:30", "--dry-run").ok(t).says(t, "would unblock")
	if d, _ := r.Client("aa:bb:cc:00:00:30"); !d.Blocked {
		t.Fatal("--dry-run unblocked the device")
	}
	drctl(t, r, "clients", "unblock", "aa:bb:cc:00:00:30").ok(t).says(t, "unblocked aa:bb:cc:00:00:30 (Phone)")
	drctl(t, r, "clients", "unblock", "aa:bb:cc:00:00:30").ok(t).says(t, "is not blocked")

	// The router would accept these, creating junk entries; drctl must not send them.
	drctl(t, r, "clients", "block", "nope").fails(t, 1, `invalid MAC address "nope"`)
	drctl(t, r, "clients", "block", "phone").fails(t, 1, `invalid MAC address "phone"`)
	if n := len(r.Clients()); n != before {
		t.Fatalf("client entries changed from %d to %d", before, n)
	}

	// An unknown but valid MAC can be blocked in advance, with a warning.
	res := drctl(t, r, "clients", "block", "AA-BB-CC-00-00-99", "--dry-run")
	res.ok(t).says(t, "would block aa:bb:cc:00:00:99;")
	if !strings.Contains(res.stderr, "warning: the router doesn't know a device with MAC aa:bb:cc:00:00:99") {
		t.Fatalf("no warning: %q", res.stderr)
	}
	if _, ok := r.Client("aa:bb:cc:00:00:99"); ok {
		t.Fatal("--dry-run blocked the unknown device")
	}
	res = drctl(t, r, "clients", "block", "aa:bb:cc:00:00:99")
	res.ok(t).says(t, "blocked aa:bb:cc:00:00:99;")
	warned(t, res, "aa:bb:cc:00:00:99")
	if d, ok := r.Client("aa:bb:cc:00:00:99"); !ok || !d.Blocked {
		t.Fatal("unknown device not blocked")
	}
	// Now the router knows it: no warning, and it can be unblocked and forgotten.
	res = drctl(t, r, "clients", "block", "aa:bb:cc:00:00:99")
	res.ok(t).says(t, "is already blocked")
	if res.stderr != "" {
		t.Fatalf("unexpected warning: %q", res.stderr)
	}
	drctl(t, r, "clients", "list", "--blocked").ok(t).says(t, "aa:bb:cc:00:00:99")
	drctl(t, r, "clients", "unblock", "aa:bb:cc:00:00:99").ok(t).says(t, "unblocked aa:bb:cc:00:00:99")
	drctl(t, r, "clients", "forget", "aa:bb:cc:00:00:99").ok(t)
	// Unblocking an unknown device warns and sends nothing (the router would
	// create an empty record).
	before = len(r.Clients())
	res = drctl(t, r, "clients", "unblock", "aa:bb:cc:00:00:98")
	res.ok(t).says(t, "aa:bb:cc:00:00:98 is not blocked")
	warned(t, res, "aa:bb:cc:00:00:98")
	if len(r.Clients()) != before {
		t.Fatal("unblocking an unknown device created a record")
	}

	// Never block the machine drctl runs on, found by MAC (even if the router
	// doesn't know it) or by IP.
	fakeLocal(t, "aa:bb:cc:00:00:97", "10.0.0.5")
	drctl(t, r, "clients", "block", "aa:bb:cc:00:00:97").fails(t, 1, "is the machine drctl is running on")
	if _, ok := r.Client("aa:bb:cc:00:00:97"); ok {
		t.Fatal("this machine's unknown MAC was blocked")
	}
	fakeLocal(t, "aa:bb:cc:00:00:10", "10.0.0.5")
	drctl(t, r, "clients", "block", "aa:bb:cc:00:00:10").fails(t, 1, "is the machine drctl is running on; refusing to block it")
	fakeLocal(t, "02:00:00:00:00:01", "192.168.1.20")
	drctl(t, r, "clients", "block", "aa:bb:cc:00:00:20").fails(t, 1, "is the machine drctl is running on")
	for _, mac := range []string{"aa:bb:cc:00:00:10", "aa:bb:cc:00:00:20"} {
		if d, _ := r.Client(mac); d.Blocked {
			t.Fatalf("%s was blocked", mac)
		}
	}
}

func TestClientsForget(t *testing.T) {
	r := clientsRouter(t)
	drctl(t, r, "clients", "forget", "aa:bb:cc:00:00:10", "--dry-run").ok(t).
		says(t, `would forget aa:bb:cc:00:00:10 (Laptop) (name "Laptop", reservation 192.168.1.10, DNS name laptop.home.internal, history)`)
	if _, ok := r.Client("aa:bb:cc:00:00:10"); !ok {
		t.Fatal("--dry-run forgot the device")
	}
	drctl(t, r, "clients", "forget", "aa:bb:cc:00:00:10").ok(t).says(t, "forgot aa:bb:cc:00:00:10 (Laptop)")
	if _, ok := r.Client("aa:bb:cc:00:00:10"); ok {
		t.Fatal("device not forgotten")
	}
	drctl(t, r, "clients", "forget", "aa:bb:cc:00:00:40").fails(t, 1, "is blocked; unblock it first")

	// Forgetting an unknown device warns; the router accepts it.
	res := drctl(t, r, "clients", "forget", "aa:bb:cc:00:00:99", "--dry-run")
	res.ok(t).says(t, "would forget aa:bb:cc:00:00:99 (nothing was stored)")
	warned(t, res, "aa:bb:cc:00:00:99")
	res = drctl(t, r, "clients", "forget", "aa:bb:cc:00:00:99")
	res.ok(t).says(t, "forgot aa:bb:cc:00:00:99 (nothing was stored)")
	warned(t, res, "aa:bb:cc:00:00:99")
	drctl(t, r, "clients", "forget", "nope").fails(t, 1, `invalid MAC address "nope"`)
	if _, ok := r.Client("aa:bb:cc:00:00:40"); !ok {
		t.Fatal("blocked device was forgotten")
	}
}

func TestDurationText(t *testing.T) {
	for d, want := range map[time.Duration]string{
		7 * time.Minute: "7m", 5*time.Hour + 12*time.Minute: "5h 12m", 76 * time.Hour: "3d 4h",
	} {
		if got := durationText(d); got != want {
			t.Errorf("durationText(%v) = %q, want %q", d, got, want)
		}
	}
	for b, want := range map[float64]string{0: "0 B", 999: "999 B", 1500: "1.5 KB", 2.5e9: "2.5 GB"} {
		if got := bytesText(b); got != want {
			t.Errorf("bytesText(%v) = %q, want %q", b, got, want)
		}
	}
}

// warned checks drctl warned that the router doesn't know the MAC address.
func warned(t *testing.T, res result, mac string) {
	t.Helper()
	if !strings.Contains(res.stderr, "drctl: warning: the router doesn't know a device with MAC "+mac) {
		t.Fatalf("no unknown-device warning for %s: %q", mac, res.stderr)
	}
}
