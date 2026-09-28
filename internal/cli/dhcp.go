package cli

import (
	"context"
	"fmt"
	"net/netip"
	"sort"
	"strings"

	"github.com/liketed/dreamrouter-go/check"
	"github.com/liketed/dreamrouter-go/unifi"
)

var dhcpCSVHeader = []string{"mac", "ip", "name", "network"}

// reservation is a validated request for a fixed IP.
type reservation struct {
	mac     string
	ip      netip.Addr
	name    string // optional: the device name shown in the web UI
	network unifi.Network
}

// state is what DHCP and host commands need from the router.
type state struct {
	networks []unifi.Network
	clients  []unifi.ClientDevice
}

func (c *command) loadState(ctx context.Context, api *unifi.Client) (*state, error) {
	networks, err := api.ListNetworks(ctx)
	if err != nil {
		return nil, err
	}
	clients, err := api.ListClients(ctx)
	if err != nil {
		return nil, err
	}
	return &state{networks: networks, clients: clients}, nil
}

func (s *state) byMAC(mac string) *unifi.ClientDevice {
	for i := range s.clients {
		if s.clients[i].MAC == mac {
			return &s.clients[i]
		}
	}
	return nil
}

func (s *state) networkName(id string) string {
	for _, n := range s.networks {
		if n.ID == id {
			return n.Name
		}
	}
	return id
}

// reservedBy returns another client holding ip as a fixed IP.
func (s *state) reservedBy(ip netip.Addr, exceptMAC string) *unifi.ClientDevice {
	for i := range s.clients {
		d := &s.clients[i]
		if d.UseFixedIP && d.FixedIP == ip.String() && d.MAC != exceptMAC {
			return d
		}
	}
	return nil
}

// inUseBy returns another client whose current address is ip.
func (s *state) inUseBy(ip netip.Addr, exceptMAC string) *unifi.ClientDevice {
	for i := range s.clients {
		d := &s.clients[i]
		if d.LastIP == ip.String() && d.MAC != exceptMAC {
			return d
		}
	}
	return nil
}

func deviceLabel(d *unifi.ClientDevice) string {
	if n := d.DisplayName(); n != "" {
		return fmt.Sprintf("%s (%s)", d.MAC, n)
	}
	return d.MAC
}

// parseReservation validates MAC, IP, optional name and network.
func (s *state) parseReservation(macArg, ipArg, name, network string) (reservation, error) {
	mac, err := check.MAC(macArg)
	if err != nil {
		return reservation{}, err
	}
	ip, err := check.IPv4(ipArg)
	if err != nil {
		return reservation{}, err
	}
	n, err := check.NetworkFor(s.networks, ip, network)
	if err != nil {
		return reservation{}, err
	}
	return reservation{mac: mac, ip: ip, name: strings.TrimSpace(name), network: n}, nil
}

// applyReservation creates or updates the client for r, adding extra fields
// (e.g. a DNS name). It returns "created", "updated" or "unchanged", from the
// user's point of view: giving a known device its first reservation (or,
// with isNew, its first DNS name) is "created".
func (c *command) applyReservation(ctx context.Context, api *unifi.Client, s *state, r reservation, extra map[string]any,
	isNew func(unifi.ClientDevice) bool) (string, error) {
	if other := s.reservedBy(r.ip, r.mac); other != nil {
		return "", fmt.Errorf("%s is already reserved for %s", r.ip, deviceLabel(other))
	}
	if other := s.inUseBy(r.ip, r.mac); other != nil {
		c.warn("%s is currently in use by %s; it will get another address when its lease renews", r.ip, deviceLabel(other))
	}
	fields := map[string]any{"use_fixedip": true, "fixed_ip": r.ip.String(), "network_id": r.network.ID}
	if r.name != "" {
		fields["name"] = r.name
	}
	for k, v := range extra {
		fields[k] = v
	}
	existing := s.byMAC(r.mac)
	if existing == nil {
		fields["mac"] = r.mac
		if !c.dryRun {
			created, err := api.CreateClient(ctx, fields)
			if err != nil {
				return "", err
			}
			s.clients = append(s.clients, created)
		} else {
			s.clients = append(s.clients, unifi.ClientDevice{MAC: r.mac, UseFixedIP: true, FixedIP: r.ip.String()})
		}
		return "created", nil
	}
	if clientMatches(*existing, fields) {
		return "unchanged", nil
	}
	action := "updated"
	if isNew(*existing) {
		action = "created"
	}
	if !c.dryRun {
		updated, err := api.UpdateClient(ctx, existing.ID, fields)
		if err != nil {
			return "", err
		}
		*existing = updated
	} else {
		existing.UseFixedIP, existing.FixedIP = true, r.ip.String()
	}
	return action, nil
}

