package cli

import (
	"testing"

	"github.com/liketed/dreamrouter-go/fakerouter"
)

func TestSSH(t *testing.T) {
	r := fakerouter.New()
	defer r.Close()
	drctl(t, r, "ssh").ok(t).says(t, "router:  on ", `devices: on, username "fakeadmin"`)
	drctl(t, r, "ssh", "--format", "json").ok(t).says(t, `"router": true`, `"devices_username": "fakeadmin"`)

	drctl(t, r, "ssh", "router", "off", "--dry-run").ok(t).says(t, "would turn off SSH to the router")
	if !r.RouterSSH() {
		t.Fatal("--dry-run turned router SSH off")
	}
	drctl(t, r, "ssh", "router", "off").ok(t).says(t, "turned off SSH to the router; new SSH logins to the router are refused")
	if r.RouterSSH() {
		t.Fatal("router SSH still on")
	}
	drctl(t, r, "ssh", "router", "off").ok(t).says(t, "SSH to the router is already off")
	drctl(t, r, "ssh").ok(t).says(t, "router:  off")
	drctl(t, r, "ssh", "router", "on").ok(t).says(t, "turned on SSH to the router (port 22, with the root password set before)")
	if !r.RouterSSH() {
		t.Fatal("router SSH still off")
	}

	// Device SSH: nothing is written unless it changes.
	drctl(t, r, "ssh", "devices", "on").ok(t).says(t, "SSH to adopted devices is already on")
	if _, writes := r.Mgmt(); writes != 0 {
		t.Fatalf("wrote %d times for no change", writes)
	}
	drctl(t, r, "ssh", "devices", "off").ok(t).says(t, "turned off SSH to adopted devices")
	if m, writes := r.Mgmt(); m["x_ssh_enabled"] != false || writes != 1 {
		t.Fatalf("devices off: %v, %d writes", m, writes)
	}
	drctl(t, r, "ssh").ok(t).says(t, "devices: off ")
	drctl(t, r, "ssh", "devices", "on").ok(t)

	drctl(t, r, "ssh", "router", "maybe").fails(t, 2, `"maybe" must be on or off`)
	drctl(t, r, "ssh", "router").fails(t, 2, "wrong number of arguments")
	drctl(t, r, "ssh", "both", "on").fails(t, 2, `unknown subcommand "both" for ssh`)
}
