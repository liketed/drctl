package cli

import (
	"context"
	"fmt"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"github.com/liketed/dreamrouter-go/check"
	"github.com/liketed/dreamrouter-go/unifi"
)

// wanWait is how long drctl lets the router apply a change before
// watching (right after a change the router still reports the old state),
// how often it then looks, and how long it watches a connection before
// putting the previous settings back. Replaced in tests.
var wanWait = struct{ settle, interval, timeout time.Duration }{20 * time.Second, 5 * time.Second, 2 * time.Minute}

type wanState struct {
	wans  []unifi.WAN
	ports []unifi.Port
	links []unifi.WANLink
}

func loadWANState(ctx context.Context, api *unifi.Client) (*wanState, error) {
	wans, err := api.ListWANs(ctx)
	if err != nil {
		return nil, err
	}
	ports, links, err := api.Ports(ctx)
	if err != nil {
		return nil, err
	}
	return &wanState{wans, ports, links}, nil
}

func (s *wanState) link(group string) unifi.WANLink {
	for _, l := range s.links {
		if l.NetworkGroup == group {
			return l
		}
	}
	return unifi.WANLink{NetworkGroup: group}
}

func (s *wanState) portFor(iface string) unifi.Port {
	for _, p := range s.ports {
		if p.Interface == iface {
			return p
		}
	}
	return unifi.Port{Interface: iface, Name: iface}
}

// portOf is the port a connection uses, from the port roles.
func (s *wanState) portOf(group string) unifi.Port {
	for _, p := range s.ports {
		if p.Role == group {
			return p
		}
	}
	return unifi.Port{}
}

func (s *wanState) groupName(group string) string {
	for _, w := range s.wans {
		if w.NetworkGroup == group {
			return w.Name
		}
	}
	return group
}

func findWAN(wans []unifi.WAN, name string) (unifi.WAN, error) {
	var names []string
	for _, w := range wans {
		if strings.EqualFold(w.Name, name) || strings.EqualFold(w.NetworkGroup, name) {
			return w, nil
		}
		names = append(names, w.Name)
	}
	return unifi.WAN{}, fmt.Errorf("no internet connection named %q (connections: %s)", name, strings.Join(names, ", "))
}

func roleText(w unifi.WAN) string {
	switch {
	case w.IsPrimary() || (w.FailoverPriority == 0 && w.NetworkGroup == "WAN"):
		return "primary"
	case w.LoadBalanceType == "failover-only":
		return "failover"
	}
	return "load balanced"
}

func loginText(w unifi.WAN) string {
	switch w.Type {
	case "pppoe":
		return w.Username
	case "static":
		return w.IP
	}
	return ""
}

func vlanTextWAN(w unifi.WAN) string {
	if w.VLANEnabled {
		return fmt.Sprint(int(w.VLAN))
	}
	return ""
}

func wanList(ctx context.Context, c *command) error {
	fs := c.flags("[--format table|csv|json]")
	format := fs.String("format", "table", "output format: table, csv or json")
	if _, err := c.parse(0, 0); err != nil {
		return err
	}
	api, err := c.connect()
	if err != nil {
		return err
	}
	s, err := loadWANState(ctx, api)
	if err != nil {
		return err
	}
	var rows [][]string
	for _, w := range s.wans {
		l, p := s.link(w.NetworkGroup), s.portOf(w.NetworkGroup)
		state := "down"
		if l.Up {
			state = "up"
		}
		port := p.Name
		if port == "" {
			port = "-"
		}
		rows = append(rows, []string{w.Name, port, strings.ToUpper(w.Type), loginText(w), vlanTextWAN(w), state, l.IP, roleText(w)})
	}
	return output(c.env.Stdout, *format, []string{"name", "port", "type", "login", "vlan", "state", "ip", "role"}, rows)
}

