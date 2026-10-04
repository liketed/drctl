package cli

import (
	"bufio"
	"context"
	"fmt"
	"net/netip"
	"os"
	"sort"
	"strings"

	"github.com/liketed/dreamrouter-go/check"
	"github.com/liketed/dreamrouter-go/unifi"
)

const wifiDropNote = "Wi-Fi devices on every Wi-Fi network disconnect for about 15-30 seconds while the access points apply this"

func networkCreate(ctx context.Context, c *command) error {
	fs := c.flags("NAME --vlan N --subnet ROUTER-IP/PREFIX [--dhcp-range START-STOP] [--dns IP[,IP...]]")
	vlan := fs.Int("vlan", 0, "VLAN ID, 2-4094")
	subnetArg := fs.String("subnet", "", "the router's address on the new network, with the prefix, e.g. 192.168.30.1/24")
	dhcpRange := fs.String("dhcp-range", "", "addresses to hand out, e.g. 192.168.30.6-192.168.30.254 (default: .6 to the last address but one)")
	dns := fs.String("dns", "", "DNS servers to hand out, up to 4 (default: the router itself)")
	args, err := c.parse(1, 1)
	if err != nil {
		return err
	}
	if *vlan == 0 || *subnetArg == "" {
		return usagef("a new network needs --vlan and --subnet")
	}
	spec := unifi.NetworkSpec{Name: args[0], VLAN: *vlan, Subnet: *subnetArg, DNS: splitList(*dns)}
	if *dhcpRange != "" {
		start, stop, ok := strings.Cut(*dhcpRange, "-")
		if !ok {
			return usagef("--dhcp-range %q must be START-STOP", *dhcpRange)
		}
		spec.DHCPStart, spec.DHCPStop = strings.TrimSpace(start), strings.TrimSpace(stop)
	}
	api, err := c.connect()
	if err != nil {
		return err
	}
	existing, err := api.ListNetworks(ctx)
	if err != nil {
		return err
	}
	if err := check.NewNetwork(&spec, existing); err != nil {
		return err
	}
	if !c.dryRun {
		if _, err := api.CreateNetwork(ctx, spec); err != nil {
			return err
		}
	}
	dnsText := "the router"
	if len(spec.DNS) > 0 {
		dnsText = strings.Join(spec.DNS, ", ")
	}
	p := netip.MustParsePrefix(spec.Subnet)
	c.out("%s network %s: VLAN %d, %s (router %s), DHCP %s - %s, DNS %s", c.verb("created", "create"), spec.Name, spec.VLAN,
		p.Masked(), p.Addr(), spec.DHCPStart, spec.DHCPStop, dnsText)
	return nil
}

func networkDelete(ctx context.Context, c *command) error {
	c.flags("NAME")
	args, err := c.parse(1, 1)
	if err != nil {
		return err
	}
	api, err := c.connect()
	if err != nil {
		return err
	}
	lans, err := lanNetworks(ctx, api)
	if err != nil {
		return err
	}
	n, err := pickNetwork(lans, args[0])
	if err != nil {
		return err
	}
	if n.NoDelete {
		return fmt.Errorf("network %s is the router's own and can't be deleted", n.Name)
	}
	wifis, err := api.ListWiFi(ctx)
	if err != nil {
		return err
	}
	var using []string
	for _, w := range wifis {
		if w.NetworkID == n.ID {
			using = append(using, w.Name)
		}
	}
	if len(using) > 0 {
		return fmt.Errorf("network %s is used by Wi-Fi network %s; delete or move that first", n.Name, strings.Join(using, ", "))
	}
	if p, err := netip.ParsePrefix(n.Subnet); err == nil {
		_, ips := localAddrs()
		for _, ip := range ips {
			if a, err := netip.ParseAddr(ip); err == nil && p.Masked().Contains(a) {
				return fmt.Errorf("this machine (%s) is on network %s; refusing to delete it", ip, n.Name)
			}
		}
	}
	if !c.dryRun {
		if err := api.DeleteNetwork(ctx, n.ID); err != nil {
			return err
		}
	}
	c.out("%s network %s (VLAN %d, %s)", c.verb("deleted", "delete"), n.Name, n.VLAN, subnet(n))
	return nil
}

func bandsText(bands []string) string {
	if len(bands) == 0 {
		return "-"
	}
	names := map[string]string{"2g": "2.4", "5g": "5", "6g": "6"}
	var out []string
	for _, b := range bands {
		if n, ok := names[b]; ok {
			out = append(out, n)
		} else {
			out = append(out, b)
		}
	}
	return strings.Join(out, "/") + " GHz"
}

func findWiFi(list []unifi.WiFi, name string) (unifi.WiFi, error) {
	for _, w := range list {
		if w.Name == name {
			return w, nil
		}
	}
	for _, w := range list {
		if strings.EqualFold(w.Name, name) {
			return w, nil
		}
	}
	var names []string
	for _, w := range list {
		names = append(names, w.Name)
	}
	return unifi.WiFi{}, fmt.Errorf("no Wi-Fi network named %q (Wi-Fi networks: %s)", name, strings.Join(names, ", "))
}

