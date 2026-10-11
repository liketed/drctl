package cli

import (
	"strings"
	"testing"
	"time"

	"github.com/liketed/dreamrouter-go/fakerouter"
)

func fastWANWait(t *testing.T) {
	old := wanWait
	wanWait.settle, wanWait.interval, wanWait.timeout = 0, 10*time.Millisecond, 300*time.Millisecond
	t.Cleanup(func() { wanWait = old })
}

// wanRouter is the setup from the real router: Internet 1 (PPPoE, VLAN 10)
// on Port 4, Internet 2 (DHCP) on the empty SFP+ port, and a second line
// plugged into Port 3 whose provider accepts eir's generic login.
func wanRouter(t *testing.T) *fakerouter.Router {
	r := fakerouter.New()
	t.Cleanup(r.Close)
	r.AddWAN2()
	r.AcceptPPPoE("eir@eir.ie")
	fastWANWait(t)
	fakeLocal(t, "02:00:00:00:00:01", "192.168.1.99")
	return r
}

func TestWANListShowAndPorts(t *testing.T) {
	r := wanRouter(t)
	res := drctl(t, r, "wan", "list")
	res.ok(t).says(t, "Internet 1", "Port 4", "PPPOE", "user@isp.example", "10", "up", "203.0.113.7", "primary",
		"Internet 2", "SFP+ 1", "DHCP", "down", "failover")
	drctl(t, r, "wan", "show", "internet 1").ok(t).says(t, "connection:  Internet 1 (primary)", "port:        Port 4 (eth3)",
		"state:       up, 203.0.113.7", "username:    user@isp.example", "password:    set\n", "vlan:        10")
	if strings.Contains(drctl(t, r, "wan", "show", "Internet 1").stdout, "isp-password") {
		t.Fatal("the PPPoE password was shown")
	}
	drctl(t, r, "wan", "show", "Internet 2").ok(t).says(t, "state:       down (no cable on SFP+ 1)")
	drctl(t, r, "port", "list").ok(t).says(t, "Port 3", "eth2", "up", "LAN", "Port 4", "eth3", "Internet 1", "SFP+ 1", "eth4", "Internet 2")
	drctl(t, r, "status").ok(t).says(t, "internet 2: down (eth4)")
	drctl(t, r, "wan", "show", "Internet 3").fails(t, 1, `no internet connection named "Internet 3"`)
}

func TestWANSetAndPortLikeEir(t *testing.T) {
	r := wanRouter(t)
	// Yesterday's steps: eir's login on Internet 2, then Internet 2 to Port 3.
	res := drctlIn(t, r, "broadband1\n", "wan", "set", "Internet 2", "--pppoe", "--username", "eir@eir.ie", "--password-stdin", "--vlan", "10")
	res.ok(t).says(t, "changed type from DHCP to PPPOE on Internet 2", `changed username from "" to "eir@eir.ie"`,
		"changed password on Internet 2", "changed VLAN from - to 10", "Internet 2 is not up (it wasn't before either; is a cable connected to SFP+ 1?)")
	if strings.Contains(res.stdout+res.stderr, "broadband1") {
		t.Fatal("the password was printed")
	}
	if n := r.Network("Internet 2"); n["wan_type"] != "pppoe" || n["wan_username"] != "eir@eir.ie" || n["x_wan_password"] != "broadband1" || n["wan_vlan"] != 10.0 {
		t.Fatalf("stored: %v", n)
	}
	drctl(t, r, "wan", "port", "Internet 2", "Port 3", "--dry-run").ok(t).says(t, "would move Internet 2 from SFP+ 1 to Port 3; SFP+ 1 would become a LAN port")
	if _, writes := r.PortRoles(); writes != 0 {
		t.Fatal("--dry-run moved the port")
	}
	drctl(t, r, "wan", "port", "Internet 2", "3").ok(t).says(t, "moved Internet 2 from SFP+ 1 to Port 3; SFP+ 1 is now a LAN port", "Internet 2 is up on Port 3: 198.51.100.9")
	roles, _ := r.PortRoles()
	if roles["eth2"] != "WAN2" || roles["eth4"] != "LAN" || roles["eth3"] != "WAN" {
		t.Fatalf("roles: %v", roles)
	}
	drctl(t, r, "status").ok(t).says(t, "internet 2: up, 198.51.100.9 (eth2)")
	drctl(t, r, "wan", "port", "Internet 2", "Port 3").ok(t).says(t, "already uses Port 3")
	drctl(t, r, "wan", "port", "Internet 2", "Port 4").fails(t, 1, "Port 4 is used by Internet 1")
	drctl(t, r, "wan", "port", "Internet 2", "Port 9").fails(t, 1, `no port "Port 9"`)
	drctl(t, r, "wan", "set", "Internet 2", "--vlan", "10").ok(t).says(t, "nothing to change on Internet 2")
}