func wanShow(ctx context.Context, c *command) error {
	c.flags("NAME")
	args, err := c.parse(1, 1)
	if err != nil {
		return err
	}
	api, err := c.connect()
	if err != nil {
		return err
	}
	s, err := loadWANState(ctx, api)
	if err != nil {
		return err
	}
	w, err := findWAN(s.wans, args[0])
	if err != nil {
		return err
	}
	l, p := s.link(w.NetworkGroup), s.portOf(w.NetworkGroup)
	state := "down"
	if l.Up {
		state = "up, " + l.IP
	} else if !p.Up && p.Name != "" {
		state = "down (no cable on " + p.Name + ")"
	}
	lines := [][2]string{{"connection", w.Name + " (" + roleText(w) + ")"}, {"port", strings.TrimSpace(p.Name + " (" + p.Interface + ")")},
		{"state", state}, {"type", strings.ToUpper(w.Type)}}
	switch w.Type {
	case "pppoe":
		pw := "not set"
		if w.Password != "" {
			pw = "set"
		}
		lines = append(lines, [2]string{"username", w.Username}, [2]string{"password", pw})
	case "static":
		lines = append(lines, [2]string{"address", w.IP + " / " + w.Netmask}, [2]string{"gateway", w.Gateway})
	}
	vlan := "off"
	if w.VLANEnabled {
		vlan = fmt.Sprint(int(w.VLAN))
	}
	dns := "from the provider"
	if w.DNSPreference == "manual" {
		dns = strings.Join(nonEmptyStrings(w.DNS1, w.DNS2), ", ")
	}
	lines = append(lines, [2]string{"vlan", vlan}, [2]string{"dns", dns})
	for _, l := range lines {
		fmt.Fprintf(c.env.Stdout, "%-12s %s\n", l[0]+":", l[1])
	}
	return nil
}

func nonEmptyStrings(values ...string) []string {
	var out []string
	for _, v := range values {
		if v != "" {
			out = append(out, v)
		}
	}
	return out
}

// confirm asks for the connection's name before changing a connection in use.
func (c *command) confirm(what, name string) error {
	fmt.Fprintf(c.env.Stdout, "%s\nType the connection's name (%s) to go ahead: ", what, name)
	line, _ := c.env.readLine()
	if strings.TrimSpace(line) != name {
		return fmt.Errorf("nothing changed: the name didn't match")
	}
	return nil
}

// waitUp lets the router apply a change, then waits until the connection
// for group is up, returning its link.
func waitUp(ctx context.Context, api *unifi.Client, group string, timeout time.Duration) (unifi.WANLink, bool) {
	deadline := time.Now().Add(timeout)
	select {
	case <-ctx.Done():
		return unifi.WANLink{}, false
	case <-time.After(wanWait.settle):
	}
	for {
		if _, links, err := api.Ports(ctx); err == nil {
			for _, l := range links {
				if l.NetworkGroup == group && l.Up && l.IP != "" {
					return l, true
				}
			}
		}
		if time.Now().After(deadline) {
			return unifi.WANLink{}, false
		}
		select {
		case <-ctx.Done():
			return unifi.WANLink{}, false
		case <-time.After(wanWait.interval):
		}
	}
}

