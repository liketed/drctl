package cli

import (
	"context"
	"encoding/json"
	"fmt"
)

func sshShow(ctx context.Context, c *command) error {
	fs := c.flags("[--format text|json]")
	format := fs.String("format", "text", "output format: text or json")
	if _, err := c.parse(0, 0); err != nil {
		return err
	}
	if *format != "text" && *format != "json" {
		return usagef("unknown format %q; use text or json", *format)
	}
	api, err := c.connect()
	if err != nil {
		return err
	}
	s, err := api.GetSSH(ctx)
	if err != nil {
		return err
	}
	if *format == "json" {
		enc := json.NewEncoder(c.env.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(map[string]any{"router": s.Router, "devices": s.Devices,
			"devices_username": s.DevicesUsername, "devices_password_auth": s.DevicesPasswordAuth})
	}
	c.out("router:  %s   (SSH to the router itself)", onOff(s.Router))
	devices := onOff(s.Devices)
	if s.Devices {
		devices += fmt.Sprintf(", username %q", s.DevicesUsername)
		if !s.DevicesPasswordAuth {
			devices += ", SSH keys only"
		}
	}
	c.out("devices: %s   (SSH to adopted devices such as access points)", devices)
	return nil
}

func sshSet(ctx context.Context, c *command, which string) error {
	c.flags("on|off")
	args, err := c.parse(1, 1)
	if err != nil {
		return err
	}
	var enable bool
	switch args[0] {
	case "on":
		enable = true
	case "off":
	default:
		return usagef("%q must be on or off", args[0])
	}
	api, err := c.connect()
	if err != nil {
		return err
	}
	s, err := api.GetSSH(ctx)
	if err != nil {
		return err
	}
	cur, what := s.Router, "SSH to the router"
	if which == "devices" {
		cur, what = s.Devices, "SSH to adopted devices"
	}
	if cur == enable {
		c.out("%s is already %s", what, onOff(enable))
		return nil
	}
	if !c.dryRun {
		if which == "router" {
			err = api.SetRouterSSH(ctx, enable)
		} else {
			err = api.SetDevicesSSH(ctx, enable)
		}
		if err != nil {
			return err
		}
	}
	verb := c.verb("turned", "turn")
	switch {
	case which == "router" && enable:
		c.out("%s on %s (port 22, with the root password set before)", verb, what)
	case which == "router":
		c.out("%s off %s; new SSH logins to the router are refused", verb, what)
	default:
		c.out("%s %s %s", verb, onOff(enable), what)
	}
	return nil
}

func sshRouter(ctx context.Context, c *command) error  { return sshSet(ctx, c, "router") }
func sshDevices(ctx context.Context, c *command) error { return sshSet(ctx, c, "devices") }
