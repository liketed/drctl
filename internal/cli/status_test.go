package cli

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/liketed/dreamrouter-go/fakerouter"
)

func TestStatus(t *testing.T) {
	r := fakerouter.New()
	defer r.Close()
	res := drctl(t, r, "status")
	res.ok(t).says(t,
		"Dream Router 7 (UDMA67A), UniFi OS 5.1.33, Network 10.6.106, up 6d 17h\n",
		"internet:   ok, 203.0.113.7, Example ISP (AS64500)\n",
		"            eth3 2.5 Gbps, PPPoE, latency 15 ms, 99.5% available, 2 drops, up 2d 6h\n",
		"system:     CPU 13.9%, memory 58.2% of 3.0 GB, CPU temperature 60.5°C, load 2.75\n",
		"clients:    42 connected (20 wired, 22 Wi-Fi, 1 guest)\n",
		"devices:    Dream Router 7 5.1.33.34087, U7 Pro 8.7.11.19419\n",
		"updates:    none available\n",
		"speed test: never run\n")
	if strings.Contains(res.stdout, "health:") {
		t.Fatalf("health line shown while everything is ok:\n%s", res.stdout)
	}

	r.ModifyStat(func(s *fakerouter.Stat) {
		s.Devices[1]["upgradable"], s.Devices[1]["upgrade_to_firmware"] = true, "8.8.0.1"
		s.Devices[1]["state"] = 0
		s.Devices[0]["speedtest-status"] = map[string]any{"rundate": float64(now().Add(-3 * time.Hour).Unix()), "xput_download": 2210.4,
			"xput_upload": 105.2, "latency": 7, "server": map[string]any{"provider": "Example ISP", "city": "Dublin"}}
		s.Health[1]["status"] = "warning"
		s.Health[2]["status"] = "error"
	})
	drctl(t, r, "status").ok(t).says(t,
		"internet:   error,",
		"U7 Pro 8.7.11.19419 (offline)",
		"updates:    available: U7 Pro 8.8.0.1\n",
		"speed test: 2.21 Gbps down, 105.2 Mbps up, ping 7 ms, 3h 0m ago (Example ISP, Dublin)\n",
		"health:     wan warning, www error\n")

	res = drctl(t, r, "status", "--format", "json")
	res.ok(t)
	var got map[string]any
	if err := json.Unmarshal([]byte(res.stdout), &got); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, res.stdout)
	}
	in := got["internet"].(map[string]any)
	if got["os_version"] != "5.1.33" || in["ip"] != "203.0.113.7" || in["link_mbps"] != 2500.0 || got["update_available"] != true ||
		got["speed_test"].(map[string]any)["download_mbps"] != 2210.4 || got["clients"].(map[string]any)["wifi"] != 22.0 {
		t.Fatalf("JSON: %s", res.stdout)
	}

	drctl(t, r, "status", "--format", "csv").fails(t, 2, `unknown format "csv"`)
	drctl(t, r, "status", "extra").fails(t, 2, "wrong number of arguments")
}