// noReservation reports whether a device has no fixed IP yet.
func noReservation(d unifi.ClientDevice) bool { return !d.UseFixedIP }

// clientMatches reports whether d already has all the given field values.
func clientMatches(d unifi.ClientDevice, fields map[string]any) bool {
	current := map[string]any{
		"use_fixedip": d.UseFixedIP, "fixed_ip": d.FixedIP, "network_id": d.NetworkID, "name": d.Name,
		"local_dns_record": d.LocalDNSRecord, "local_dns_record_enabled": d.LocalDNSRecordEnabled,
	}
	for k, v := range fields {
		if current[k] != v {
			return false
		}
	}
	return true
}

func describeReservation(r reservation, s *state) string {
	desc := fmt.Sprintf("%s -> %s", r.mac, r.ip)
	name := r.name
	if name == "" {
		if d := s.byMAC(r.mac); d != nil {
			name = d.DisplayName()
		}
	}
	if name != "" {
		desc += fmt.Sprintf(" (%s)", name)
	}
	return desc
}

func dhcpList(ctx context.Context, c *command) error {
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
	var reserved []unifi.ClientDevice
	for _, d := range s.clients {
		if d.UseFixedIP {
			reserved = append(reserved, d)
		}
	}
	sortByIP(reserved)
	var rows [][]string
	for _, d := range reserved {
		rows = append(rows, []string{d.MAC, d.FixedIP, d.DisplayName(), s.networkName(d.NetworkID)})
	}
	if *format == "table" {
		var table [][]string
		for i, d := range reserved {
			dns := ""
			if d.LocalDNSRecordEnabled {
				dns = d.LocalDNSRecord
			}
			table = append(table, append(rows[i], dns))
		}
		return output(c.env.Stdout, "table", []string{"mac", "ip", "name", "network", "dns name"}, table)
	}
	return output(c.env.Stdout, *format, dhcpCSVHeader, rows)
}

func sortByIP(devices []unifi.ClientDevice) {
	sort.SliceStable(devices, func(i, j int) bool {
		a, errA := netip.ParseAddr(devices[i].FixedIP)
		b, errB := netip.ParseAddr(devices[j].FixedIP)
		if errA != nil || errB != nil {
			return devices[i].FixedIP < devices[j].FixedIP
		}
		return a.Less(b)
	})
}

