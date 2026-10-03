package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/liketed/dreamrouter-go/unifi"
)

func statusCmd(ctx context.Context, c *command) error {
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
	s, err := api.GetStatus(ctx)
	if err != nil {
		return err
	}
	if *format == "json" {
		enc := json.NewEncoder(c.env.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(statusJSON(s))
	}

	var lines [][2]string
	add := func(k, v string) { lines = append(lines, [2]string{k, v}) }

	in := s.Internet
	inet := []string{orDash(in.Status)}
	if in.IP != "" {
		inet = append(inet, in.IP)
	}
	if in.ISP != "" {
		isp := in.ISP
		if in.ASN != 0 {
			isp += fmt.Sprintf(" (AS%d)", in.ASN)
		}
		inet = append(inet, isp)
	}
	if !in.Up && in.Interface != "" {
		inet = append(inet, "WAN link down")
	}
	add("internet", strings.Join(inet, ", "))
	var link []string
	if in.Interface != "" {
		l := in.Interface
		if in.LinkMbps > 0 {
			l += " " + mbpsText(in.LinkMbps)
		}
		link = append(link, l)
	}
	if in.PPPoE {
		link = append(link, "PPPoE")
	}
	if in.LatencyMs > 0 {
		link = append(link, fmt.Sprintf("latency %d ms", in.LatencyMs))
	}
	if in.Availability > 0 {
		link = append(link, fmt.Sprintf("%g%% available", in.Availability))
	}
	if in.Drops > 0 {
		link = append(link, fmt.Sprintf("%d drops", in.Drops))
	}
	if in.InternetUptime > 0 {
		link = append(link, "up "+durationText(in.InternetUptime))
	}
	if len(link) > 0 {
		add("", strings.Join(link, ", "))
	}

	sy := s.System
	sys := []string{fmt.Sprintf("CPU %g%%", sy.CPUPercent)}
	mem := fmt.Sprintf("memory %g%%", sy.MemoryPercent)
	if sy.MemoryTotal > 0 {
		mem += " of " + bytesText(float64(sy.MemoryTotal))
	}
	sys = append(sys, mem)
	if sy.CPUTempC > 0 {
		sys = append(sys, fmt.Sprintf("CPU temperature %.1f°C", sy.CPUTempC))
	}
	if sy.Load1 > 0 {
		sys = append(sys, fmt.Sprintf("load %.2f", sy.Load1))
	}
	if sy.Overheating {
		sys = append(sys, "OVERHEATING")
	}
	add("system", strings.Join(sys, ", "))

	clients := fmt.Sprintf("%d connected (%d wired, %d Wi-Fi", s.Clients, s.Wired, s.WiFi)
	switch {
	case s.Guests == 1:
		clients += ", 1 guest"
	case s.Guests > 1:
		clients += fmt.Sprintf(", %d guests", s.Guests)
	}
	add("clients", clients+")")

	var devs, updates []string
	for _, d := range s.Devices {
		v := d.Name + " " + d.Version
		if !d.Online {
			v += " (offline)"
		}
		devs = append(devs, v)
		if d.Upgradable {
			u := d.Name
			if d.UpgradeTo != "" {
				u += " " + d.UpgradeTo
			}
			updates = append(updates, u)
		}
	}
	add("devices", strings.Join(devs, ", "))
	switch {
	case len(updates) > 0:
		add("updates", "available: "+strings.Join(updates, ", "))
	case s.UpdateAvailable:
		add("updates", "available for the Network application")
	default:
		add("updates", "none available")
	}

	if st := s.SpeedTest; st.Run.IsZero() {
		add("speed test", "never run")
	} else {
		v := fmt.Sprintf("%s down, %s up, ping %g ms, %s", mbpsFloat(st.DownloadMbps), mbpsFloat(st.UploadMbps), st.PingMs, agoText(st.Run))
		if st.Server != "" {
			v += " (" + st.Server + ")"
		}
		add("speed test", v)
	}

	var problems []string
	for _, name := range sortedKeys(s.Subsystems) {
		if st := s.Subsystems[name]; st != "ok" && st != "unknown" && st != "" {
			problems = append(problems, name+" "+st)
		}
	}
	if len(problems) > 0 {
		add("health", strings.Join(problems, ", "))
	}

	title := s.Name
	if s.Model != "" {
		title += " (" + s.Model + ")"
	}
	var versions []string
	if s.OSVersion != "" {
		versions = append(versions, "UniFi OS "+s.OSVersion)
	}
	if s.NetworkVersion != "" {
		versions = append(versions, "Network "+s.NetworkVersion)
	}
	if s.Uptime > 0 {
		versions = append(versions, "up "+durationText(s.Uptime))
	}
	fmt.Fprintf(c.env.Stdout, "%s, %s\n", title, strings.Join(versions, ", "))
	for _, l := range lines {
		k := l[0]
		if k != "" {
			k += ":"
		}
		fmt.Fprintf(c.env.Stdout, "%-12s%s\n", k, l[1])
	}
	return nil
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// mbpsText formats a link speed, e.g. "2.5 Gbps" or "100 Mbps".
func mbpsText(mbps int) string {
	if mbps >= 1000 {
		return fmt.Sprintf("%g Gbps", float64(mbps)/1000)
	}
	return fmt.Sprintf("%d Mbps", mbps)
}

func mbpsFloat(mbps float64) string {
	if mbps >= 1000 {
		return fmt.Sprintf("%.2f Gbps", mbps/1000)
	}
	return fmt.Sprintf("%.1f Mbps", mbps)
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// statusJSON is the JSON form of the status, with units in the field names.
func statusJSON(s unifi.Status) map[string]any {
	devices := []map[string]any{}
	for _, d := range s.Devices {
		devices = append(devices, map[string]any{
			"name": d.Name, "model": d.Model, "type": d.Type, "mac": d.MAC, "ip": d.IP, "version": d.Version,
			"online": d.Online, "upgradable": d.Upgradable, "upgrade_to": d.UpgradeTo,
			"uptime_seconds": int64(d.Uptime / time.Second), "clients": d.Clients,
		})
	}
	speed := map[string]any{"run": nil}
	if st := s.SpeedTest; !st.Run.IsZero() {
		speed = map[string]any{"run": st.Run.UTC().Format(time.RFC3339), "download_mbps": st.DownloadMbps,
			"upload_mbps": st.UploadMbps, "ping_ms": st.PingMs, "server": st.Server}
	}
	in, sy := s.Internet, s.System
	return map[string]any{
		"name": s.Name, "model": s.Model, "os_version": s.OSVersion, "network_version": s.NetworkVersion,
		"timezone": s.Timezone, "uptime_seconds": int64(s.Uptime / time.Second),
		"internet": map[string]any{
			"status": in.Status, "up": in.Up, "ip": in.IP, "isp": in.ISP, "asn": in.ASN, "interface": in.Interface,
			"link_mbps": in.LinkMbps, "pppoe": in.PPPoE, "latency_ms": in.LatencyMs, "availability_percent": in.Availability,
			"drops": in.Drops, "uptime_seconds": int64(in.InternetUptime / time.Second),
		},
		"system": map[string]any{
			"cpu_percent": sy.CPUPercent, "memory_percent": sy.MemoryPercent, "memory_total_bytes": sy.MemoryTotal,
			"memory_used_bytes": sy.MemoryUsed, "load_1": sy.Load1, "cpu_temperature_c": sy.CPUTempC, "overheating": sy.Overheating,
		},
		"clients":       map[string]any{"total": s.Clients, "wired": s.Wired, "wifi": s.WiFi, "guests": s.Guests},
		"access_points": s.AccessPoints, "switches": s.Switches,
		"devices": devices, "update_available": s.UpdateAvailable, "speed_test": speed, "health": s.Subsystems,
	}
}
