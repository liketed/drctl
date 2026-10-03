package cli

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/liketed/dreamrouter-go/check"
	"github.com/liketed/dreamrouter-go/unifi"
)

// describeForward summarises a rule, e.g. "TCP 8443 -> 192.168.1.20:443 from any".
func describeForward(pf unifi.PortForward) string {
	s := fmt.Sprintf("%s %s -> %s:%s", check.ProtocolText(pf.Protocol), pf.Port, pf.ForwardIP, orDefault(pf.ForwardPort, pf.Port))
	if pf.Source != "" && pf.Source != "any" {
		s += " from " + pf.Source
	}
	return s
}

// opensText says what an enabled rule exposes, e.g. "opens TCP 8443 on the
// internet to 192.168.1.20:443".
func opensText(pf unifi.PortForward) string {
	from := "on the internet"
	if pf.Source != "" && pf.Source != "any" {
		from = "to " + pf.Source
	}
	return fmt.Sprintf("opens %s %s %s, forwarded to %s:%s", check.ProtocolText(pf.Protocol), pf.Port, from, pf.ForwardIP, orDefault(pf.ForwardPort, pf.Port))
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

// findForward finds a rule by name (case-insensitive).
func findForward(list []unifi.PortForward, name string) (unifi.PortForward, error) {
	for _, pf := range list {
		if strings.EqualFold(pf.Name, name) {
			return pf, nil
		}
	}
	var names []string
	for _, pf := range list {
		names = append(names, pf.Name)
	}
	if len(names) == 0 {
		return unifi.PortForward{}, fmt.Errorf("no port forward named %q (there are none)", name)
	}
	return unifi.PortForward{}, fmt.Errorf("no port forward named %q (port forwards: %s)", name, strings.Join(names, ", "))
}

func portforwardList(ctx context.Context, c *command) error {
	fs := c.flags("[--format table|csv|json]")
	format := fs.String("format", "table", "output format: table, csv or json")
	if _, err := c.parse(0, 0); err != nil {
		return err
	}
	api, err := c.connect()
	if err != nil {
		return err
	}
	list, err := api.ListPortForwards(ctx)
	if err != nil {
		return err
	}
	sort.SliceStable(list, func(i, j int) bool { return strings.ToLower(list[i].Name) < strings.ToLower(list[j].Name) })
	var rows [][]string
	enabled := 0
	for _, pf := range list {
		state := "disabled"
		if pf.Enabled {
			state = "enabled"
			enabled++
		}
		if *format == "table" {
			rows = append(rows, []string{pf.Name, check.ProtocolText(pf.Protocol), pf.Port,
				pf.ForwardIP + ":" + orDefault(pf.ForwardPort, pf.Port), orDefault(pf.Source, "any"), state})
		} else {
			rows = append(rows, []string{pf.Name, fmt.Sprint(pf.Enabled), pf.Protocol, pf.Port, pf.ForwardIP,
				orDefault(pf.ForwardPort, pf.Port), orDefault(pf.Source, "any"), pf.Interface, fmt.Sprint(pf.Log)})
		}
	}
	if *format == "table" {
		if err := output(c.env.Stdout, "table", []string{"name", "protocol", "port", "forward to", "from", "state"}, rows); err != nil {
			return err
		}
		fmt.Fprintf(c.env.Stderr, "%d port forwards (%d enabled)\n", len(list), enabled)
		return nil
	}
	return output(c.env.Stdout, *format, []string{"name", "enabled", "protocol", "port", "forward_ip", "forward_port", "source", "wan", "log"}, rows)
}

func portforwardAdd(ctx context.Context, c *command) error {
	fs := c.flags("NAME PORT IP[:PORT] [--proto tcp|udp|both] [--from CIDR] [--wan wan|wan2|both] [--disabled] [--log]")
	proto := fs.String("proto", "both", "protocol: tcp, udp or both")
	from := fs.String("from", "any", `who may connect: "any", or an IPv4 address or network`)
	wan := fs.String("wan", "wan", "internet connection: wan, wan2 or both")
	disabled := fs.Bool("disabled", false, "create the rule switched off (nothing is opened)")
	logHits := fs.Bool("log", false, "log connections that use the rule")
	args, err := c.parse(3, 3)
	if err != nil {
		return err
	}
	target, fwdPort := args[2], ""
	if i := strings.LastIndex(target, ":"); i >= 0 {
		target, fwdPort = target[:i], target[i+1:]
	}
	pf := unifi.PortForward{Name: args[0], Enabled: !*disabled, Interface: *wan, Source: *from, Port: args[1],
		ForwardIP: target, ForwardPort: fwdPort, Protocol: *proto, Log: *logHits}
	api, err := c.connect()
	if err != nil {
		return err
	}
	networks, err := api.ListNetworks(ctx)
	if err != nil {
		return err
	}
	existing, err := api.ListPortForwards(ctx)
	if err != nil {
		return err
	}
	if err := check.PortForward(&pf, networks, existing); err != nil {
		return err
	}
	if !c.dryRun {
		if pf, err = api.CreatePortForward(ctx, pf); err != nil {
			return err
		}
	}
	if pf.Enabled {
		c.out("%s port forward %q: %s", c.verb("created", "create"), pf.Name, opensText(pf))
	} else {
		c.out("%s port forward %q (disabled): %s; nothing is open until \"drctl portforward enable %s\"",
			c.verb("created", "create"), pf.Name, describeForward(pf), pf.Name)
	}
	return nil
}

func portforwardDelete(ctx context.Context, c *command) error {
	c.flags("NAME")
	args, err := c.parse(1, 1)
	if err != nil {
		return err
	}
	api, err := c.connect()
	if err != nil {
		return err
	}
	list, err := api.ListPortForwards(ctx)
	if err != nil {
		return err
	}
	pf, err := findForward(list, args[0])
	if err != nil {
		return err
	}
	if !c.dryRun {
		if err := api.DeletePortForward(ctx, pf.ID); err != nil {
			return err
		}
	}
	c.out("%s port forward %q (%s)", c.verb("deleted", "delete"), pf.Name, describeForward(pf))
	return nil
}

func portforwardEnable(ctx context.Context, c *command) error { return c.setForwardEnabled(ctx, true) }
func portforwardDisable(ctx context.Context, c *command) error {
	return c.setForwardEnabled(ctx, false)
}

func (c *command) setForwardEnabled(ctx context.Context, enable bool) error {
	c.flags("NAME")
	args, err := c.parse(1, 1)
	if err != nil {
		return err
	}
	api, err := c.connect()
	if err != nil {
		return err
	}
	list, err := api.ListPortForwards(ctx)
	if err != nil {
		return err
	}
	pf, err := findForward(list, args[0])
	if err != nil {
		return err
	}
	if pf.Enabled == enable {
		c.out("port forward %q is already %s", pf.Name, map[bool]string{true: "enabled", false: "disabled"}[enable])
		return nil
	}
	pf.Enabled = enable
	if enable {
		// Enabling may clash with another enabled rule for the same port.
		networks, err := api.ListNetworks(ctx)
		if err != nil {
			return err
		}
		if err := check.PortForward(&pf, networks, list); err != nil {
			return err
		}
	}
	if !c.dryRun {
		if pf, err = api.UpdatePortForward(ctx, pf.ID, pf); err != nil {
			return err
		}
	}
	if enable {
		c.out("%s port forward %q: %s", c.verb("enabled", "enable"), pf.Name, opensText(pf))
	} else {
		c.out("%s port forward %q (%s); nothing is open now", c.verb("disabled", "disable"), pf.Name, describeForward(pf))
	}
	return nil
}