func dhcpAdd(ctx context.Context, c *command) error {
	fs := c.flags("MAC IP [--name NAME] [--network NET]")
	name := fs.String("name", "", "device name to show in the web UI")
	network := fs.String("network", "", "network name (default: the network whose subnet contains IP)")
	args, err := c.parse(2, 2)
	if err != nil {
		return err
	}
	if _, err := check.MAC(args[0]); err != nil {
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
	r, err := s.parseReservation(args[0], args[1], *name, *network)
	if err != nil {
		return err
	}
	old := s.byMAC(r.mac)
	var was string
	if old != nil && old.UseFixedIP && old.FixedIP != r.ip.String() {
		was = fmt.Sprintf(" (was %s)", old.FixedIP)
	}
	action, err := c.applyReservation(ctx, api, s, r, nil, noReservation)
	if err != nil {
		return err
	}
	c.out("%s %s%s", c.actionVerb(action), describeReservation(r, s), was)
	return nil
}

// actionVerb turns "created"/"updated"/"unchanged" into dry-run aware text.
func (c *command) actionVerb(action string) string {
	switch action {
	case "created":
		return c.verb("created", "create")
	case "updated":
		return c.verb("updated", "update")
	case "cleared":
		return c.verb("cleared", "clear")
	}
	return action
}

func dhcpDelete(ctx context.Context, c *command) error {
	fs := c.flags("MAC [--forget]")
	forget := fs.Bool("forget", false, "also remove the device from the router entirely (name and history)")
	args, err := c.parse(1, 1)
	if err != nil {
		return err
	}
	mac, err := check.MAC(args[0])
	if err != nil {
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
	d := s.byMAC(mac)
	if d == nil {
		return fmt.Errorf("no device with MAC %s", mac)
	}
	if *forget {
		if !c.dryRun {
			if err := api.ForgetClient(ctx, mac); err != nil {
				return err
			}
		}
		c.out("%s device %s", c.verb("forgot", "forget"), deviceLabel(d))
		return nil
	}
	if !d.UseFixedIP {
		return fmt.Errorf("%s has no DHCP reservation", deviceLabel(d))
	}
	return c.clearReservation(ctx, api, d)
}

// clearReservation removes a device's fixed IP (and therefore its DNS name,
// which the router requires a fixed IP for), keeping the device itself.
func (c *command) clearReservation(ctx context.Context, api *unifi.Client, d *unifi.ClientDevice) error {
	hadDNS := d.LocalDNSRecordEnabled
	if !c.dryRun {
		if _, err := api.UpdateClient(ctx, d.ID, map[string]any{"use_fixedip": false, "local_dns_record_enabled": false}); err != nil {
			return err
		}
	}
	msg := fmt.Sprintf("%s reservation %s -> %s", c.verb("removed", "remove"), deviceLabel(d), d.FixedIP)
	if hadDNS {
		msg += fmt.Sprintf(" and DNS name %s", d.LocalDNSRecord)
	}
	c.out("%s", msg)
	return nil
}

// dhcpImport adds/updates (or with --delete, removes) the reservations in a
// CSV file with a single login. Lines are mac,ip[,name[,network]], optionally
// with a header row (the output of "dhcp list --format csv"). For --delete,
// lines are mac[,ip]; when an IP is given, the reservation is only removed if
// it still has that IP.
func dhcpImport(ctx context.Context, c *command) error {
	fs := c.flags("FILE [--delete]   (FILE may be - for stdin)")
	del := fs.Bool("delete", false, "remove the reservations in FILE instead of adding them")
	args, err := c.parse(1, 1)
	if err != nil {
		return err
	}
	path := args[0]
	header, lines, err := readCSV(path, c.env.stdin(), "mac")
	if err != nil {
		return err
	}
	if header == nil {
		header = dhcpCSVHeader
	}
	api, err := c.connect()
	if err != nil {
		return err
	}
	s, err := c.loadState(ctx, api)
	if err != nil {
		return err
	}
	if *del {
		return c.importDHCPDelete(ctx, api, s, path, header, lines)
	}

	// Validate every line, and check for conflicts, before changing anything.
	var batch []reservation
	macs, ips := map[string]int{}, map[string]int{}
	for _, l := range lines {
		if len(l.fields) < 2 || len(l.fields) > 4 {
			return lineErr(path, l, "expected 'mac,ip[,name[,network]]', got %q", strings.Join(l.fields, ","))
		}
		r, err := s.parseReservation(l.column(header, "mac"), l.column(header, "ip"), l.column(header, "name"), l.column(header, "network"))
		if err != nil {
			return lineErr(path, l, "%v", err)
		}
		if prev, ok := macs[r.mac]; ok {
			return lineErr(path, l, "MAC %s is also on line %d", r.mac, prev)
		}
		if prev, ok := ips[r.ip.String()]; ok {
			return lineErr(path, l, "IP %s is also on line %d", r.ip, prev)
		}
		macs[r.mac], ips[r.ip.String()] = l.num, l.num
		batch = append(batch, r)
	}
	for i, r := range batch {
		if other := s.reservedBy(r.ip, r.mac); other != nil {
			if _, movedByFile := macs[other.MAC]; !movedByFile {
				return lineErr(path, lines[i], "%s is already reserved for %s; nothing was changed", r.ip, deviceLabel(other))
			}
		}
	}
	batch, err = orderReservations(s, batch)
	if err != nil {
		return err
	}
	counts := map[string]int{}
	for _, r := range batch {
		old := s.byMAC(r.mac)
		var was string
		if old != nil && old.UseFixedIP && old.FixedIP != r.ip.String() {
			was = fmt.Sprintf(" (was %s)", old.FixedIP)
		}
		action, err := c.applyReservation(ctx, api, s, r, nil, noReservation)
		if err != nil {
			return fmt.Errorf("%s: %w (reservations before this one were applied)", describeReservation(r, s), err)
		}
		c.out("%s %s%s", c.actionVerb(action), describeReservation(r, s), was)
		counts[action]++
	}
	c.summary(counts, "created", "updated", "unchanged")
	return nil
}

// orderReservations returns batch in an order the router accepts: an IP that
// the file moves from one device to another is freed before it is reused.
// IP swaps (cycles) are rejected, since every order would conflict.
func orderReservations(s *state, batch []reservation) ([]reservation, error) {
	holder := map[string]string{} // ip -> mac, as reservations change
	for _, d := range s.clients {
		if d.UseFixedIP {
			holder[d.FixedIP] = d.MAC
		}
	}
	var ordered []reservation
	remaining := batch
	for len(remaining) > 0 {
		var blocked []reservation
		for _, r := range remaining {
			if h := holder[r.ip.String()]; h != "" && h != r.mac {
				blocked = append(blocked, r)
				continue
			}
			for ip, mac := range holder {
				if mac == r.mac {
					delete(holder, ip)
				}
			}
			holder[r.ip.String()] = r.mac
			ordered = append(ordered, r)
		}
		if len(blocked) == len(remaining) {
			var list []string
			for _, r := range blocked {
				list = append(list, fmt.Sprintf("%s -> %s (currently %s's)", r.mac, r.ip, holder[r.ip.String()]))
			}
			return nil, fmt.Errorf("these reservations swap IPs between devices, which can't be done in one step: %s; "+
				"remove one of the reservations first (drctl dhcp delete MAC), then import again; nothing was changed",
				strings.Join(list, ", "))
		}
		remaining = blocked
	}
	return ordered, nil
}

func (c *command) importDHCPDelete(ctx context.Context, api *unifi.Client, s *state, path string, header []string, lines []csvLine) error {
	type target struct {
		mac string
		ip  string
	}
	var batch []target
	for _, l := range lines {
		if len(l.fields) > 4 {
			return lineErr(path, l, "expected 'mac[,ip]', got %q", strings.Join(l.fields, ","))
		}
		mac, err := check.MAC(l.column(header, "mac"))
		if err != nil {
			return lineErr(path, l, "%v", err)
		}
		ip := l.column(header, "ip")
		if ip != "" {
			a, err := check.IPv4(ip)
			if err != nil {
				return lineErr(path, l, "%v", err)
			}
			ip = a.String()
		}
		batch = append(batch, target{mac, ip})
	}
	counts := map[string]int{}
	for _, t := range batch {
		d := s.byMAC(t.mac)
		switch {
		case d == nil || !d.UseFixedIP:
			c.out("not found %s", t.mac)
			counts["not found"]++
		case t.ip != "" && d.FixedIP != t.ip:
			c.out("skipped %s (reserved IP is %s, file says %s)", deviceLabel(d), d.FixedIP, t.ip)
			counts["skipped"]++
		default:
			if err := c.clearReservation(ctx, api, d); err != nil {
				return err
			}
			counts["removed"]++
		}
	}
	if c.dryRun {
		counts["would remove"] = counts["removed"]
		c.summary(counts, "would remove", "not found", "skipped")
		return nil
	}
	c.summary(counts, "removed", "not found", "skipped")
	return nil
}
