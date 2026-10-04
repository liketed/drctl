package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liketed/dreamrouter-go/fakerouter"
)

func TestNetworkCreateDelete(t *testing.T) {
	r := fakerouter.New()
	defer r.Close()
	fakeLocal(t, "02:00:00:00:00:01", "192.168.1.99")

	drctl(t, r, "network", "create", "Kids", "--vlan", "30", "--subnet", "192.168.30.1/24", "--dns", "94.140.14.15,94.140.15.16", "--dry-run").ok(t).
		says(t, "would create network Kids: VLAN 30, 192.168.30.0/24 (router 192.168.30.1), DHCP 192.168.30.6 - 192.168.30.254, DNS 94.140.14.15, 94.140.15.16")
	if r.Network("Kids") != nil {
		t.Fatal("--dry-run created the network")
	}
	drctl(t, r, "network", "create", "Kids", "--vlan", "30", "--subnet", "192.168.30.1/24", "--dns", "94.140.14.15,94.140.15.16").ok(t).
		says(t, "created network Kids: VLAN 30")
	n := r.Network("Kids")
	if n["vlan"] != 30.0 || n["vlan_enabled"] != true || n["dhcpd_dns_1"] != "94.140.14.15" || n["dhcpd_dns_2"] != "94.140.15.16" || n["dhcpd_start"] != "192.168.30.6" {
		t.Fatalf("stored: %v", n)
	}
	drctl(t, r, "network", "list").ok(t).says(t, "Kids", "192.168.30.0/24", "30", "94.140.14.15, 94.140.15.16")
	drctl(t, r, "network", "show", "kids").ok(t).says(t, "DNS servers      94.140.14.15, 94.140.15.16")

	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"kids", "--vlan", "31", "--subnet", "192.168.31.1/24"}, `named "Kids" already exists`},
		{[]string{"x", "--vlan", "30", "--subnet", "192.168.31.1/24"}, `"Kids" already uses VLAN 30`},
		{[]string{"x", "--vlan", "31", "--subnet", "192.168.1.1/24"}, `overlaps network "Default"`},
		{[]string{"x", "--vlan", "31", "--subnet", "8.8.8.1/24"}, "not a private range"},
		{[]string{"x", "--vlan", "31", "--subnet", "192.168.31.0/24"}, "router's address on it"},
		{[]string{"x", "--vlan", "4095", "--subnet", "192.168.31.1/24"}, "from 2 to 4094"},
		{[]string{"x", "--vlan", "31", "--subnet", "192.168.31.1/24", "--dns", "dns.adguard.com"}, "must be an IPv4 address"},
	} {
		drctl(t, r, append([]string{"network", "create"}, tc.args...)...).fails(t, 1, tc.want)
	}
	drctl(t, r, "network", "create", "x", "--subnet", "192.168.31.1/24").fails(t, 2, "needs --vlan and --subnet")

	drctl(t, r, "network", "delete", "Default").fails(t, 1, "is the router's own and can't be deleted")
	drctl(t, r, "network", "delete", "Kids", "--dry-run").ok(t).says(t, "would delete network Kids (VLAN 30, 192.168.30.0/24)")
	fakeLocal(t, "02:00:00:00:00:01", "192.168.30.50")
	drctl(t, r, "network", "delete", "Kids").fails(t, 1, "this machine (192.168.30.50) is on network Kids")
	fakeLocal(t, "02:00:00:00:00:01", "192.168.1.99")
	drctl(t, r, "network", "delete", "Kids").ok(t).says(t, "deleted network Kids")
	if r.Network("Kids") != nil {
		t.Fatal("not deleted")
	}
}

