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

// lanNetworks returns the networks with a subnet (not WAN connections), sorted by name.
func lanNetworks(ctx context.Context, api *unifi.Client) ([]unifi.Network, error) {
	all, err := api.ListNetworks(ctx)
	if err != nil {
		return nil, err
	}
	var lans []unifi.Network
	for _, n := range all {
		if n.Subnet != "" {
			lans = append(lans, n)
		}
	}
	sort.Slice(lans, func(i, j int) bool { return lans[i].Name < lans[j].Name })
	return lans, nil
}

// pickNetwork finds a network by name (case-insensitive) or ID; with no name,
// the only network, if there is exactly one.
func pickNetwork(lans []unifi.Network, name string) (unifi.Network, error) {
	if name == "" {
		if len(lans) == 1 {
			return lans[0], nil
		}
		return unifi.Network{}, fmt.Errorf("the router has several networks; name one (networks: %s)", check.NetworkNames(lans))
	}
	for _, n := range lans {
		if strings.EqualFold(n.Name, name) || n.ID == name {
			return n, nil
		}
	}
	return unifi.Network{}, fmt.Errorf("no network named %q (networks: %s)", name, check.NetworkNames(lans))
}

// subnet shows a network's subnet in network form, e.g. 192.168.1.0/24.
func subnet(n unifi.Network) string {
	if p, err := netip.ParsePrefix(n.Subnet); err == nil {
		return p.Masked().String()
	}
	return n.Subnet
}

func onOff(b bool) string {
	if b {
		return "on"
	}
	return "off"
}

func orNotSet(s string) string {
	if s == "" {
		return "(not set)"
	}
	return s
}

// bootSummary describes a network's boot settings on one line.
func bootSummary(n unifi.Network) string {
	if !n.BootEnabled {
		return "off"
	}
	return fmt.Sprintf("on, server %s, file %s", n.BootServer, n.BootFilename)
}

