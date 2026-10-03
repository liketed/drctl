package cli

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"sort"
	"strings"
	"time"

	"github.com/liketed/dreamrouter-go/check"
	"github.com/liketed/dreamrouter-go/unifi"
)

// localAddrs returns this machine's MAC and IP addresses, so drctl can
// refuse to block the machine it runs on. Replaced in tests.
var localAddrs = func() (macs, ips []string) {
	ifaces, _ := net.Interfaces()
	for _, i := range ifaces {
		if len(i.HardwareAddr) == 6 {
			macs = append(macs, i.HardwareAddr.String())
		}
		addrs, _ := i.Addrs()
		for _, a := range addrs {
			if p, err := netip.ParsePrefix(a.String()); err == nil {
				ips = append(ips, p.Addr().String())
			}
		}
	}
	return macs, ips
}

// clientInfo combines what the router knows about one device: its status
// (connected now or seen recently) and its stored record (name, note,
// reservation, blocked).
type clientInfo struct {
	status *unifi.ClientStatus
	device *unifi.ClientDevice
}

func (ci clientInfo) mac() string {
	if ci.device != nil {
		return ci.device.MAC
	}
	return ci.status.MAC
}

func (ci clientInfo) name() string {
	if ci.device != nil && ci.device.Name != "" {
		return ci.device.Name
	}
	if ci.status != nil && ci.status.Label() != "" {
		return ci.status.Label()
	}
	if ci.device != nil {
		return ci.device.Hostname
	}
	return ""
}

func (ci clientInfo) ip() string {
	if ci.status != nil && ci.status.Address() != "" {
		return ci.status.Address()
	}
	if ci.device != nil {
		if ci.device.UseFixedIP {
			return ci.device.FixedIP
		}
		return ci.device.LastIP
	}
	return ""
}

func (ci clientInfo) online() bool { return ci.status != nil && ci.status.Status == "online" }

func (ci clientInfo) blocked() bool {
	return (ci.device != nil && ci.device.Blocked) || (ci.status != nil && ci.status.Blocked)
}

func (ci clientInfo) wired() bool {
	return ci.status != nil && (ci.status.IsWired || ci.status.Type == "WIRED")
}

func (ci clientInfo) label() string {
	if n := ci.name(); n != "" {
		return fmt.Sprintf("%s (%s)", ci.mac(), n)
	}
	return ci.mac()
}

func (ci clientInfo) lastSeen() time.Time {
	if ci.status != nil && ci.status.LastSeen > 0 {
		return time.Unix(int64(ci.status.LastSeen), 0)
	}
	if ci.device != nil && ci.device.LastSeen > 0 {
		return time.Unix(ci.device.LastSeen, 0)
	}
	return time.Time{}
}

func (ci clientInfo) flags() string {
	var f []string
	if ci.blocked() {
		f = append(f, "blocked")
	}
	if ci.device != nil && ci.device.UseFixedIP {
		f = append(f, "reserved")
	}
	if ci.status != nil && ci.status.IsGuest {
		f = append(f, "guest")
	}
	if ci.device != nil && ci.device.Note != "" {
		f = append(f, "note")
	}
	return strings.Join(f, ",")
}

// band names the Wi-Fi band from the router's radio code.
func band(radio string) string {
	switch radio {
	case "ng":
		return "2.4GHz"
	case "na":
		return "5GHz"
	case "6e":
		return "6GHz"
	}
	return radio
}

// connection summarises how a device connects, e.g. "wired 1G" or "wifi 5GHz ax".
func (ci clientInfo) connection() string {
	s := ci.status
	switch {
	case s == nil:
		return ""
	case ci.wired():
		if s.WiredRateMbps >= 1000 {
			return fmt.Sprintf("wired %gG", s.WiredRateMbps/1000)
		}
		if s.WiredRateMbps > 0 {
			return fmt.Sprintf("wired %gM", s.WiredRateMbps)
		}
		return "wired"
	default:
		proto := s.RadioProto
		if proto == "ng" || proto == "na" { // Wi-Fi 4
			proto = "n"
		}
		return strings.TrimSpace("wifi " + band(s.Radio) + " " + proto)
	}
}