func wanSet(ctx context.Context, c *command) error {
	usage := "NAME [--pppoe --username U [--password-file F | --password-stdin] | --dhcp | --static --ip ADDR/PREFIX --gateway IP] [--vlan N | --no-vlan] [--dns IP[,IP] | --dns auto] [--yes]"
	fs := c.flags(usage)
	pppoe := fs.Bool("pppoe", false, "connect with PPPoE")
	dhcp := fs.Bool("dhcp", false, "get the address by DHCP")
	static := fs.Bool("static", false, "use a fixed address (--ip, --gateway)")
	username := fs.String("username", "", "PPPoE username")
	pwFile := fs.String("password-file", "", "read the PPPoE password from this file")
	pwStdin := fs.Bool("password-stdin", false, "read the PPPoE password from the first line of standard input")
	vlan := fs.Int("vlan", 0, "VLAN ID the provider uses, 1-4094")
	noVLAN := fs.Bool("no-vlan", false, "no VLAN")
	dns := fs.String("dns", "", `DNS servers for the router itself (up to 2), or "auto" for the provider's`)
	ipArg := fs.String("ip", "", "static address with prefix, e.g. 203.0.113.10/24")
	gateway := fs.String("gateway", "", "static gateway")
	yes := fs.Bool("yes", false, "don't ask before changing the connection in use")
	args, err := c.parse(1, 1)
	if err != nil {
		return err
	}
	if countTrue(*pppoe, *dhcp, *static) > 1 {
		return usagef("use only one of --pppoe, --dhcp and --static")
	}
	if *vlan != 0 && *noVLAN {
		return usagef("use only one of --vlan and --no-vlan")
	}
	api, err := c.connect()
	if err != nil {
		return err
	}
	s, err := loadWANState(ctx, api)
	if err != nil {
		return err
	}
	old, err := findWAN(s.wans, args[0])
	if err != nil {
		return err
	}
	w := old
	switch {
	case *pppoe:
		w.Type = "pppoe"
	case *dhcp:
		w.Type = "dhcp"
	case *static:
		w.Type = "static"
	}
	if *username != "" {
		w.Username = *username
	}
	if w.Type == "pppoe" && (*pwFile != "" || *pwStdin || old.Type != "pppoe" || *username != "" && old.Password == "") {
		if w.Password, err = c.readNewSecret("PPPoE password", *pwFile, *pwStdin); err != nil {
			return err
		}
	}
	if w.Type == "static" && *ipArg != "" {
		p, err := netip.ParsePrefix(*ipArg)
		if err != nil || !p.Addr().Is4() {
			return usagef("--ip %q must be an address with a prefix, e.g. 203.0.113.10/24", *ipArg)
		}
		m, _ := netip.AddrFrom4([4]byte{255, 255, 255, 255}).Prefix(p.Bits())
		w.IP, w.Netmask = p.Addr().String(), netmaskOf(m.Bits())
	}
	if *gateway != "" {
		w.Gateway = *gateway
	}
	switch {
	case *vlan != 0:
		w.VLANEnabled, w.VLAN = true, unifi.VLANID(*vlan)
	case *noVLAN:
		w.VLANEnabled = false
	}
	switch {
	case *dns == "auto":
		w.DNSPreference, w.DNS1, w.DNS2 = "auto", "", ""
	case *dns != "":
		list := splitList(*dns)
		w.DNSPreference, w.DNS1, w.DNS2 = "manual", at(list, 0), at(list, 1)
		if len(list) > 2 {
			return fmt.Errorf("at most 2 DNS servers, not %d", len(list))
		}
	}
	if err := check.WAN(&w); err != nil {
		return err
	}
	fields, changes := wanChanges(old, w)
	if len(fields) == 0 {
		c.out("nothing to change on %s", w.Name)
		return nil
	}
	link := s.link(w.NetworkGroup)
	if c.dryRun {
		for _, ch := range changes {
			c.out("would change %s on %s", ch, w.Name)
		}
		return nil
	}
	if link.Up && w.IsPrimary() && !*yes {
		if err := c.confirm(fmt.Sprintf("%s is your primary internet connection and is up; it will reconnect, and if it doesn't come back\nwithin %s drctl puts the previous settings back.", w.Name, wanWait.timeout), w.Name); err != nil {
			return err
		}
	}
	if _, err := api.UpdateWAN(ctx, w.ID, fields); err != nil {
		return err
	}
	for _, ch := range changes {
		c.out("changed %s on %s", ch, w.Name)
	}
	if !link.Up {
		if l, up := waitUp(ctx, api, w.NetworkGroup, wanWait.timeout); up {
			c.out("%s is up: %s", w.Name, l.IP)
		} else {
			c.out("%s is not up (it wasn't before either; is a cable connected to %s?)", w.Name, s.portOf(w.NetworkGroup).Name)
		}
		return nil
	}
	start := time.Now()
	if l, up := waitUp(ctx, api, w.NetworkGroup, wanWait.timeout); up {
		c.out("%s is back up after %s: %s", w.Name, time.Since(start).Round(time.Second), l.IP)
		return nil
	}
	restore, _ := wanChanges(w, old)
	if _, err := api.UpdateWAN(ctx, w.ID, restore); err != nil {
		return fmt.Errorf("%s didn't come back within %s, and putting the previous settings back failed: %w", w.Name, wanWait.timeout, err)
	}
	return fmt.Errorf("%s didn't come back within %s with the new settings; put the previous settings back", w.Name, wanWait.timeout)
}

