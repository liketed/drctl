package cli

import (
	"testing"

	"github.com/liketed/dreamrouter-go/fakerouter"
)

func TestNetworkDHCP(t *testing.T) {
	r := fakerouter.New()
	defer r.Close()
	drctl(t, r, "network", "show").ok(t).says(t, "DNS servers      the router (default)", "Lease time       1d (default)", "NTP servers      none")

	drctl(t, r, "network", "dhcp", "--dns", "192.168.1.1,1.1.1.1", "--lease", "12h", "--dry-run").ok(t).
		says(t, "would change DNS servers from the router (default) to 192.168.1.1, 1.1.1.1 on network Default",
			"would change lease time from 1d (default) to 12h on network Default")
	if n := r.Network("Default"); n["dhcpd_dns_enabled"] != nil || n["dhcpd_leasetime"] != nil {
		t.Fatalf("--dry-run changed the network: %v", n)
	}

	drctl(t, r, "network", "dhcp", "default", "--dns", "192.168.1.1, 1.1.1.1", "--lease", "12h", "--ntp", "192.168.1.1", "--domain", "home.internal").ok(t).
		says(t, "changed DNS servers from the router (default) to 192.168.1.1, 1.1.1.1 on network Default",
			"changed lease time from 1d (default) to 12h", "changed NTP servers from none to 192.168.1.1",
			"changed domain from localdomain to home.internal", "devices pick up the change when they renew their lease (within 1d)\n")
	n := r.Network("Default")
	if n["dhcpd_dns_enabled"] != true || n["dhcpd_dns_1"] != "192.168.1.1" || n["dhcpd_dns_2"] != "1.1.1.1" || n["dhcpd_dns_3"] != "" ||
		n["dhcpd_leasetime"] != 43200.0 || n["dhcpd_ntp_enabled"] != true || n["dhcpd_ntp_1"] != "192.168.1.1" || n["domain_name"] != "home.internal" {
		t.Fatalf("stored: %v", n)
	}
	drctl(t, r, "network", "show").ok(t).says(t, "DNS servers      192.168.1.1, 1.1.1.1", "Lease time       12h\n", "NTP servers      192.168.1.1", "Domain           home.internal")
	drctl(t, r, "network", "show", "--format", "csv").ok(t).says(t, ",192.168.1.1 1.1.1.1,43200,192.168.1.1\n")

	// Nothing written when nothing changes.
	before := r.Network("Default")
	drctl(t, r, "network", "dhcp", "--dns", "192.168.1.1,1.1.1.1", "--lease", "720m").ok(t).says(t, "nothing to change on network Default")
	if after := r.Network("Default"); after["setting_preference"] != before["setting_preference"] || len(after) != len(before) {
		t.Fatal("wrote although nothing changed")
	}

	// Back to the defaults.
	drctl(t, r, "network", "dhcp", "--dns", "auto", "--lease", "default", "--ntp", "off").ok(t).
		says(t, "to the router (default)", "lease time from 12h to 1d (default)", "NTP servers from 192.168.1.1 to none")
	if n := r.Network("Default"); n["dhcpd_dns_enabled"] != false || n["dhcpd_dns_1"] != "" || n["dhcpd_leasetime"] != 86400.0 || n["dhcpd_ntp_enabled"] != false {
		t.Fatalf("defaults: %v", n)
	}
	drctl(t, r, "network", "dhcp", "--lease", "7d").ok(t).says(t, "to 7d")
	drctl(t, r, "network", "dhcp", "--lease", "1d12h").ok(t).says(t, "to 36h")

	// What the router lets through, drctl refuses.
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"--dns", "not-an-ip"}, `DNS server "not-an-ip" must be an IPv4 address`},
		{[]string{"--dns", "dns.google"}, "must be an IPv4 address"},
		{[]string{"--dns", "2606:4700:4700::1111"}, "must be an IPv4 address"},
		{[]string{"--dns", "1.1.1.1,1.1.1.1"}, "listed twice"},
		{[]string{"--dns", "1.1.1.1,1.1.1.2,1.1.1.3,1.1.1.4,1.1.1.5"}, "at most 4 DNS servers"},
		{[]string{"--lease", "0"}, "between 2 minutes"},
		{[]string{"--lease", "60s"}, "between 2 minutes"},
		{[]string{"--lease", "400d"}, "and a year"},
		{[]string{"--ntp", "pool.ntp.org"}, "must be an IPv4 address"},
		{[]string{"--domain", "home internal"}, "lower-case letters, digits and hyphens"},
	} {
		drctl(t, r, append([]string{"network", "dhcp"}, tc.args...)...).fails(t, 1, tc.want)
	}
	drctl(t, r, "network", "dhcp", "--lease", "soon").fails(t, 2, `lease time "soon" must be e.g. 12h`)
	drctl(t, r, "network", "dhcp").fails(t, 2, "nothing to change; usage")
	drctl(t, r, "network", "dhcp", "IoT", "--lease", "12h").fails(t, 1, `no network named "IoT"`)
}
