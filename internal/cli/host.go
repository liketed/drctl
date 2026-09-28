package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/liketed/drctl/internal/check"
	"github.com/liketed/drctl/internal/unifi"
)

// A "host" is a device with a DHCP reservation and a DNS name. The router
// stores the name on the device (its local DNS record) and only serves it while
// the device has a fixed IP, so the two are added and removed together.

func hostList(ctx context.Context, c *command) error {
	fs := c.flags("[--format table|csv|json]")
	format := fs.String("format", "table", "output format: table, csv or json")
	if _, err := c.parse(0, 0); err != nil {
		return err
	}
	api, err := c.connect()
	if err != nil {
		return err
	}
	s, err := c.loadState(ctx, api)
	if err != nil {
		return err
	}
	var hosts []unifi.ClientDevice
	for _, d := range s.clients {
		if d.UseFixedIP && d.LocalDNSRecordEnabled && d.LocalDNSRecord != "" {
			hosts = append(hosts, d)
		}
	}
	sortByIP(hosts)
	var rows [][]string
	for _, d := range hosts {
		rows = append(rows, []string{d.LocalDNSRecord, d.FixedIP, d.MAC, d.DisplayName(), s.networkName(d.NetworkID)})
	}
	return output(c.env.Stdout, *format, []string{"name", "ip", "mac", "device", "network"}, rows)
}

func hostAdd(ctx context.Context, c *command) error {
	fs := c.flags("NAME IP --mac MAC [--network NET] [--device-name NAME]")
	macArg := fs.String("mac", "", "the device's MAC address (required)")
	network := fs.String("network", "", "network name (default: the network whose subnet contains IP)")
	deviceName := fs.String("device-name", "", "device name to show in the web UI (default: NAME for a new device)")
	args, err := c.parse(2, 2)
	if err != nil {
		return err
	}
	if *macArg == "" {
		return usagef("--mac is required; usage: drctl host add %s", c.usage)
	}
	name := check.NormalizeName(args[0])
	if err := check.Name(name); err != nil {
		return err
	}
	if _, err := check.MAC(*macArg); err != nil {
		return err
	}
	if _, err := check.IPv4(args[1]); err != nil {
		return err
	}
	api, err := c.connect()
	if err != nil {
		return err
	}
	s, err := c.loadState(ctx, api)
	if err != nil {
		return err
	}
	r, err := s.parseReservation(*macArg, args[1], *deviceName, *network)
	if err != nil {
		return err
	}
	// The router rejects a name clash with a bare "api.err.Invalid"; explain it.
	records, err := api.ListDNS(ctx)
	if err != nil {
		return err
	}
	for _, rec := range records {
		if strings.EqualFold(rec.Key, name) {
			return fmt.Errorf("%s is already a static DNS record (%s); delete it with \"drctl dns delete %s\" or choose another name",
				name, describeDNS(rec), rec.Key)
		}
	}
	for i := range s.clients {
		d := &s.clients[i]
		if d.MAC != r.mac && d.UseFixedIP && d.LocalDNSRecordEnabled && strings.EqualFold(d.LocalDNSRecord, name) {
			return fmt.Errorf("%s is already the DNS name of %s (%s)", name, deviceLabel(d), d.FixedIP)
		}
	}
	if r.name == "" && s.byMAC(r.mac) == nil {
		r.name = name // label new devices in the web UI
	}
	old := s.byMAC(r.mac)
	var was []string
	if old != nil && old.UseFixedIP && old.FixedIP != r.ip.String() {
		was = append(was, old.FixedIP)
	}
	if old != nil && old.LocalDNSRecordEnabled && old.LocalDNSRecord != "" && !strings.EqualFold(old.LocalDNSRecord, name) {
		was = append(was, old.LocalDNSRecord)
	}
	noHostName := func(d unifi.ClientDevice) bool { return !(d.UseFixedIP && d.LocalDNSRecordEnabled) }
	action, err := c.applyReservation(ctx, api, s, r, map[string]any{"local_dns_record": name, "local_dns_record_enabled": true}, noHostName)
	if err != nil {
		return err
	}
	msg := fmt.Sprintf("%s host %s -> %s (%s)", c.actionVerb(action), name, r.ip, r.mac)
	if len(was) > 0 {
		msg += " (was " + strings.Join(was, ", ") + ")"
	}
	c.out("%s", msg)
	return nil
}

func hostDelete(ctx context.Context, c *command) error {
	fs := c.flags("NAME [--keep-reservation]")
	keep := fs.Bool("keep-reservation", false, "remove only the DNS name and keep the device's fixed IP")
	args, err := c.parse(1, 1)
	if err != nil {
		return err
	}
	name := check.NormalizeName(args[0])
	api, err := c.connect()
	if err != nil {
		return err
	}
	s, err := c.loadState(ctx, api)
	if err != nil {
		return err
	}
	for i := range s.clients {
		d := &s.clients[i]
		if !(d.LocalDNSRecordEnabled && strings.EqualFold(d.LocalDNSRecord, name)) {
			continue
		}
		if !*keep {
			return c.clearReservation(ctx, api, d)
		}
		if !c.dryRun {
			if _, err := api.UpdateClient(ctx, d.ID, map[string]any{"local_dns_record_enabled": false}); err != nil {
				return err
			}
		}
		c.out("%s DNS name %s from %s (kept reservation %s)", c.verb("removed", "remove"), d.LocalDNSRecord, deviceLabel(d), d.FixedIP)
		return nil
	}
	return fmt.Errorf("no host named %s; see \"drctl host list\"", name)
}