func netmaskOf(bits int) string {
	var m [4]byte
	for i := 0; i < bits; i++ {
		m[i/8] |= 1 << (7 - uint(i%8))
	}
	return netip.AddrFrom4(m).String()
}

// wanChanges returns the fields that turn from into to, and a description
// of each change (passwords are never shown).
func wanChanges(from, to unifi.WAN) (map[string]any, []string) {
	f := map[string]any{}
	var ch []string
	add := func(desc string, kv ...any) {
		for i := 0; i < len(kv); i += 2 {
			f[kv[i].(string)] = kv[i+1]
		}
		ch = append(ch, desc)
	}
	if from.Type != to.Type {
		add(fmt.Sprintf("type from %s to %s", strings.ToUpper(from.Type), strings.ToUpper(to.Type)), "wan_type", to.Type)
	}
	if to.Type == "pppoe" && from.Username != to.Username {
		add(fmt.Sprintf("username from %q to %q", from.Username, to.Username), "wan_username", to.Username)
	}
	if to.Type == "pppoe" && from.Password != to.Password {
		add("password", "x_wan_password", to.Password)
	}
	if to.Type == "static" && (from.IP != to.IP || from.Netmask != to.Netmask || from.Gateway != to.Gateway) {
		add(fmt.Sprintf("address to %s / %s, gateway %s", to.IP, to.Netmask, to.Gateway), "wan_ip", to.IP, "wan_netmask", to.Netmask, "wan_gateway", to.Gateway)
	}
	if from.VLANEnabled != to.VLANEnabled || (to.VLANEnabled && from.VLAN != to.VLAN) {
		desc := fmt.Sprintf("VLAN from %s to %s", orDash(vlanTextWAN(from)), orDash(vlanTextWAN(to)))
		if to.VLANEnabled {
			add(desc, "wan_vlan_enabled", true, "wan_vlan", int(to.VLAN))
		} else {
			add(desc, "wan_vlan_enabled", false)
		}
	}
	if from.DNSPreference != to.DNSPreference || from.DNS1 != to.DNS1 || from.DNS2 != to.DNS2 {
		desc := func(w unifi.WAN) string {
			if w.DNSPreference == "manual" {
				return strings.Join(nonEmptyStrings(w.DNS1, w.DNS2), ", ")
			}
			return "the provider's"
		}
		add(fmt.Sprintf("DNS from %s to %s", desc(from), desc(to)), "wan_dns_preference", orDefault(to.DNSPreference, "auto"), "wan_dns1", to.DNS1, "wan_dns2", to.DNS2)
	}
	return f, ch
}

// findPort finds a port by name ("Port 3", "SFP+ 1"), number ("3") or
// interface ("eth2").
func findPort(ports []unifi.Port, key string) (unifi.Port, error) {
	n, numErr := strconv.Atoi(key)
	var names []string
	for _, p := range ports {
		if strings.EqualFold(p.Name, key) || p.Interface == key || (numErr == nil && p.Index == n) {
			return p, nil
		}
		names = append(names, p.Name)
	}
	return unifi.Port{}, fmt.Errorf("no port %q (ports: %s)", key, strings.Join(names, ", "))
}