func TestWANSetRollsBack(t *testing.T) {
	r := wanRouter(t)
	before := r.Network("Internet 1")
	// A login the provider rejects: Internet 1 doesn't come back, so drctl
	// puts the previous settings back.
	res := drctlIn(t, r, "wrong-password\nInternet 1\n", "wan", "set", "Internet 1", "--username", "typo@isp.example", "--password-stdin")
	res.fails(t, 1, "Internet 1 didn't come back within 300ms with the new settings; put the previous settings back")
	if n := r.Network("Internet 1"); n["wan_username"] != before["wan_username"] || n["x_wan_password"] != before["x_wan_password"] {
		t.Fatalf("not rolled back: %v", n)
	}
	// The primary connection needs its name typed (or --yes).
	drctlIn(t, r, "Internet\n", "wan", "set", "Internet 1", "--no-vlan").fails(t, 1, "nothing changed: the name didn't match")
	if r.Network("Internet 1")["wan_vlan_enabled"] != true {
		t.Fatal("changed without confirmation")
	}
	// A change that keeps it working: confirmed with --yes, and kept.
	drctl(t, r, "wan", "set", "Internet 1", "--dns", "1.1.1.1,9.9.9.9", "--yes").ok(t).
		says(t, "changed DNS from the provider's to 1.1.1.1, 9.9.9.9 on Internet 1", "Internet 1 is back up")
	if n := r.Network("Internet 1"); n["wan_dns1"] != "1.1.1.1" || n["wan_dns_preference"] != "manual" {
		t.Fatalf("DNS: %v", n)
	}
	// Checked before anything is sent.
	drctl(t, r, "wan", "set", "Internet 2", "--vlan", "4095").fails(t, 1, "from 1 to 4094")
	drctl(t, r, "wan", "set", "Internet 2", "--dns", "dns.google").fails(t, 1, "must be an IPv4 address")
	drctl(t, r, "wan", "set", "Internet 2", "--static", "--ip", "203.0.113.10/24", "--gateway", "198.51.100.1").fails(t, 1, "must be another address in 203.0.113.0/24")
	drctl(t, r, "wan", "set", "Internet 2", "--pppoe", "--dhcp").fails(t, 2, "only one of --pppoe, --dhcp and --static")
}

func TestWANPortSafeguards(t *testing.T) {
	r := wanRouter(t)
	// A LAN device on Port 3.
	r.PutActive(fakerouter.Status{MAC: "aa:bb:cc:00:00:33", IP: "192.168.1.33", DisplayName: "NAS", Type: "WIRED", IsWired: true,
		UplinkName: "Dream Router 7", SwitchPort: 3})
	drctl(t, r, "wan", "port", "Internet 2", "Port 3").fails(t, 1, "LAN devices are connected to Port 3 (NAS)")
	if _, writes := r.PortRoles(); writes != 0 {
		t.Fatal("moved despite LAN devices")
	}
	// This machine itself is on Port 1: never.
	r.PutActive(fakerouter.Status{MAC: "02:00:00:00:00:01", IP: "192.168.1.99", Type: "WIRED", IsWired: true, UplinkName: "Dream Router 7", SwitchPort: 1})
	drctl(t, r, "wan", "port", "Internet 2", "Port 1", "--force").fails(t, 1, "this machine is connected to Port 1")
	fakeLocal(t, "02:00:00:00:00:01", "10.9.9.9") // recognised by MAC address alone
	drctl(t, r, "wan", "port", "Internet 2", "Port 1", "--force").fails(t, 1, "this machine is connected to Port 1")
	fakeLocal(t, "02:00:00:00:00:01", "192.168.1.99")

	// Moving Internet 1 to a port without its line: confirmed, then moved
	// back automatically when it doesn't come up.
	drctlIn(t, r, "Internet 1\n", "wan", "port", "Internet 1", "Port 2").fails(t, 1, "Internet 1 didn't come back within 300ms; moved the ports back")
	roles, writes := r.PortRoles()
	if roles["eth3"] != "WAN" || roles["eth1"] != "LAN" || writes != 2 {
		t.Fatalf("not moved back: %v (%d writes)", roles, writes)
	}
	drctlIn(t, r, "no\n", "wan", "port", "Internet 1", "Port 2").fails(t, 1, "the name didn't match")
}

func TestWANPrimary(t *testing.T) {
	r := wanRouter(t)
	drctl(t, r, "wan", "primary", "Internet 2").fails(t, 1, "Internet 2 is down; making it primary")
	r.SetPortLink("eth4", true) // the second line is connected (SFP+ here)
	drctl(t, r, "wan", "primary", "Internet 2", "--dry-run").ok(t).
		says(t, "would make Internet 2 the primary connection (traffic out via 198.51.100.9); Internet 1 would become the failover")
	drctlIn(t, r, "nope\n", "wan", "primary", "Internet 2").fails(t, 1, "nothing changed: the name didn't match")
	if r.Network("Internet 2")["wan_failover_priority"] != 2.0 {
		t.Fatal("switched without confirmation")
	}
	drctlIn(t, r, "Internet 2\n", "wan", "primary", "Internet 2").ok(t).
		says(t, "Internet 2 is now the primary connection (traffic out via 198.51.100.9", "Internet 1 is the failover")
	drctl(t, r, "wan", "list").ok(t).says(t, "Internet 1", "failover", "Internet 2", "primary")
	if n := r.Network("Internet 1"); n["wan_failover_priority"] != 2.0 || n["wan_load_balance_type"] != "failover-only" {
		t.Fatalf("Internet 1: %v", n)
	}
	drctl(t, r, "wan", "primary", "Internet 2").ok(t).says(t, "already the primary connection")
	// Changing the (new) primary now needs confirmation; Internet 1 doesn't.
	drctlIn(t, r, "nope\n", "wan", "set", "Internet 2", "--vlan", "20").fails(t, 1, "the name didn't match")
	drctl(t, r, "wan", "primary", "Internet 1", "--yes").ok(t).says(t, "Internet 1 is now the primary connection")

	// A primary that doesn't carry traffic is switched back: Internet 2 is
	// up, but its provider routes nothing.
	r.BlockTraffic("WAN2")
	drctl(t, r, "wan", "primary", "Internet 2", "--yes").fails(t, 1, "Internet 2 didn't carry traffic within 300ms; switched back to Internet 1")
	if n := r.Network("Internet 1"); n["wan_failover_priority"] != 1.0 || n["wan_load_balance_type"] == "failover-only" {
		t.Fatalf("not switched back: %v", n)
	}
}