// connectedVia returns the Wi-Fi network this machine is connected through,
// if any, so drctl doesn't cut itself off.
func connectedVia(ctx context.Context, api *unifi.Client) string {
	active, err := api.ListActiveClients(ctx)
	if err != nil {
		return ""
	}
	macs, ips := localAddrs()
	for _, s := range active {
		for _, m := range macs {
			if strings.EqualFold(m, s.MAC) && s.ESSID != "" {
				return s.ESSID
			}
		}
		for _, ip := range ips {
			if ip == s.IP && s.ESSID != "" {
				return s.ESSID
			}
		}
	}
	return ""
}

func wifiList(ctx context.Context, c *command) error {
	fs := c.flags("[--format table|csv|json]")
	format := fs.String("format", "table", "output format: table, csv or json")
	if _, err := c.parse(0, 0); err != nil {
		return err
	}
	api, err := c.connect()
	if err != nil {
		return err
	}
	list, err := api.ListWiFi(ctx)
	if err != nil {
		return err
	}
	nets, err := api.ListNetworks(ctx)
	if err != nil {
		return err
	}
	netName := map[string]string{}
	for _, n := range nets {
		netName[n.ID] = n.Name
	}
	clients := map[string]int{}
	if active, err := api.ListActiveClients(ctx); err == nil {
		for _, s := range active {
			if s.ESSID != "" {
				clients[s.ESSID]++
			}
		}
	}
	sort.Slice(list, func(i, j int) bool { return strings.ToLower(list[i].Name) < strings.ToLower(list[j].Name) })
	var rows [][]string
	for _, w := range list {
		state := "enabled"
		if !w.Enabled {
			state = "disabled"
		}
		security := "WPA2/WPA3"
		if w.Security == "open" {
			security = "open"
		}
		if *format == "table" {
			rows = append(rows, []string{w.Name, netName[w.NetworkID], bandsText(w.Bands), security, map[bool]string{true: "yes", false: ""}[w.Hidden], state, fmt.Sprint(clients[w.Name])})
		} else {
			rows = append(rows, []string{w.Name, netName[w.NetworkID], strings.Join(w.Bands, " "), w.Security, fmt.Sprint(w.Hidden), fmt.Sprint(w.Enabled), fmt.Sprint(clients[w.Name])})
		}
	}
	header := []string{"name", "network", "bands", "security", "hidden", "state", "clients"}
	if *format != "table" {
		header = []string{"name", "network", "bands", "security", "hidden", "enabled", "clients"}
	}
	return output(c.env.Stdout, *format, header, rows)
}

// readWiFiPassword reads a new Wi-Fi password from a file, from stdin, or
// from a hidden prompt (asked twice). It is never taken as an argument,
// which would leave it in shell history and process lists.
func (c *command) readWiFiPassword(file string, fromStdin bool) (string, error) {
	switch {
	case file != "":
		b, err := os.ReadFile(file)
		if err != nil {
			return "", err
		}
		return strings.TrimRight(string(b), "\r\n"), nil
	case fromStdin:
		line, err := bufio.NewReader(c.env.stdin()).ReadString('\n')
		if err != nil && line == "" {
			return "", fmt.Errorf("no password on standard input")
		}
		return strings.TrimRight(line, "\r\n"), nil
	case c.env.ReadPassword != nil:
		pw, err := c.env.ReadPassword("New Wi-Fi password: ")
		if err != nil {
			return "", err
		}
		again, err := c.env.ReadPassword("Again: ")
		if err != nil {
			return "", err
		}
		if pw != again {
			return "", fmt.Errorf("the passwords didn't match")
		}
		return pw, nil
	}
	return "", usagef("give the password with --password-file FILE or --password-stdin (there is no terminal to ask on)")
}

