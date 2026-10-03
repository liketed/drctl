package cli

import (
	"strings"
	"testing"

	"github.com/liketed/dreamrouter-go/fakerouter"
)

func TestPortForward(t *testing.T) {
	r := fakerouter.New()
	defer r.Close()

	drctl(t, r, "portforward", "add", "web", "8443", "192.168.1.20:443", "--proto", "tcp", "--dry-run").ok(t).
		says(t, `would create port forward "web": opens TCP 8443 on the internet, forwarded to 192.168.1.20:443`)
	if len(r.PortForwards()) != 0 {
		t.Fatal("--dry-run created a rule")
	}
	drctl(t, r, "portforward", "add", "web", "8443", "192.168.1.20:443", "--proto", "tcp").ok(t).
		says(t, `created port forward "web": opens TCP 8443 on the internet, forwarded to 192.168.1.20:443`)
	f := r.PortForwards()[0]
	if f["enabled"] != true || f["dst_port"] != "8443" || f["fwd_port"] != "443" || f["fwd"] != "192.168.1.20" ||
		f["proto"] != "tcp" || f["src"] != "any" || f["pfwd_interface"] != "wan" || f["log"] != false {
		t.Fatalf("stored: %v", f)
	}

	drctl(t, r, "portforward", "add", "games", "27000-27010", "192.168.1.51", "--proto", "udp", "--from", "203.0.113.9/24", "--disabled").ok(t).
		says(t, `created port forward "games" (disabled): UDP 27000-27010 -> 192.168.1.51:27000-27010 from 203.0.113.0/24; nothing is open until "drctl portforward enable games"`)
	if f := r.PortForwards()[1]; f["enabled"] != false || f["src"] != "203.0.113.0/24" {
		t.Fatalf("stored: %v", f)
	}

	res := drctl(t, r, "portforward", "list")
	res.ok(t).says(t, "games", "UDP", "27000-27010", "192.168.1.51:27000-27010", "203.0.113.0/24", "disabled",
		"web", "TCP", "8443", "192.168.1.20:443", "enabled")
	if strings.Index(res.stdout, "games") > strings.Index(res.stdout, "web") {
		t.Fatalf("not sorted by name:\n%s", res.stdout)
	}
	if !strings.Contains(res.stderr, "2 port forwards (1 enabled)") {
		t.Fatalf("summary: %q", res.stderr)
	}
	drctl(t, r, "portforward", "list", "--format", "csv").ok(t).
		says(t, "name,enabled,protocol,port,forward_ip,forward_port,source,wan,log\n",
			"web,true,tcp,8443,192.168.1.20,443,any,wan,false\n")

	// Checks the router itself doesn't make.
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"web2", "8443", "192.168.1.21"}, `TCP/UDP 8443 is already forwarded by "web"`},
		{[]string{"WEB", "9000", "192.168.1.21"}, `named "web" already exists`},
		{[]string{"x", "40090-40080", "192.168.1.21"}, "reversed; write it as 40080-40090"},
		{[]string{"x", "40100-40110", "192.168.1.21:40100-40105"}, "can only be forwarded to the same ports (40100-40110), not 40100-40105"},
		{[]string{"x", "40010-40020", "192.168.1.21:50010-50020"}, "only a single port can be forwarded to a different one"},
		{[]string{"x", "9000", "8.8.8.8"}, "forward address:"},
		{[]string{"x", "9000", "192.168.1.1"}, "router's own address"},
		{[]string{"x", "9000", "192.168.1.21", "--wan", "wan9"}, "must be wan, wan2 or both"},
		{[]string{"x", "9000", "192.168.1.21", "--wan", "both"}, "needs a second internet connection"},
		{[]string{"x", "70000", "192.168.1.21"}, "outside 1–65535"},
		{[]string{"x", "9000", "192.168.1.21", "--proto", "icmp"}, "must be tcp, udp or both"},
		{[]string{"x", "9000", "192.168.1.21", "--from", "nope"}, `source "nope"`},
	} {
		drctl(t, r, append([]string{"portforward", "add"}, tc.args...)...).fails(t, 1, tc.want)
	}
	if n := len(r.PortForwards()); n != 2 {
		t.Fatalf("invalid rules reached the router: %d rules", n)
	}
	// A disabled duplicate is allowed, but can't be enabled while the other is.
	drctl(t, r, "portforward", "add", "web-spare", "8443", "192.168.1.22", "--disabled").ok(t)
	drctl(t, r, "portforward", "enable", "web-spare").fails(t, 1, `already forwarded by "web"`)

	drctl(t, r, "portforward", "enable", "games", "--dry-run").ok(t).says(t, `would enable port forward "games": opens UDP 27000-27010 to 203.0.113.0/24`)
	if r.PortForwards()[1]["enabled"] != false {
		t.Fatal("--dry-run enabled the rule")
	}
	drctl(t, r, "portforward", "enable", "games").ok(t).says(t, `enabled port forward "games"`)
	drctl(t, r, "portforward", "enable", "games").ok(t).says(t, `port forward "games" is already enabled`)
	drctl(t, r, "portforward", "disable", "GAMES").ok(t).says(t, `disabled port forward "games" (UDP 27000-27010 -> 192.168.1.51:27000-27010 from 203.0.113.0/24); nothing is open now`)
	if f := r.PortForwards()[1]; f["enabled"] != false || f["src"] != "203.0.113.0/24" || f["dst_port"] != "27000-27010" {
		t.Fatalf("disable changed other fields: %v", f)
	}

	drctl(t, r, "portforward", "delete", "web", "--dry-run").ok(t).says(t, `would delete port forward "web" (TCP 8443 -> 192.168.1.20:443)`)
	drctl(t, r, "portforward", "delete", "web").ok(t).says(t, `deleted port forward "web"`)
	drctl(t, r, "portforward", "delete", "web").fails(t, 1, `no port forward named "web" (port forwards: games, web-spare)`)
	drctl(t, r, "portforward", "enable", "web-spare").ok(t)
	if n := len(r.PortForwards()); n != 2 {
		t.Fatalf("rules: %d", n)
	}
}