// via is where the device connects: the access point (and network name),
// or the switch/router and port.
func (ci clientInfo) via() string {
	s := ci.status
	if s == nil {
		return ""
	}
	if ci.wired() {
		if s.SwitchPort > 0 {
			return fmt.Sprintf("%s port %g", s.UplinkName, s.SwitchPort)
		}
		return s.UplinkName
	}
	if s.ESSID != "" && s.UplinkName != "" {
		return s.ESSID + " @ " + s.UplinkName
	}
	return s.ESSID + s.UplinkName
}

// durationText describes d, e.g. "3d 4h", "5h 12m" or "7m".
func durationText(d time.Duration) string {
	switch {
	case d >= 24*time.Hour:
		return fmt.Sprintf("%dd %dh", int(d.Hours())/24, int(d.Hours())%24)
	case d >= time.Hour:
		return fmt.Sprintf("%dh %dm", int(d.Hours()), int(d.Minutes())%60)
	default:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
}

func agoText(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	d := now().Sub(t)
	if d < time.Minute {
		return "just now"
	}
	return durationText(d) + " ago"
}

// bytesText formats a byte count, e.g. "1.2 GB".
func bytesText(b float64) string {
	units := []string{"B", "KB", "MB", "GB", "TB"}
	i := 0
	for b >= 1000 && i < len(units)-1 {
		b /= 1000
		i++
	}
	if i == 0 {
		return fmt.Sprintf("%.0f B", b)
	}
	return fmt.Sprintf("%.1f %s", b, units[i])
}

func rfc3339(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

// loadClients fetches connected devices, devices seen within offlineHours
// (none if 0), and the router's stored device records.
func loadClients(ctx context.Context, api *unifi.Client, offlineHours int) ([]clientInfo, error) {
	active, err := api.ListActiveClients(ctx)
	if err != nil {
		return nil, err
	}
	var offline []unifi.ClientStatus
	if offlineHours > 0 {
		if offline, err = api.ListOfflineClients(ctx, offlineHours); err != nil {
			return nil, err
		}
	}
	devices, err := api.ListClients(ctx)
	if err != nil {
		return nil, err
	}
	byMAC := map[string]*unifi.ClientDevice{}
	for i := range devices {
		byMAC[devices[i].MAC] = &devices[i]
	}
	var out []clientInfo
	seen := map[string]bool{}
	for _, list := range [][]unifi.ClientStatus{active, offline} {
		for i := range list {
			s := &list[i]
			if seen[s.MAC] {
				continue
			}
			seen[s.MAC] = true
			out = append(out, clientInfo{status: s, device: byMAC[s.MAC]})
		}
	}
	// Stored devices not seen in the period (e.g. blocked long ago).
	for i := range devices {
		if !seen[devices[i].MAC] {
			out = append(out, clientInfo{device: &devices[i]})
		}
	}
	return out, nil
}

func sortClients(list []clientInfo) {
	sort.SliceStable(list, func(i, j int) bool {
		a, errA := netip.ParseAddr(list[i].ip())
		b, errB := netip.ParseAddr(list[j].ip())
		switch {
		case errA == nil && errB == nil:
			return a.Less(b)
		case errA == nil || errB == nil:
			return errA == nil // devices without an address last
		}
		return list[i].mac() < list[j].mac()
	})
}

func clientsList(ctx context.Context, c *command) error {
	fs := c.flags("[--offline | --all | --blocked] [--wired | --wifi] [--days N] [--format table|csv|json]")
	offline := fs.Bool("offline", false, "list devices that are offline now but were seen recently (see --days)")
	all := fs.Bool("all", false, "list connected and recently seen devices")
	blocked := fs.Bool("blocked", false, "list blocked devices, however long ago they were seen")
	wired := fs.Bool("wired", false, "only wired devices")
	wifi := fs.Bool("wifi", false, "only Wi-Fi devices")
	days := fs.Int("days", 7, "with --offline or --all: how many days back to look")
	format := fs.String("format", "table", "output format: table, csv or json")
	if _, err := c.parse(0, 0); err != nil {
		return err
	}
	if n := countTrue(*offline, *all, *blocked); n > 1 {
		return usagef("use only one of --offline, --all and --blocked")
	}
	if *wired && *wifi {
		return usagef("use only one of --wired and --wifi")
	}
	if *days < 1 {
		return usagef("--days must be at least 1")
	}
	api, err := c.connect()
	if err != nil {
		return err
	}
	hours := 0
	if *offline || *all || *blocked {
		hours = *days * 24
	}
	clients, err := loadClients(ctx, api, hours)
	if err != nil {
		return err
	}
	var list []clientInfo
	for _, ci := range clients {
		switch {
		case *blocked && !ci.blocked():
			continue
		case *offline && (ci.online() || ci.status == nil):
			continue
		case !*offline && !*all && !*blocked && !ci.online():
			continue
		case *all && ci.status == nil:
			continue
		case *wired && (ci.status == nil || !ci.wired()):
			continue
		case *wifi && (ci.status == nil || ci.wired()):
			continue
		}
		list = append(list, ci)
	}
	sortClients(list)

	if *format == "table" {
		var header []string
		var rows [][]string
		onlineOnly := !*offline && !*all && !*blocked
		if onlineOnly {
			header = []string{"name", "ip", "mac", "connection", "via", "signal", "uptime", "down", "up", "flags"}
		} else {
			header = []string{"name", "ip", "mac", "status", "connection", "via", "last seen", "flags"}
		}
		for _, ci := range list {
			if onlineOnly {
				s := ci.status
				signal := ""
				if !ci.wired() && s.Signal != 0 {
					signal = fmt.Sprintf("%g dBm", s.Signal)
				}
				rows = append(rows, []string{shorten(ci.name(), 28), ci.ip(), ci.mac(), ci.connection(), shorten(ci.via(), 28), signal,
					durationText(time.Duration(s.Uptime) * time.Second), bytesText(s.TxBytes), bytesText(s.RxBytes), ci.flags()})
				continue
			}
			status, seen := "offline", agoText(ci.lastSeen())
			if ci.online() {
				status, seen = "online", "now"
			}
			rows = append(rows, []string{shorten(ci.name(), 28), ci.ip(), ci.mac(), status, ci.connection(), shorten(ci.via(), 28), seen, ci.flags()})
		}
		if err := output(c.env.Stdout, "table", header, rows); err != nil {
			return err
		}
		online := 0
		for _, ci := range list {
			if ci.online() {
				online++
			}
		}
		noun := "devices"
		if len(list) == 1 {
			noun = "device"
		}
		fmt.Fprintf(c.env.Stderr, "%d %s (%d online)\n", len(list), noun, online)
		return nil
	}
	var rows [][]string
	for _, ci := range list {
		row := []string{ci.name(), ci.ip(), ci.mac(), "offline", "", "", "", "", "", "", "", "", "", "", "", "", ""}
		if s := ci.status; s != nil {
			conn := "wifi"
			if ci.wired() {
				conn = "wired"
			}
			row = []string{ci.name(), ci.ip(), ci.mac(), s.Status, conn, s.NetworkName, s.ESSID, band(s.Radio), s.RadioProto,
				num(s.Signal), s.UplinkName, num(s.SwitchPort), num(s.WiredRateMbps), num(s.Uptime), num(s.TxBytes), num(s.RxBytes), s.OUI}
		}
		reserved, dnsName, note, hostname := false, "", "", ""
		if d := ci.device; d != nil {
			reserved, note, hostname = d.UseFixedIP, d.Note, d.Hostname
			if d.LocalDNSRecordEnabled {
				dnsName = d.LocalDNSRecord
			}
			if row[16] == "" {
				row[16] = d.OUI
			}
		}
		if hostname == "" && ci.status != nil {
			hostname = ci.status.Hostname
		}
		row = append(row, hostname, rfc3339(ci.lastSeen()), fmt.Sprint(ci.blocked()), fmt.Sprint(reserved), dnsName, note)
		rows = append(rows, row)
	}
	return output(c.env.Stdout, *format, []string{"name", "ip", "mac", "status", "connection", "network", "ssid", "band", "wifi_standard",
		"signal_dbm", "uplink", "port", "link_mbps", "uptime_seconds", "download_bytes", "upload_bytes", "vendor",
		"hostname", "last_seen", "blocked", "reserved", "dns_name", "note"}, rows)
}

func countTrue(bs ...bool) int {
	n := 0
	for _, b := range bs {
		if b {
			n++
		}
	}
	return n
}

// num formats a number, empty for zero.
func num(f float64) string {
	if f == 0 {
		return ""
	}
	return fmt.Sprint(f)
}

// findClient finds a device by MAC address, IP address or name. Names
// (and host names) need not be unique, so several matches are an error.
func findClient(clients []clientInfo, key string) (clientInfo, error) {
	if mac, err := check.MAC(key); err == nil {
		for _, ci := range clients {
			if ci.mac() == mac {
				return ci, nil
			}
		}
		return clientInfo{}, fmt.Errorf("the router doesn't know a device with MAC %s", mac)
	}
	var matches []clientInfo
	if ip, err := check.IPv4(key); err == nil {
		for _, ci := range clients {
			if ci.ip() == ip.String() {
				matches = append(matches, ci)
			}
		}
		if len(matches) == 0 {
			return clientInfo{}, fmt.Errorf("no device has (or recently had) IP %s", ip)
		}
	} else {
		for _, ci := range clients {
			names := []string{ci.name()}
			if ci.status != nil {
				names = append(names, ci.status.Hostname)
			}
			if ci.device != nil {
				names = append(names, ci.device.Hostname)
			}
			for _, n := range names {
				if n != "" && strings.EqualFold(n, key) {
					matches = append(matches, ci)
					break
				}
			}
		}
		if len(matches) == 0 {
			return clientInfo{}, fmt.Errorf("no device named %q; see \"drctl clients list --all\"", key)
		}
	}
	if len(matches) > 1 {
		var labels []string
		for _, ci := range matches {
			labels = append(labels, ci.label())
		}
		return clientInfo{}, fmt.Errorf("%d devices match %q, use the MAC address: %s", len(matches), key, strings.Join(labels, ", "))
	}
	return matches[0], nil
}

// showHours is how far back "clients show" looks for offline devices.
const showHours = 30 * 24

func clientsShow(ctx context.Context, c *command) error {
	c.flags("MAC|IP|NAME")
	args, err := c.parse(1, 1)
	if err != nil {
		return err
	}
	api, err := c.connect()
	if err != nil {
		return err
	}
	clients, err := loadClients(ctx, api, showHours)
	if err != nil {
		return err
	}
	ci, err := findClient(clients, args[0])
	if err != nil {
		return err
	}
	var lines [][2]string
	add := func(k, v string) {
		if v != "" {
			lines = append(lines, [2]string{k, v})
		}
	}
	s, d := ci.status, ci.device
	add("name", ci.name())
	add("mac", ci.mac())
	if s != nil {
		add("hostname", s.Hostname)
		add("vendor", s.OUI)
		add("model", s.ModelName)
	} else {
		add("hostname", d.Hostname)
		add("vendor", d.OUI)
	}
	switch {
	case ci.online():
		add("status", "online for "+durationText(time.Duration(s.Uptime)*time.Second))
	case ci.lastSeen().IsZero():
		add("status", "offline, never connected")
	default:
		add("status", "offline, last seen "+agoText(ci.lastSeen()))
	}
	ip := ci.ip()
	if d != nil && d.UseFixedIP {
		ip = d.FixedIP + " (reserved)"
		if s != nil && s.Address() != "" && s.Address() != d.FixedIP {
			ip += ", currently " + s.Address()
		}
	} else if ip != "" && !ci.online() {
		ip += " (last)"
	}
	add("ip", ip)
	if d != nil && d.LocalDNSRecordEnabled {
		add("dns name", d.LocalDNSRecord)
	}
	if s != nil {
		add("network", s.NetworkName)
		conn := ci.connection()
		if !ci.wired() {
			var parts []string
			if s.ESSID != "" {
				parts = append(parts, fmt.Sprintf("%q", s.ESSID))
			}
			if s.Channel != 0 {
				parts = append(parts, fmt.Sprintf("channel %g", s.Channel))
			}
			if s.Signal != 0 && ci.online() {
				parts = append(parts, fmt.Sprintf("signal %g dBm", s.Signal))
			}
			if len(parts) > 0 {
				conn += ", " + strings.Join(parts, ", ")
			}
		}
		add("connection", conn)
		add("via", ci.via())
		if ci.online() && s.WifiExperience > 0 {
			add("wifi experience", fmt.Sprintf("%g%%", s.WifiExperience))
		}
		if s.TxBytes > 0 || s.RxBytes > 0 {
			add("traffic", fmt.Sprintf("%s down, %s up", bytesText(s.TxBytes), bytesText(s.RxBytes)))
		}
		if s.FirstSeen > 0 {
			add("first seen", time.Unix(int64(s.FirstSeen), 0).Local().Format("2006-01-02 15:04"))
		}
		if s.IsGuest {
			add("guest", "yes")
		}
	}
	if ci.blocked() {
		add("blocked", "yes")
	} else {
		add("blocked", "no")
	}
	if d != nil {
		add("note", d.Note)
	}
	for _, l := range lines {
		fmt.Fprintf(c.env.Stdout, "%-16s %s\n", l[0]+":", l[1])
	}
	return nil
}

// unknownClientError means the router has no record of a MAC address.
type unknownClientError struct{ mac string }

func (e unknownClientError) Error() string {
	return fmt.Sprintf("the router doesn't know a device with MAC %s; see \"drctl clients list --all\"", e.mac)
}

// knownClient validates a MAC address and finds the router's record of it.
// The router accepts changes for any value, creating a new entry for an
// unknown MAC, so the commands handle unknown devices themselves (anyClient).
func (c *command) knownClient(ctx context.Context, macArg string) (*unifi.Client, clientInfo, error) {
	mac, err := check.MAC(macArg)
	if err != nil {
		return nil, clientInfo{}, err
	}
	api, err := c.connect()
	if err != nil {
		return nil, clientInfo{}, err
	}
	clients, err := loadClients(ctx, api, showHours)
	if err != nil {
		return nil, clientInfo{}, err
	}
	for _, ci := range clients {
		if ci.mac() == mac {
			return api, ci, nil
		}
	}
	return api, clientInfo{}, unknownClientError{mac}
}

// anyClient is knownClient for commands that also accept a MAC address the
// router doesn't know: it warns and returns unknown = true instead of failing.
func (c *command) anyClient(ctx context.Context, macArg string) (api *unifi.Client, ci clientInfo, unknown bool, err error) {
	api, ci, err = c.knownClient(ctx, macArg)
	var u unknownClientError
	if errors.As(err, &u) {
		c.warn("the router doesn't know a device with MAC %s (it has never connected, or was forgotten)", u.mac)
		return api, clientInfo{status: &unifi.ClientStatus{MAC: u.mac}}, true, nil
	}
	return api, ci, false, err
}

// record returns the device's stored record, which name, note and forget need.
func (ci clientInfo) record() (*unifi.ClientDevice, error) {
	if ci.device == nil {
		return nil, fmt.Errorf("the router has no stored record for %s yet; try again once it has been connected for a minute", ci.label())
	}
	return ci.device, nil
}

func clientsName(ctx context.Context, c *command) error {
	c.flags(`MAC NAME   (NAME "" removes the name)`)
	args, err := c.parse(2, 2)
	if err != nil {
		return err
	}
	api, ci, unknown, err := c.anyClient(ctx, args[0])
	if err != nil {
		return err
	}
	name := strings.TrimSpace(args[1])
	if unknown {
		return c.createClient(ctx, api, ci.mac(), name == "", map[string]any{"name": name},
			fmt.Sprintf("%s has no name", ci.mac()), fmt.Sprintf("%s %s %q", c.verb("named", "name"), ci.mac(), name))
	}
	d, err := ci.record()
	if err != nil {
		return err
	}
	if d.Name == name {
		c.out("%s already has that name", ci.label())
		return nil
	}
	if !c.dryRun {
		if _, err := api.UpdateClient(ctx, d.ID, map[string]any{"name": name}); err != nil {
			return err
		}
	}
	if name == "" {
		c.out("%s name of %s", c.verb("removed", "remove"), ci.label())
	} else {
		c.out("%s %s %q", c.verb("named", "name"), ci.label(), name)
	}
	return nil
}

func clientsNote(ctx context.Context, c *command) error {
	c.flags(`MAC TEXT   (TEXT "" removes the note)`)
	args, err := c.parse(2, 2)
	if err != nil {
		return err
	}
	api, ci, unknown, err := c.anyClient(ctx, args[0])
	if err != nil {
		return err
	}
	note := strings.TrimSpace(args[1])
	if unknown {
		return c.createClient(ctx, api, ci.mac(), note == "", map[string]any{"note": note, "noted": true},
			fmt.Sprintf("%s has no note", ci.mac()), fmt.Sprintf("%s note of %s: %s", c.verb("set", "set"), ci.mac(), note))
	}
	d, err := ci.record()
	if err != nil {
		return err
	}
	if d.Note == note {
		c.out("%s already has that note", ci.label())
		return nil
	}
	if !c.dryRun {
		if _, err := api.UpdateClient(ctx, d.ID, map[string]any{"note": note, "noted": note != ""}); err != nil {
			return err
		}
	}
	if note == "" {
		c.out("%s note of %s", c.verb("removed", "remove"), ci.label())
	} else {
		c.out("%s note of %s: %s", c.verb("set", "set"), ci.label(), note)
	}
	return nil
}

// isThisMachine reports whether the device is the machine drctl runs on.
func (ci clientInfo) isThisMachine() bool {
	macs, ips := localAddrs()
	for _, m := range macs {
		if strings.EqualFold(m, ci.mac()) {
			return true
		}
	}
	addrs := []string{ci.ip()}
	if ci.status != nil {
		addrs = append(addrs, ci.status.IP)
	}
	for _, ip := range ips {
		for _, a := range addrs {
			if a != "" && ip == a {
				return true
			}
		}
	}
	return false
}

func clientsBlock(ctx context.Context, c *command) error {
	c.flags("MAC")
	args, err := c.parse(1, 1)
	if err != nil {
		return err
	}
	// Blocking a device before it ever connects is allowed; the router
	// creates an entry for it.
	api, ci, _, err := c.anyClient(ctx, args[0])
	if err != nil {
		return err
	}
	if ci.isThisMachine() {
		return fmt.Errorf("%s is the machine drctl is running on; refusing to block it", ci.label())
	}
	if ci.blocked() {
		c.out("%s is already blocked", ci.label())
		return nil
	}
	if !c.dryRun {
		if err := api.BlockClient(ctx, ci.mac()); err != nil {
			return err
		}
	}
	c.out("%s %s; it can't connect until \"drctl clients unblock %s\"", c.verb("blocked", "block"), ci.label(), ci.mac())
	return nil
}

func clientsUnblock(ctx context.Context, c *command) error {
	c.flags("MAC")
	args, err := c.parse(1, 1)
	if err != nil {
		return err
	}
	// An unknown device can't be blocked. (The router would accept the
	// unblock, but create an empty entry for it, so don't send it.)
	api, ci, _, err := c.anyClient(ctx, args[0])
	if err != nil {
		return err
	}
	if !ci.blocked() {
		c.out("%s is not blocked", ci.label())
		return nil
	}
	if !c.dryRun {
		if err := api.UnblockClient(ctx, ci.mac()); err != nil {
			return err
		}
	}
	c.out("%s %s", c.verb("unblocked", "unblock"), ci.label())
	return nil
}

// clientsForget removes the router's record of a device: its name, note,
// reservation, DNS name and history. It reappears as new if it reconnects.
func clientsForget(ctx context.Context, c *command) error {
	c.flags("MAC")
	args, err := c.parse(1, 1)
	if err != nil {
		return err
	}
	api, ci, unknown, err := c.anyClient(ctx, args[0])
	if err != nil {
		return err
	}
	if unknown {
		// Nothing is stored, but the router accepts it, and it clears any
		// history the router keeps without listing.
		if !c.dryRun {
			if err := api.ForgetClient(ctx, ci.mac()); err != nil {
				return err
			}
		}
		c.out("%s %s (nothing was stored)", c.verb("forgot", "forget"), ci.mac())
		return nil
	}
	d, err := ci.record()
	if err != nil {
		return err
	}
	if ci.blocked() {
		return fmt.Errorf("%s is blocked; unblock it first (forgetting a blocked device would lose track of the block)", ci.label())
	}
	var lost []string
	if d.Name != "" {
		lost = append(lost, fmt.Sprintf("name %q", d.Name))
	}
	if d.Note != "" {
		lost = append(lost, "note")
	}
	if d.UseFixedIP {
		lost = append(lost, "reservation "+d.FixedIP)
	}
	if d.LocalDNSRecordEnabled {
		lost = append(lost, "DNS name "+d.LocalDNSRecord)
	}
	lost = append(lost, "history")
	if !c.dryRun {
		if err := api.ForgetClient(ctx, ci.mac()); err != nil {
			return err
		}
	}
	c.out("%s %s (%s)", c.verb("forgot", "forget"), ci.label(), strings.Join(lost, ", "))
	return nil
}

// createClient creates the router's record for an unknown device with the
// given fields, so a name or note is in place before it first connects.
// clearing means there is nothing to set (an empty name or note).
func (c *command) createClient(ctx context.Context, api *unifi.Client, mac string, clearing bool, fields map[string]any, nothing, done string) error {
	if clearing {
		c.out("%s", nothing)
		return nil
	}
	fields["mac"] = mac
	if !c.dryRun {
		if _, err := api.CreateClient(ctx, fields); err != nil {
			return err
		}
	}
	c.out("%s", done)
	return nil
}