func wanPort(ctx context.Context, c *command) error {
	fs := c.flags("NAME PORT [--force] [--yes]")
	force := fs.Bool("force", false, "move even if LAN devices are connected to PORT (they lose their connection)")
	yes := fs.Bool("yes", false, "don't ask before moving the primary connection")
	args, err := c.parse(2, 2)
	if err != nil {
		return err
	}
	api, err := c.connect()
	if err != nil {
		return err
	}
	s, err := loadWANState(ctx, api)
	if err != nil {
		return err
	}
	w, err := findWAN(s.wans, args[0])
	if err != nil {
		return err
	}
	target, err := findPort(s.ports, args[1])
	if err != nil {
		return err
	}
	if target.Role == w.NetworkGroup {
		c.out("%s already uses %s", w.Name, target.Name)
		return nil
	}
	if target.Role != "LAN" {
		return fmt.Errorf("%s is used by %s; move that connection first", target.Name, s.groupName(target.Role))
	}
	// LAN devices on the port would lose their connection, and this machine
	// must never cut itself off.
	if active, err := api.ListActiveClients(ctx); err == nil {
		macs, ips := localAddrs()
		var on []string
		for _, cl := range active {
			if !cl.IsWired || int(cl.SwitchPort) != target.Index || cl.UplinkName == "" || !isRouterName(cl.UplinkName) {
				continue
			}
			for _, m := range macs {
				if strings.EqualFold(m, cl.MAC) {
					return fmt.Errorf("this machine is connected to %s; refusing to make it an internet port", target.Name)
				}
			}
			for _, ip := range ips {
				if ip == cl.IP {
					return fmt.Errorf("this machine is connected to %s; refusing to make it an internet port", target.Name)
				}
			}
			on = append(on, orDefault(cl.Label(), cl.MAC))
		}
		if len(on) > 0 && !*force {
			return fmt.Errorf("LAN devices are connected to %s (%s); they'd lose their connection. Use --force to move anyway", target.Name, strings.Join(on, ", "))
		}
	}
	roles := map[string]string{}
	for _, p := range s.ports {
		roles[p.Interface] = p.Role
	}
	from := s.portOf(w.NetworkGroup)
	oldRoles := map[string]string{}
	for k, v := range roles {
		oldRoles[k] = v
	}
	if from.Interface != "" {
		roles[from.Interface] = "LAN"
	}
	roles[target.Interface] = w.NetworkGroup
	fromText := orDefault(from.Name, "no port")
	if c.dryRun {
		c.out("would move %s from %s to %s; %s would become a LAN port", w.Name, fromText, target.Name, fromText)
		return nil
	}
	primaryGroup := "WAN"
	for _, o := range s.wans {
		if o.IsPrimary() {
			primaryGroup = o.NetworkGroup
		}
	}
	primary := s.link(primaryGroup)
	if w.IsPrimary() && primary.Up && !*yes {
		if err := c.confirm(fmt.Sprintf("%s is your primary internet connection and is up on %s; moving it to %s takes it down\nuntil the line is connected there. If it doesn't come up within %s, drctl moves it back.", w.Name, from.Name, target.Name, wanWait.timeout), w.Name); err != nil {
			return err
		}
	}
	if err := api.SetPortRoles(ctx, roles); err != nil {
		return err
	}
	c.out("moved %s from %s to %s; %s is now a LAN port", w.Name, fromText, target.Name, fromText)
	// The primary connection must stay (or come back) up, or everything goes back.
	if primary.Up {
		if _, up := waitUp(ctx, api, primaryGroup, wanWait.timeout); !up {
			if err := api.SetPortRoles(ctx, oldRoles); err != nil {
				return fmt.Errorf("%s didn't come back within %s, and moving the ports back failed: %w", s.groupName(primaryGroup), wanWait.timeout, err)
			}
			return fmt.Errorf("%s didn't come back within %s; moved the ports back", s.groupName(primaryGroup), wanWait.timeout)
		}
	}
	if w.NetworkGroup != primaryGroup {
		if l, up := waitUp(ctx, api, w.NetworkGroup, wanWait.timeout); up {
			c.out("%s is up on %s: %s", w.Name, target.Name, l.IP)
		} else {
			c.out("%s is not up on %s yet (is the line connected, and are its settings right? see \"drctl wan show %s\")", w.Name, target.Name, w.Name)
		}
	}
	return nil
}