func networkList(ctx context.Context, c *command) error {
	fs := c.flags("[--format table|csv|json]")
	format := fs.String("format", "table", "output format: table, csv or json")
	if _, err := c.parse(0, 0); err != nil {
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
	var rows [][]string
	for _, n := range lans {
		pool := ""
		if n.DHCPEnabled {
			pool = n.DHCPStart + " - " + n.DHCPStop
		}
		rows = append(rows, []string{n.Name, subnet(n), onOff(n.DHCPEnabled), pool, n.DomainName, onOff(n.BootEnabled)})
	}
	return output(c.env.Stdout, *format, []string{"name", "subnet", "dhcp", "pool", "domain", "network boot"}, rows)
}

func networkShow(ctx context.Context, c *command) error {
	fs := c.flags("[NETWORK] [--format table|csv|json]")
	format := fs.String("format", "table", "output format: table, csv or json")
	args, err := c.parse(0, 1)
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
	name := ""
	if len(args) == 1 {
		name = args[0]
	}
	n, err := pickNetwork(lans, name)
	if err != nil {
		return err
	}
	if *format != "table" {
		header := []string{"name", "subnet", "dhcp", "dhcp_start", "dhcp_stop", "domain",
			"boot_enabled", "boot_server", "boot_file", "tftp_server"}
		row := []string{n.Name, subnet(n), onOff(n.DHCPEnabled), n.DHCPStart, n.DHCPStop, n.DomainName,
			onOff(n.BootEnabled), n.BootServer, n.BootFilename, n.TFTPServer}
		return output(c.env.Stdout, *format, header, [][]string{row})
	}
	dhcp := "off"
	if n.DHCPEnabled {
		dhcp = fmt.Sprintf("on, pool %s - %s", n.DHCPStart, n.DHCPStop)
	}
	boot := bootSummary(n)
	if !n.BootEnabled && (n.BootServer != "" || n.BootFilename != "") {
		boot = fmt.Sprintf("off (stored: server %s, file %s)", orNotSet(n.BootServer), orNotSet(n.BootFilename))
	}
	w := c.env.Stdout
	fmt.Fprintf(w, "Network %s (%s)\n", n.Name, subnet(n))
	fmt.Fprintf(w, "  %-16s %s\n", "DHCP", dhcp)
	fmt.Fprintf(w, "  %-16s %s\n", "Domain", orNotSet(n.DomainName))
	fmt.Fprintf(w, "  %-16s %s\n", "Network boot", boot)
	fmt.Fprintf(w, "  %-16s %s\n", "TFTP server", orNotSet(n.TFTPServer))
	return nil
}

// networkBoot turns network boot (PXE) on or off for a network, and sets or
// clears its TFTP server (DHCP option 66, which the router hands out whenever
// it is set, even with network boot off).
func networkBoot(ctx context.Context, c *command) error {
	usage := "[NETWORK] --server IP --file NAME [--tftp-server HOST | --no-tftp]\n" +
		"       drctl network boot [NETWORK] --off [--no-tftp]\n" +
		"       drctl network boot [NETWORK] --tftp-server HOST | --no-tftp"
	fs := c.flags(usage)
	server := fs.String("server", "", "IPv4 address of the boot (TFTP/HTTP) server")
	file := fs.String("file", "", `file the machine should load, e.g. "netboot.xyz.efi"`)
	tftp := fs.String("tftp-server", "", "TFTP server host name or IP (DHCP option 66)")
	noTFTP := fs.Bool("no-tftp", false, "stop handing out a TFTP server")
	off := fs.Bool("off", false, "turn network boot off (the server and file stay stored, as in the web UI)")
	args, err := c.parse(0, 1)
	if err != nil {
		return err
	}
	setBoot := *server != "" || *file != ""
	switch {
	case *off && setBoot:
		return usagef("--off can't be combined with --server or --file")
	case *tftp != "" && *noTFTP:
		return usagef("--tftp-server and --no-tftp can't be combined")
	case setBoot && (*server == "" || *file == ""):
		return usagef("turning network boot on needs both --server and --file")
	case !setBoot && !*off && *tftp == "" && !*noTFTP:
		return usagef("nothing to change; usage: drctl network boot %s", usage)
	}
	if setBoot {
		if err := check.Boot(*server, *file); err != nil {
			return err
		}
	}
	if *tftp != "" {
		if err := check.TFTPServer(*tftp); err != nil {
			return err
		}
	}

	api, err := c.connect()
	if err != nil {
		return err
	}
	lans, err := lanNetworks(ctx, api)
	if err != nil {
		return err
	}
	name := ""
	if len(args) == 1 {
		name = args[0]
	}
	n, err := pickNetwork(lans, name)
	if err != nil {
		return err
	}

	// Work out the changes, field by field.
	type change struct{ label, from, to string }
	var changes []change
	fields := map[string]any{}
	want := n
	if setBoot {
		want.BootEnabled, want.BootServer, want.BootFilename = true, *server, *file
		if p, err := netip.ParsePrefix(n.Subnet); err == nil {
			if a, _ := netip.ParseAddr(*server); !p.Masked().Contains(a) {
				c.warn("boot server %s is outside %s's subnet (%s); it must be reachable from there", *server, n.Name, subnet(n))
			}
		}
	}
	if *off {
		want.BootEnabled = false
	}
	if *tftp != "" {
		want.TFTPServer = *tftp
	}
	if *noTFTP {
		want.TFTPServer = ""
	}
	if want.BootEnabled != n.BootEnabled {
		changes = append(changes, change{"network boot", onOff(n.BootEnabled), onOff(want.BootEnabled)})
		fields["dhcpd_boot_enabled"] = want.BootEnabled
	}
	if want.BootServer != n.BootServer {
		changes = append(changes, change{"boot server", dash(n.BootServer), want.BootServer})
		fields["dhcpd_boot_server"] = want.BootServer
	}
	if want.BootFilename != n.BootFilename {
		changes = append(changes, change{"boot file", dash(n.BootFilename), want.BootFilename})
		fields["dhcpd_boot_filename"] = want.BootFilename
	}
	if want.TFTPServer != n.TFTPServer {
		changes = append(changes, change{"TFTP server", dash(n.TFTPServer), dash(want.TFTPServer)})
		fields["dhcpd_tftp_server"] = want.TFTPServer
	}

	summary := bootSummary(want)
	if !want.BootEnabled && (want.BootServer != "" || want.BootFilename != "") && *off {
		summary = fmt.Sprintf("off (server %s and file %s kept, not active)", orNotSet(want.BootServer), orNotSet(want.BootFilename))
	}
	if want.TFTPServer != "" {
		summary += ", TFTP server " + want.TFTPServer
	}
	if len(changes) == 0 {
		c.out("unchanged network boot on %s: %s", n.Name, summary)
		return nil
	}
	if c.dryRun {
		c.out("would update network boot on %s:", n.Name)
		for _, ch := range changes {
			c.out("  %-13s %s -> %s", ch.label, ch.from, ch.to)
		}
		return nil
	}
	if _, err := api.UpdateNetwork(ctx, n.ID, fields); err != nil {
		return err
	}
	c.out("updated network boot on %s: %s", n.Name, summary)
	if !want.BootEnabled && want.TFTPServer != "" {
		c.out("Note: TFTP server %s is still handed out (DHCP option 66); use --no-tftp to stop that.", want.TFTPServer)
	}
	c.out("Devices get the new settings the next time they ask for an address (a network-booting machine asks when it starts).")
	return nil
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
