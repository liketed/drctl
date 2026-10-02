package cli

import (
	"context"
	"fmt"
	"net/netip"
	"sort"
	"strings"
	"time"

	"github.com/liketed/dreamrouter-go/check"
	"github.com/liketed/dreamrouter-go/unifi"
)

// now is replaced in tests.
var now = time.Now

// untilText describes how long until t, e.g. "23h 41m" or "3d 4h".
func untilText(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	d := t.Sub(now())
	if d <= 0 {
		return "expired"
	}
	switch {
	case d >= 24*time.Hour:
		return fmt.Sprintf("%dd %dh", int(d.Hours())/24, int(d.Hours())%24)
	case d >= time.Hour:
		return fmt.Sprintf("%dh %dm", int(d.Hours()), int(d.Minutes())%60)
	default:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
}

func sortLeases(leases []unifi.Lease) {
	sort.SliceStable(leases, func(i, j int) bool {
		a, errA := netip.ParseAddr(leases[i].IP)
		b, errB := netip.ParseAddr(leases[j].IP)
		if errA != nil || errB != nil {
			return leases[i].IP < leases[j].IP
		}
		return a.Less(b)
	})
}

func reservedText(l unifi.Lease) string {
	if !l.UseFixedIP {
		return ""
	}
	if l.LocalDNSRecord != "" {
		return "yes (" + l.LocalDNSRecord + ")"
	}
	return "yes"
}

func leasesList(ctx context.Context, c *command) error {
	fs := c.flags("[--network NET] [--format table|csv|json]")
	network := fs.String("network", "", "only leases on this network")
	format := fs.String("format", "table", "output format: table, csv or json")
	if _, err := c.parse(0, 0); err != nil {
		return err
	}
	api, err := c.connect()
	if err != nil {
		return err
	}
	leases, err := api.ListLeases(ctx)
	if err != nil {
		return err
	}
	networkName := map[string]string{}
	if *network != "" || *format != "table" {
		lans, err := lanNetworks(ctx, api)
		if err != nil {
			return err
		}
		for _, n := range lans {
			networkName[n.ID] = n.Name
		}
		if *network != "" {
			n, err := pickNetwork(lans, *network)
			if err != nil {
				return err
			}
			var filtered []unifi.Lease
			for _, l := range leases {
				if l.NetworkID == n.ID {
					filtered = append(filtered, l)
				}
			}
			leases = filtered
		}
	}
	sortLeases(leases)

	if *format == "table" {
		var rows [][]string
		reserved := 0
		for _, l := range leases {
			if l.UseFixedIP {
				reserved++
			}
			rows = append(rows, []string{l.IP, l.MAC, l.Label(), shorten(l.OUI, 24), strings.ToLower(l.Status), untilText(l.Expires()), reservedText(l)})
		}
		if err := output(c.env.Stdout, "table", []string{"ip", "mac", "name", "vendor", "status", "expires", "reserved"}, rows); err != nil {
			return err
		}
		fmt.Fprintf(c.env.Stderr, "%d leases (%d reserved)\n", len(leases), reserved)
		return nil
	}
	var rows [][]string
	for _, l := range leases {
		expires := ""
		if t := l.Expires(); !t.IsZero() {
			expires = t.UTC().Format(time.RFC3339)
		}
		rows = append(rows, []string{l.IP, l.MAC, l.Label(), l.Hostname, l.OUI, strings.ToLower(l.Status),
			strings.ToLower(l.ClientType), expires, fmt.Sprint(l.UseFixedIP), l.LocalDNSRecord, networkName[l.NetworkID]})
	}
	return output(c.env.Stdout, *format,
		[]string{"ip", "mac", "name", "hostname", "vendor", "status", "connection", "expires", "reserved", "dns_name", "network"}, rows)
}

// leasesReserve turns a device's current lease into a DHCP reservation (or,
// with --dns-name, a reservation plus a DNS name) for the same address, using the
// same checks and output as "dhcp add" and "host add".
func leasesReserve(ctx context.Context, c *command) error {
	fs := c.flags("IP|MAC [--name NAME] [--dns-name NAME]")
	name := fs.String("name", "", "device name to show in the web UI")
	host := fs.String("dns-name", "", "also give the device this DNS name (like drctl host add)")
	args, err := c.parse(1, 1)
	if err != nil {
		return err
	}
	if *host != "" {
		if err := check.Name(check.NormalizeName(*host)); err != nil {
			return err
		}
	}
	api, err := c.connect()
	if err != nil {
		return err
	}
	leases, err := api.ListLeases(ctx)
	if err != nil {
		return err
	}
	l, err := findLease(leases, args[0])
	if err != nil {
		return err
	}

	// Run "dhcp add" or "host add" in this session, passing on --dry-run.
	var sub []string
	run := dhcpAdd
	subName := "dhcp add"
	if *host != "" {
		sub = []string{*host, l.IP, "--mac", l.MAC, "--network", l.NetworkID}
		if *name != "" {
			sub = append(sub, "--device-name", *name)
		}
		run, subName = hostAdd, "host add"
	} else {
		sub = []string{l.MAC, l.IP, "--network", l.NetworkID}
		if *name != "" {
			sub = append(sub, "--name", *name)
		}
	}
	if c.dryRun {
		sub = append(sub, "--dry-run")
	}
	return run(ctx, &command{env: c.env, name: subName, args: sub, api: api})
}

// findLease finds a lease by IP address or MAC address.
func findLease(leases []unifi.Lease, key string) (unifi.Lease, error) {
	if ip, err := check.IPv4(key); err == nil {
		for _, l := range leases {
			if l.IP == ip.String() {
				return l, nil
			}
		}
		return unifi.Lease{}, fmt.Errorf("no current DHCP lease for %s; see \"drctl leases list\"", ip)
	}
	mac, err := check.MAC(key)
	if err != nil {
		return unifi.Lease{}, usagef("%q is neither an IPv4 address nor a MAC address", key)
	}
	for _, l := range leases {
		if l.MAC == mac {
			return l, nil
		}
	}
	return unifi.Lease{}, fmt.Errorf("no current DHCP lease for %s; see \"drctl leases list\"", mac)
}

// shorten truncates s to at most n characters, marking the cut with "…".
func shorten(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return strings.TrimSpace(string(r[:n-1])) + "…"
}
