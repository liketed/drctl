package cli

import (
	"testing"

	"github.com/liketed/dreamrouter-go/fakerouter"
)

func TestRoutes(t *testing.T) {
	r := fakerouter.New()
	defer r.Close()
	drctl(t, r, "route", "add", "192.168.2.0/24", "192.168.1.4", "--dry-run").ok(t).says(t, "would add static route 192.168.2.0/24 via 192.168.1.4")
	if len(r.Routes()) != 0 {
		t.Fatal("--dry-run added the route")
	}
	drctl(t, r, "route", "add", "192.168.2.0/24", "192.168.1.4").ok(t).says(t, "added static route 192.168.2.0/24 via 192.168.1.4")
	rt := r.Routes()[0]
	if rt["static-route_network"] != "192.168.2.0/24" || rt["static-route_nexthop"] != "192.168.1.4" || rt["name"] != "192.168.2.0/24 via 192.168.1.4" ||
		rt["enabled"] != true || rt["static-route_distance"] != 1.0 || rt["static-route_type"] != "nexthop-route" {
		t.Fatalf("stored: %v", rt)
	}
	drctl(t, r, "route", "add", "10.10.0.0/16", "192.168.1.20", "--name", "lab", "--distance", "5", "--disabled").ok(t).says(t, "added static route 10.10.0.0/16 via 192.168.1.20 (disabled)")
	drctl(t, r, "route", "list").ok(t).says(t, "10.10.0.0/16", "192.168.1.20", "5", "disabled", "lab", "192.168.2.0/24", "192.168.1.4", "enabled")

	// What the router lets through, drctl refuses.
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"172.16.0.0/16", "8.8.8.8"}, "must be a router on one of your networks"},
		{[]string{"172.16.0.0/16", "192.168.1.1"}, "router's own address"},
		{[]string{"172.16.0.0/16", "192.168.1.30", "--name", "LAB"}, `named "lab" already exists`},
		{[]string{"192.168.2.0/24", "192.168.1.5"}, "already covers 192.168.2.0/24"},
		{[]string{"192.168.2.7/24", "192.168.1.5"}, "should be written 192.168.2.0/24"},
		{[]string{"192.168.1.0/24", "192.168.1.4"}, `overlaps the router's own network "Default"`},
		{[]string{"0.0.0.0/0", "192.168.1.4"}, "default route"},
		{[]string{"172.16.0.0/16", "192.168.1.30", "--distance", "300"}, "from 1 to 255"},
	} {
		drctl(t, r, append([]string{"route", "add"}, tc.args...)...).fails(t, 1, tc.want)
	}
	if len(r.Routes()) != 2 {
		t.Fatalf("invalid routes reached the router: %v", r.Routes())
	}

	drctl(t, r, "route", "enable", "lab").ok(t).says(t, "enabled static route 10.10.0.0/16 via 192.168.1.20")
	drctl(t, r, "route", "enable", "10.10.0.0/16").ok(t).says(t, "already enabled")
	drctl(t, r, "route", "disable", "192.168.2.0/24", "--dry-run").ok(t).says(t, "would disable")
	drctl(t, r, "route", "delete", "10.10.0.0/16").ok(t).says(t, "deleted static route 10.10.0.0/16 via 192.168.1.20")
	drctl(t, r, "route", "delete", "10.10.0.0/16").fails(t, 1, "no static route \"10.10.0.0/16\" (routes: 192.168.2.0/24)")
	if len(r.Routes()) != 1 {
		t.Fatal("not deleted")
	}
}
