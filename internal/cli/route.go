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

// findRoute finds a static route by network (e.g. 192.168.2.0/24) or name.
func findRoute(routes []unifi.StaticRoute, key string) (unifi.StaticRoute, error) {
	p, perr := netip.ParsePrefix(key)
	var names []string
	for _, r := range routes {
		if strings.EqualFold(r.Name, key) || (perr == nil && r.Network == p.Masked().String()) {
			return r, nil
		}
		names = append(names, r.Network)
	}
	if len(names) == 0 {
		return unifi.StaticRoute{}, fmt.Errorf("no static route %q (there are none)", key)
	}
	return unifi.StaticRoute{}, fmt.Errorf("no static route %q (routes: %s)", key, strings.Join(names, ", "))
}

func routeList(ctx context.Context, c *command) error {
	fs := c.flags("[--format table|csv|json]")
	format := fs.String("format", "table", "output format: table, csv or json")
	if _, err := c.parse(0, 0); err != nil {
		return err
	}
	api, err := c.connect()
	if err != nil {
		return err
	}
	routes, err := api.ListRoutes(ctx)
	if err != nil {
		return err
	}
	sort.Slice(routes, func(i, j int) bool { return routes[i].Network < routes[j].Network })
	var rows [][]string
	for _, r := range routes {
		state := "enabled"
		if !r.Enabled {
			state = "disabled"
		}
		rows = append(rows, []string{r.Network, r.NextHop, fmt.Sprint(r.Distance), state, r.Name})
	}
	return output(c.env.Stdout, *format, []string{"network", "next hop", "distance", "state", "name"}, rows)
}

func routeAdd(ctx context.Context, c *command) error {
	fs := c.flags("NETWORK NEXT-HOP [--name NAME] [--distance N] [--disabled]")
	name := fs.String("name", "", `name shown in the web UI (default "NETWORK via NEXT-HOP")`)
	distance := fs.Int("distance", 1, "administrative distance, 1-255 (lower wins)")
	disabled := fs.Bool("disabled", false, "create the route switched off")
	args, err := c.parse(2, 2)
	if err != nil {
		return err
	}
	api, err := c.connect()
	if err != nil {
		return err
	}
	networks, err := api.ListNetworks(ctx)
	if err != nil {
		return err
	}
	routes, err := api.ListRoutes(ctx)
	if err != nil {
		return err
	}
	r := unifi.StaticRoute{Name: *name, Network: args[0], NextHop: args[1], Distance: *distance, Enabled: !*disabled}
	if r.Name == "" {
		r.Name = args[0] + " via " + args[1]
		if p, err := netip.ParsePrefix(args[0]); err == nil {
			r.Name = p.Masked().String() + " via " + args[1]
		}
	}
	if err := check.StaticRoute(&r, networks, routes); err != nil {
		return err
	}
	if !c.dryRun {
		if r, err = api.CreateRoute(ctx, r); err != nil {
			return err
		}
	}
	state := ""
	if !r.Enabled {
		state = " (disabled)"
	}
	c.out("%s static route %s via %s%s", c.verb("added", "add"), r.Network, r.NextHop, state)
	return nil
}

func routeDelete(ctx context.Context, c *command) error {
	c.flags("NETWORK|NAME")
	args, err := c.parse(1, 1)
	if err != nil {
		return err
	}
	api, err := c.connect()
	if err != nil {
		return err
	}
	routes, err := api.ListRoutes(ctx)
	if err != nil {
		return err
	}
	r, err := findRoute(routes, args[0])
	if err != nil {
		return err
	}
	if !c.dryRun {
		if err := api.DeleteRoute(ctx, r.ID); err != nil {
			return err
		}
	}
	c.out("%s static route %s via %s", c.verb("deleted", "delete"), r.Network, r.NextHop)
	return nil
}

func routeEnable(ctx context.Context, c *command) error  { return c.setRouteEnabled(ctx, true) }
func routeDisable(ctx context.Context, c *command) error { return c.setRouteEnabled(ctx, false) }

func (c *command) setRouteEnabled(ctx context.Context, enable bool) error {
	c.flags("NETWORK|NAME")
	args, err := c.parse(1, 1)
	if err != nil {
		return err
	}
	api, err := c.connect()
	if err != nil {
		return err
	}
	routes, err := api.ListRoutes(ctx)
	if err != nil {
		return err
	}
	r, err := findRoute(routes, args[0])
	if err != nil {
		return err
	}
	word := map[bool][2]string{true: {"enabled", "enable"}, false: {"disabled", "disable"}}[enable]
	if r.Enabled == enable {
		c.out("static route %s is already %s", r.Network, word[0])
		return nil
	}
	if !c.dryRun {
		if _, err := api.UpdateRoute(ctx, r.ID, map[string]any{"enabled": enable}); err != nil {
			return err
		}
	}
	c.out("%s static route %s via %s", c.verb(word[0], word[1]), r.Network, r.NextHop)
	return nil
}