func TestWiFi(t *testing.T) {
	r := fakerouter.New()
	defer r.Close()
	fakeLocal(t, "02:00:00:00:00:01", "192.168.1.99")
	drctl(t, r, "network", "create", "Kids", "--vlan", "30", "--subnet", "192.168.30.1/24").ok(t)
	pwFile := filepath.Join(t.TempDir(), "pw")
	os.WriteFile(pwFile, []byte("correct horse battery\n"), 0o600)

	drctl(t, r, "wifi", "list").ok(t).says(t, "home", "Default", "2.4/5/6 GHz", "WPA2/WPA3", "enabled")

	res := drctl(t, r, "wifi", "create", "kids-wifi", "--network", "Kids", "--password-file", pwFile, "--dry-run")
	res.ok(t).says(t, `would create Wi-Fi network "kids-wifi" on network Kids (2.4/5 GHz, WPA2/WPA3)`)
	if len(r.WiFis()) != 1 || r.WiFiApplies() != 0 {
		t.Fatal("--dry-run created the Wi-Fi network")
	}
	res = drctl(t, r, "wifi", "create", "kids-wifi", "--network", "Kids", "--password-file", pwFile)
	res.ok(t).says(t, `created Wi-Fi network "kids-wifi" on network Kids (2.4/5 GHz, WPA2/WPA3)`, "disconnect for about 15-30 seconds")
	if strings.Contains(res.stdout+res.stderr, "correct horse") {
		t.Fatal("the password was printed")
	}
	w := r.WiFis()[1]
	if w["name"] != "kids-wifi" || w["x_passphrase"] != "correct horse battery" || w["networkconf_id"] != r.Network("Kids")["_id"] || w["enabled"] != true {
		t.Fatalf("stored: %v", w)
	}
	drctl(t, r, "wifi", "list").ok(t).says(t, "kids-wifi", "Kids", "2.4/5 GHz")

	// Checks the router doesn't make.
	drctlIn(t, r, "correct horse battery\n", "wifi", "create", "home", "--network", "Kids", "--password-stdin").fails(t, 1, `named "home" already exists`)
	drctlIn(t, r, strings.Repeat("z", 64)+"\n", "wifi", "create", "x", "--network", "Kids", "--password-stdin").fails(t, 1, "8 to 63 characters")
	drctlIn(t, r, "short12\n", "wifi", "create", "x", "--network", "Kids", "--password-stdin").fails(t, 1, "8 to 63 characters")
	drctl(t, r, "wifi", "create", "x", "--network", "IoT", "--password-file", pwFile).fails(t, 1, `no network named "IoT"`)
	drctl(t, r, "wifi", "create", "x", "--network", "Kids", "--password-file", pwFile, "--bands", "60g").fails(t, 1, `band "60g"`)
	drctl(t, r, "wifi", "create", "x", "--network", "Kids").fails(t, 2, "--password-file FILE or --password-stdin")

	drctl(t, r, "wifi", "disable", "kids-wifi").ok(t).says(t, `disabled Wi-Fi network "kids-wifi"`)
	drctl(t, r, "wifi", "disable", "kids-wifi").ok(t).says(t, "already disabled")
	drctl(t, r, "wifi", "enable", "kids-wifi").ok(t).says(t, `enabled Wi-Fi network "kids-wifi"`)
	drctlIn(t, r, "a new long password\n", "wifi", "password", "kids-wifi", "--password-stdin").ok(t).says(t, `changed the password of Wi-Fi network "kids-wifi"`)
	if r.WiFis()[1]["x_passphrase"] != "a new long password" {
		t.Fatal("password not changed")
	}
	drctlIn(t, r, "a new long password\n", "wifi", "password", "kids-wifi", "--password-stdin").ok(t).says(t, "already has that password")

	// The network can't be deleted while a Wi-Fi network uses it.
	drctl(t, r, "network", "delete", "Kids").fails(t, 1, "used by Wi-Fi network kids-wifi")

	// Never cut off the Wi-Fi network this machine is connected through.
	r.PutActive(fakerouter.Status{MAC: "02:00:00:00:00:01", IP: "192.168.1.99", ESSID: "home", Type: "WIRELESS"})
	drctl(t, r, "wifi", "disable", "home").fails(t, 1, `connected through Wi-Fi network "home"; refusing to disable it`)
	drctl(t, r, "wifi", "delete", "home").fails(t, 1, `connected through Wi-Fi network "home"; refusing to delete it`)

	before := r.WiFiApplies()
	drctl(t, r, "wifi", "delete", "kids-wifi").ok(t).says(t, `deleted Wi-Fi network "kids-wifi"`)
	if len(r.WiFis()) != 1 || r.WiFiApplies() != before+1 {
		t.Fatal("not deleted")
	}
	drctl(t, r, "network", "delete", "Kids").ok(t)
}