// isRouterName is replaced in tests; it tells whether an uplink name is the
// router itself (devices on its own ports report it as their uplink).
var isRouterName = func(name string) bool { return strings.Contains(name, "Dream Router") || strings.Contains(name, "UDR") }

func portList(ctx context.Context, c *command) error {
	fs := c.flags("[--format table|csv|json]")
	format := fs.String("format", "table", "output format: table, csv or json")
	if _, err := c.parse(0, 0); err != nil {
		return err
	}
	api, err := c.connect()
	if err != nil {
		return err
	}
	s, err := loadWANState(ctx, api)
	if err != nil {
		return err
	}
	var rows [][]string
	for _, p := range s.ports {
		link, speed := "down", ""
		if p.Up {
			link = "up"
			speed = mbpsText(p.SpeedMbps)
		}
		role := p.Role
		if role != "LAN" {
			role = s.groupName(p.Role)
		}
		rows = append(rows, []string{fmt.Sprint(p.Index), p.Name, p.Interface, p.Media, link, speed, role})
	}
	return output(c.env.Stdout, *format, []string{"#", "port", "interface", "media", "link", "speed", "role"}, rows)
}

// waitActive lets the router apply a change, then waits until traffic goes
// out through the connection for group and the internet is reachable.
func waitActive(ctx context.Context, api *unifi.Client, group string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	select {
	case <-ctx.Done():
		return false
	case <-time.After(wanWait.settle):
	}
	for {
		if active, err := api.ActiveWAN(ctx); err == nil && active == group {
			if st, err := api.GetStatus(ctx); err == nil && st.Internet.Status == "ok" {
				return true
			}
		}
		if time.Now().After(deadline) {
			return false
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(wanWait.interval):
		}
	}
}

func wanPrimary(ctx context.Context, c *command) error {
	fs := c.flags("NAME [--yes]")
	yes := fs.Bool("yes", false, "don't ask before switching")
	args, err := c.parse(1, 1)
	if err != nil {
		return err
	}
	api, err := c.connect()
	if err != nil {
		return err
	}
	s, err := loadWANState(ctx, api)
	if err != nil {
		return err
	}
	w, err := findWAN(s.wans, args[0])
	if err != nil {
		return err
	}
	if w.IsPrimary() {
		c.out("%s is already the primary connection", w.Name)
		return nil
	}
	var old unifi.WAN
	for _, o := range s.wans {
		if o.IsPrimary() {
			old = o
		}
	}
	l := s.link(w.NetworkGroup)
	if !l.Up {
		return fmt.Errorf("%s is down; making it primary would leave the internet to the failover at best", w.Name)
	}
	if c.dryRun {
		c.out("would make %s the primary connection (traffic out via %s); %s would become the failover", w.Name, l.IP, orDefault(old.Name, "the current primary"))
		return nil
	}
	if !*yes {
		if err := c.confirm(fmt.Sprintf("New traffic will go out through %s (%s); open connections break and reconnect, and your public IP\nchanges. If it doesn't carry traffic within %s, drctl switches back.", w.Name, l.IP, wanWait.timeout), w.Name); err != nil {
			return err
		}
	}
	start := time.Now()
	if err := api.SetPrimaryWAN(ctx, w.ID); err != nil {
		return err
	}
	if waitActive(ctx, api, w.NetworkGroup, wanWait.timeout) {
		c.out("%s is now the primary connection (traffic out via %s, after %s); %s is the failover", w.Name, l.IP, time.Since(start).Round(time.Second), orDefault(old.Name, "the previous primary"))
		return nil
	}
	if old.ID == "" {
		return fmt.Errorf("%s didn't carry traffic within %s, and there was no previous primary to switch back to", w.Name, wanWait.timeout)
	}
	if err := api.SetPrimaryWAN(ctx, old.ID); err != nil {
		return fmt.Errorf("%s didn't carry traffic within %s, and switching back to %s failed: %w", w.Name, wanWait.timeout, old.Name, err)
	}
	return fmt.Errorf("%s didn't carry traffic within %s; switched back to %s", w.Name, wanWait.timeout, old.Name)
}