func wifiCreate(ctx context.Context, c *command) error {
	fs := c.flags("NAME --network NET [--password-file FILE | --password-stdin] [--bands 2g,5g,6g] [--hidden] [--disabled]")
	network := fs.String("network", "", "network the Wi-Fi network's devices join")
	pwFile := fs.String("password-file", "", "read the password from this file")
	pwStdin := fs.Bool("password-stdin", false, "read the password from the first line of standard input")
	bands := fs.String("bands", "2g,5g", "bands to broadcast on: 2g, 5g, 6g")
	hidden := fs.Bool("hidden", false, "don't broadcast the name")
	disabled := fs.Bool("disabled", false, "create it switched off")
	args, err := c.parse(1, 1)
	if err != nil {
		return err
	}
	if *network == "" {
		return usagef("a Wi-Fi network needs --network")
	}
	if err := check.WiFiBands(splitList(*bands)); err != nil {
		return err
	}
	api, err := c.connect()
	if err != nil {
		return err
	}
	lans, err := lanNetworks(ctx, api)
	if err != nil {
		return err
	}
	n, err := pickNetwork(lans, *network)
	if err != nil {
		return err
	}
	existing, err := api.ListWiFi(ctx)
	if err != nil {
		return err
	}
	for _, w := range existing {
		if w.Name == args[0] {
			return fmt.Errorf("a Wi-Fi network named %q already exists", w.Name)
		}
	}
	pw, err := c.readWiFiPassword(*pwFile, *pwStdin)
	if err != nil {
		return err
	}
	spec := unifi.WiFiSpec{Name: args[0], Password: pw, NetworkID: n.ID, Bands: splitList(*bands), Hidden: *hidden, Disabled: *disabled}
	if err := check.NewWiFi(&spec, existing, lans); err != nil {
		return err
	}
	if !c.dryRun {
		if _, err := api.CreateWiFi(ctx, spec); err != nil {
			return err
		}
	}
	extra := ""
	if *hidden {
		extra += ", hidden"
	}
	if *disabled {
		extra += ", disabled"
	}
	c.out("%s Wi-Fi network %q on network %s (%s, WPA2/WPA3%s)", c.verb("created", "create"), spec.Name, n.Name, bandsText(spec.Bands), extra)
	if !*disabled {
		c.out("%s", wifiDropNote)
	}
	return nil
}

func wifiDelete(ctx context.Context, c *command) error {
	c.flags("NAME")
	args, err := c.parse(1, 1)
	if err != nil {
		return err
	}
	api, err := c.connect()
	if err != nil {
		return err
	}
	list, err := api.ListWiFi(ctx)
	if err != nil {
		return err
	}
	w, err := findWiFi(list, args[0])
	if err != nil {
		return err
	}
	if via := connectedVia(ctx, api); via == w.Name {
		return fmt.Errorf("this machine is connected through Wi-Fi network %q; refusing to delete it", w.Name)
	}
	if !c.dryRun {
		if err := api.DeleteWiFi(ctx, w.ID); err != nil {
			return err
		}
	}
	c.out("%s Wi-Fi network %q", c.verb("deleted", "delete"), w.Name)
	c.out("%s", wifiDropNote)
	return nil
}

func wifiEnable(ctx context.Context, c *command) error  { return c.setWiFiEnabled(ctx, true) }
func wifiDisable(ctx context.Context, c *command) error { return c.setWiFiEnabled(ctx, false) }

func (c *command) setWiFiEnabled(ctx context.Context, enable bool) error {
	c.flags("NAME")
	args, err := c.parse(1, 1)
	if err != nil {
		return err
	}
	api, err := c.connect()
	if err != nil {
		return err
	}
	list, err := api.ListWiFi(ctx)
	if err != nil {
		return err
	}
	w, err := findWiFi(list, args[0])
	if err != nil {
		return err
	}
	if w.Enabled == enable {
		c.out("Wi-Fi network %q is already %s", w.Name, map[bool]string{true: "enabled", false: "disabled"}[enable])
		return nil
	}
	if !enable {
		if via := connectedVia(ctx, api); via == w.Name {
			return fmt.Errorf("this machine is connected through Wi-Fi network %q; refusing to disable it", w.Name)
		}
	}
	if !c.dryRun {
		if _, err := api.UpdateWiFi(ctx, w.ID, map[string]any{"enabled": enable}); err != nil {
			return err
		}
	}
	c.out("%s Wi-Fi network %q", c.verb(map[bool]string{true: "enabled", false: "disabled"}[enable], map[bool]string{true: "enable", false: "disable"}[enable]), w.Name)
	c.out("%s", wifiDropNote)
	return nil
}

func wifiPassword(ctx context.Context, c *command) error {
	fs := c.flags("NAME [--password-file FILE | --password-stdin]")
	pwFile := fs.String("password-file", "", "read the password from this file")
	pwStdin := fs.Bool("password-stdin", false, "read the password from the first line of standard input")
	args, err := c.parse(1, 1)
	if err != nil {
		return err
	}
	api, err := c.connect()
	if err != nil {
		return err
	}
	list, err := api.ListWiFi(ctx)
	if err != nil {
		return err
	}
	w, err := findWiFi(list, args[0])
	if err != nil {
		return err
	}
	pw, err := c.readWiFiPassword(*pwFile, *pwStdin)
	if err != nil {
		return err
	}
	if err := check.WiFiPassword(pw); err != nil {
		return err
	}
	if pw == w.Password {
		c.out("Wi-Fi network %q already has that password", w.Name)
		return nil
	}
	if !c.dryRun {
		if _, err := api.UpdateWiFi(ctx, w.ID, map[string]any{"x_passphrase": pw}); err != nil {
			return err
		}
	}
	c.out("%s the password of Wi-Fi network %q; its devices must reconnect with the new one", c.verb("changed", "change"), w.Name)
	c.out("%s", wifiDropNote)
	return nil
}
