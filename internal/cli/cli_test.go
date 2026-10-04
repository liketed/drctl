package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"testing"
	"time"

	"github.com/liketed/dreamrouter-go/fakerouter"
	"github.com/liketed/dreamrouter-go/unifi"
)

type result struct {
	code           int
	stdout, stderr string
}

// drctl runs a command in-process against the fake router.
func drctl(t *testing.T, r *fakerouter.Router, args ...string) result {
	t.Helper()
	var out, errOut bytes.Buffer
	env := Env{
		Stdout: &out, Stderr: &errOut, Insecure: true,
		Getenv: func(k string) string {
			switch k {
			case "DREAMROUTER_HOST":
				return r.Host()
			case "UNIFI_PASS":
				return fakerouter.Password
			}
			return ""
		},
		newClient: func(cfg unifi.Config) (*unifi.Client, error) {
			cfg.RetryIntervals = []time.Duration{20 * time.Millisecond}
			return unifi.New(cfg)
		},
	}
	code := Run(context.Background(), args, env)
	return result{code, out.String(), errOut.String()}
}

func (res result) ok(t *testing.T) result {
	t.Helper()
	if res.code != 0 {
		t.Fatalf("exit %d\nstdout: %s\nstderr: %s", res.code, res.stdout, res.stderr)
	}
	return res
}

func (res result) fails(t *testing.T, code int, wantErr string) result {
	t.Helper()
	if res.code != code || !strings.Contains(res.stderr, wantErr) {
		t.Fatalf("got exit %d, stderr %q; want exit %d with %q", res.code, res.stderr, code, wantErr)
	}
	return res
}

func (res result) says(t *testing.T, want ...string) result {
	t.Helper()
	for _, w := range want {
		if !strings.Contains(res.stdout, w) {
			t.Fatalf("stdout %q does not contain %q", res.stdout, w)
		}
	}
	return res
}

func writeTemp(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "in.csv")
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// ---------- DNS ----------

func TestDNSAddUpdateAppend(t *testing.T) {
	r := fakerouter.New()
	defer r.Close()
	drctl(t, r, "dns", "add", "nas.home.internal", "192.168.1.50").ok(t).says(t, "created A nas.home.internal -> 192.168.1.50")
	drctl(t, r, "dns", "add", "nas.home.internal", "192.168.1.50").ok(t).says(t, "unchanged A nas.home.internal -> 192.168.1.50")
	drctl(t, r, "dns", "add", "nas.home.internal.", "192.168.1.51").ok(t).says(t, "updated A nas.home.internal -> 192.168.1.51 (was 192.168.1.50)")
	drctl(t, r, "dns", "add", "nas.home.internal", "192.168.1.52", "--append").ok(t).says(t, "created A nas.home.internal -> 192.168.1.52")
	drctl(t, r, "dns", "add", "nas.home.internal", "192.168.1.53").fails(t, 1, "already has 2 A records (192.168.1.51, 192.168.1.52); use --append")
	if n := len(r.DNS()); n != 2 {
		t.Fatalf("%d records, want 2", n)
	}
}

func TestDNSAllTypesAndValidation(t *testing.T) {
	r := fakerouter.New()
	defer r.Close()
	for _, args := range [][]string{
		{"nas.home.internal", "fd00::50", "--type", "aaaa", "--ttl", "300"},
		{"files.home.internal", "nas.home.internal", "--type", "CNAME"},
		{"home.internal", "mail.home.internal", "--type", "MX", "--priority", "10"},
		{"lab.home.internal", "192.168.1.2", "--type", "NS"},
		{"_sip._tcp.home.internal", "pbx.home.internal", "--type", "SRV", "--priority", "10", "--weight", "5", "--port", "5060"},
		{"home.internal", "v=spf1 -all", "--type", "TXT"},
		{"off.home.internal", "192.168.1.9", "--disabled"},
	} {
		drctl(t, r, append([]string{"dns", "add"}, args...)...).ok(t)
	}
	if n := len(r.DNS()); n != 7 {
		t.Fatalf("%d records stored, want 7", n)
	}
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"x.home.internal", "999.1.1.1"}, `value must be an IPv4 address for A records (got "999.1.1.1")`},
		{[]string{"home.internal", "mail.home.internal", "--type", "MX", "--ttl", "60"}, "ttl cannot be set for MX records (only A, AAAA, CNAME records)"},
		{[]string{"x.home.internal", "192.168.1.5", "--port", "80"}, "port cannot be set for A records"},
		{[]string{"lab.home.internal", "ns1.home.internal", "--type", "NS"}, "conditional forwarders"},
		{[]string{"x.home.internal", "x.home.internal", "--type", "CNAME"}, "cannot point to itself"},
		{[]string{"x.home.internal", `say "hi" now`, "--type", "TXT"}, "double quotes are only allowed around the whole value"},
		{[]string{"sip.home.internal", "pbx.home.internal", "--type", "SRV"}, "_service._protocol.domain"},
		{[]string{"a,b.home.internal", "192.168.1.5"}, "must not be empty or contain whitespace or commas"},
		{[]string{"x.home.internal", "y", "--type", "PTR"}, "unsupported record type"},
	} {
		drctl(t, r, append([]string{"dns", "add"}, tc.args...)...).fails(t, 1, tc.want)
	}
	if n := len(r.DNS()); n != 7 {
		t.Fatalf("validation failures reached the router: %d records", n)
	}
	drctl(t, r, "dns", "add", "x.home.internal").fails(t, 2, "wrong number of arguments")
}

func TestDNSDelete(t *testing.T) {
	r := fakerouter.New()
	defer r.Close()
	r.PutDNS(fakerouter.DNSRecord{RecordType: "A", Key: "rr.home.internal", Value: "192.168.1.10", Enabled: true})
	r.PutDNS(fakerouter.DNSRecord{RecordType: "A", Key: "rr.home.internal", Value: "192.168.1.11", Enabled: true})
	r.PutDNS(fakerouter.DNSRecord{RecordType: "TXT", Key: "rr.home.internal", Value: "hello", Enabled: true})
	drctl(t, r, "dns", "delete", "rr.home.internal").fails(t, 1, "3 records match rr.home.internal")
	drctl(t, r, "dns", "delete", "rr.home.internal", "--type", "txt").ok(t).says(t, "deleted TXT rr.home.internal -> hello")
	drctl(t, r, "dns", "delete", "rr.home.internal", "--value", "192.168.1.10", "--dry-run").ok(t).says(t, "would delete A rr.home.internal -> 192.168.1.10")
	if n := len(r.DNS()); n != 2 {
		t.Fatalf("dry run deleted something: %d records", n)
	}
	drctl(t, r, "dns", "delete", "rr.home.internal", "--all").ok(t).says(t, "deleted A rr.home.internal -> 192.168.1.10", "deleted A rr.home.internal -> 192.168.1.11")
	drctl(t, r, "dns", "delete", "rr.home.internal").fails(t, 1, "no record found for rr.home.internal")
}

func TestDNSListAndRoundTrip(t *testing.T) {
	r := fakerouter.New()
	defer r.Close()
	r.PutDNS(fakerouter.DNSRecord{RecordType: "A", Key: "b.home.internal", Value: "192.168.1.2", Enabled: true})
	r.PutDNS(fakerouter.DNSRecord{RecordType: "SRV", Key: "_sip._tcp.home.internal", Value: "pbx.home.internal", Priority: 10, Weight: 5, Port: 5060, Enabled: true})
	r.PutDNS(fakerouter.DNSRecord{RecordType: "A", Key: "off.home.internal", Value: "192.168.1.9", Enabled: false})

	table := drctl(t, r, "dns", "list").ok(t).stdout
	for _, want := range []string{"TYPE", "priority=10 weight=5 port=5060", "disabled"} {
		if !strings.Contains(table, want) {
			t.Fatalf("table missing %q:\n%s", want, table)
		}
	}
	var objs []map[string]string
	if err := json.Unmarshal([]byte(drctl(t, r, "dns", "list", "--format", "json", "--type", "A").ok(t).stdout), &objs); err != nil || len(objs) != 2 {
		t.Fatalf("json: %v %v", err, objs)
	}
	csvOut := drctl(t, r, "dns", "list", "--format", "csv").ok(t).stdout
	if !strings.HasPrefix(csvOut, "type,name,value,ttl,priority,weight,port,enabled\n") {
		t.Fatalf("csv header: %q", csvOut)
	}
	// The CSV output imports back with no changes, including the disabled record.
	drctl(t, r, "dns", "import", writeTemp(t, csvOut)).ok(t).says(t, "created 0, updated 0, unchanged 3")
}

func TestDNSImport(t *testing.T) {
	r := fakerouter.New()
	defer r.Close()
	r.PutDNS(fakerouter.DNSRecord{RecordType: "A", Key: "old.home.internal", Value: "192.168.1.99", Enabled: true})
	legacy := writeTemp(t, "# hosts\n\nnas.home.internal, 192.168.1.50\nold.home.internal,192.168.1.98\n")
	drctl(t, r, "dns", "import", legacy, "--dry-run").ok(t).says(t, "would create A nas.home.internal", "dry run: would create 1, would update 1, unchanged 0")
	if n := len(r.DNS()); n != 1 {
		t.Fatalf("dry run changed the router: %d records", n)
	}
	drctl(t, r, "dns", "import", legacy).ok(t).says(t, "created 1, updated 1, unchanged 0")

	headed := writeTemp(t, "type,name,value,priority\nMX,home.internal,mx1.home.internal,10\nMX,home.internal,mx2.home.internal,20\n")
	drctl(t, r, "dns", "import", headed).ok(t).says(t, "created 2")

	drctl(t, r, "dns", "import", writeTemp(t, "a.home.internal,192.168.1.1\nb.home.internal,1.2.3\n")).fails(t, 1, `in.csv:2: value must be an IPv4 address for A records (got "1.2.3")`)
	drctl(t, r, "dns", "import", writeTemp(t, "a.home.internal,192.168.1.1\na.home.internal,192.168.1.1\n")).fails(t, 1, "in.csv:2: duplicate record")

	del := writeTemp(t, "nas.home.internal\nold.home.internal,192.168.1.1\nmissing.home.internal\n")
	drctl(t, r, "dns", "import", del, "--delete").ok(t).says(t,
		"deleted A nas.home.internal", "skipped A old.home.internal -> 192.168.1.1 (is 192.168.1.98)", "not found A missing.home.internal",
		"deleted 1, not found 1, skipped 1")
}

// ---------- DHCP ----------

func TestDHCPAddUpdateDelete(t *testing.T) {
	r := fakerouter.New()
	defer r.Close()
	drctl(t, r, "dhcp", "add", "AA-BB-CC-00-00-01", "192.168.1.50", "--name", "nas").ok(t).says(t, "created aa:bb:cc:00:00:01 -> 192.168.1.50 (nas)")
	c, ok := r.Client("aa:bb:cc:00:00:01")
	if !ok || !c.UseFixedIP || c.FixedIP != "192.168.1.50" || c.NetworkID != fakerouter.NetworkID || c.Name != "nas" {
		t.Fatalf("stored %+v", c)
	}
	drctl(t, r, "dhcp", "add", "aa:bb:cc:00:00:01", "192.168.1.50").ok(t).says(t, "unchanged aa:bb:cc:00:00:01 -> 192.168.1.50 (nas)")
	drctl(t, r, "dhcp", "add", "aa:bb:cc:00:00:01", "192.168.1.51").ok(t).says(t, "updated aa:bb:cc:00:00:01 -> 192.168.1.51 (nas) (was 192.168.1.50)")

	// An existing device (seen on the network) keeps its name and gets a reservation.
	r.PutClient(fakerouter.Client{MAC: "aa:bb:cc:00:00:02", Hostname: "printer", LastIP: "192.168.1.77"})
	drctl(t, r, "dhcp", "add", "aa:bb:cc:00:00:02", "192.168.1.60").ok(t).says(t, "created", "(printer)")
	if n := len(r.Clients()); n != 2 {
		t.Fatalf("%d clients, want 2 (existing device updated, not duplicated)", n)
	}

	drctl(t, r, "dhcp", "add", "aa:bb:cc:00:00:03", "192.168.1.51").fails(t, 1, "192.168.1.51 is already reserved for aa:bb:cc:00:00:01 (nas)")
	drctl(t, r, "dhcp", "add", "aa:bb:cc:00:00:03", "10.0.0.5").fails(t, 1, "not in any network's subnet (networks: Default 192.168.1.1/24)")
	drctl(t, r, "dhcp", "add", "aa:bb:cc:00:00:03", "192.168.1.1").fails(t, 1, "the router's own address")
	drctl(t, r, "dhcp", "add", "aa:bb:cc:00:00:03", "192.168.1.255").fails(t, 1, "broadcast address")
	drctl(t, r, "dhcp", "add", "aa:bb:cc:00:00:03", "192.168.1.70", "--network", "IoT").fails(t, 1, `no network named "IoT"`)
	drctl(t, r, "dhcp", "add", "not-a-mac", "192.168.1.70").fails(t, 1, "invalid MAC address")
	res := drctl(t, r, "dhcp", "add", "aa:bb:cc:00:00:03", "192.168.1.77").ok(t)
	if !strings.Contains(res.stderr, "192.168.1.77 is currently in use by aa:bb:cc:00:00:02 (printer)") {
		t.Fatalf("missing in-use warning: %q", res.stderr)
	}

	drctl(t, r, "dhcp", "delete", "aa:bb:cc:00:00:02").ok(t).says(t, "removed reservation aa:bb:cc:00:00:02 (printer) -> 192.168.1.60")
	if c, _ := r.Client("aa:bb:cc:00:00:02"); c.UseFixedIP || c.Hostname != "printer" {
		t.Fatalf("after delete: %+v (should keep the device, without a reservation)", c)
	}
	drctl(t, r, "dhcp", "delete", "aa:bb:cc:00:00:02").fails(t, 1, "has no DHCP reservation")
	drctl(t, r, "dhcp", "delete", "aa:bb:cc:00:00:02", "--forget").ok(t).says(t, "forgot device aa:bb:cc:00:00:02 (printer)")
	if _, ok := r.Client("aa:bb:cc:00:00:02"); ok {
		t.Fatal("device not forgotten")
	}
	drctl(t, r, "dhcp", "delete", "aa:bb:cc:00:00:99").fails(t, 1, "no device with MAC aa:bb:cc:00:00:99")
}

func TestDHCPListAndImport(t *testing.T) {
	r := fakerouter.New()
	defer r.Close()
	r.PutClient(fakerouter.Client{MAC: "aa:bb:cc:00:00:09", Name: "other", UseFixedIP: true, FixedIP: "192.168.1.90", NetworkID: fakerouter.NetworkID})
	in := writeTemp(t, "aa:bb:cc:00:00:01,192.168.1.50,nas\naa:bb:cc:00:00:02, 192.168.1.60, printer, Default\n")
	drctl(t, r, "dhcp", "import", in, "--dry-run").ok(t).says(t, "dry run: would create 2")
	if n := len(r.Clients()); n != 1 {
		t.Fatalf("dry run changed the router")
	}
	drctl(t, r, "dhcp", "import", in).ok(t).says(t, "created 2, updated 0, unchanged 0")

	csvOut := drctl(t, r, "dhcp", "list", "--format", "csv").ok(t).stdout
	if !strings.HasPrefix(csvOut, "mac,ip,name,network\naa:bb:cc:00:00:01,192.168.1.50,nas,Default\n") {
		t.Fatalf("csv (sorted by IP): %q", csvOut)
	}
	drctl(t, r, "dhcp", "import", writeTemp(t, csvOut)).ok(t).says(t, "created 0, updated 0, unchanged 3")

	// Conflicts are found before anything changes.
	drctl(t, r, "dhcp", "import", writeTemp(t, "aa:bb:cc:00:00:05,192.168.1.55\naa:bb:cc:00:00:06,192.168.1.90\n")).
		fails(t, 1, "in.csv:2: 192.168.1.90 is already reserved for aa:bb:cc:00:00:09 (other); nothing was changed")
	drctl(t, r, "dhcp", "import", writeTemp(t, "aa:bb:cc:00:00:05,192.168.1.55\naa:bb:cc:00:00:05,192.168.1.56\n")).fails(t, 1, "in.csv:2: MAC aa:bb:cc:00:00:05 is also on line 1")
	drctl(t, r, "dhcp", "import", writeTemp(t, "aa:bb:cc:00:00:05,192.168.1.55\naa:bb:cc:00:00:06,192.168.1.55\n")).fails(t, 1, "in.csv:2: IP 192.168.1.55 is also on line 1")
	if _, ok := r.Client("aa:bb:cc:00:00:05"); ok {
		t.Fatal("a failed import changed the router")
	}

	// Moving an IP from one device to another in the same file works in any line order.
	drctl(t, r, "dhcp", "import", writeTemp(t, "aa:bb:cc:00:00:02,192.168.1.50\naa:bb:cc:00:00:01,192.168.1.51\n")).ok(t).says(t, "updated 2")
	if c, _ := r.Client("aa:bb:cc:00:00:02"); c.FixedIP != "192.168.1.50" {
		t.Fatalf("printer: %+v", c)
	}
	// A swap can't be done in one step and is rejected up front.
	drctl(t, r, "dhcp", "import", writeTemp(t, "aa:bb:cc:00:00:01,192.168.1.50\naa:bb:cc:00:00:02,192.168.1.51\n")).fails(t, 1, "swap IPs between devices")

	del := writeTemp(t, "mac,ip\naa:bb:cc:00:00:01\naa:bb:cc:00:00:09,192.168.1.1\naa:bb:cc:00:00:77\n")
	drctl(t, r, "dhcp", "import", del, "--delete").ok(t).says(t,
		"removed reservation aa:bb:cc:00:00:01 (nas)", "skipped aa:bb:cc:00:00:09 (other) (reserved IP is 192.168.1.90, file says 192.168.1.1)",
		"not found aa:bb:cc:00:00:77", "removed 1, not found 1, skipped 1")
}

// ---------- hosts ----------

func TestHosts(t *testing.T) {
	r := fakerouter.New()
	defer r.Close()
	r.PutDNS(fakerouter.DNSRecord{RecordType: "A", Key: "static.home.internal", Value: "192.168.1.5", Enabled: true})

	drctl(t, r, "host", "add", "nas.home.internal", "192.168.1.50", "--mac", "aa:bb:cc:00:00:01").ok(t).says(t, "created host nas.home.internal -> 192.168.1.50 (aa:bb:cc:00:00:01)")
	c, _ := r.Client("aa:bb:cc:00:00:01")
	if !c.UseFixedIP || c.FixedIP != "192.168.1.50" || c.LocalDNSRecord != "nas.home.internal" || !c.LocalDNSRecordEnabled || c.Name != "nas.home.internal" {
		t.Fatalf("stored %+v", c)
	}
	drctl(t, r, "host", "add", "nas.home.internal", "192.168.1.50", "--mac", "aa:bb:cc:00:00:01").ok(t).says(t, "unchanged host")
	drctl(t, r, "host", "add", "nas2.home.internal", "192.168.1.52", "--mac", "aa:bb:cc:00:00:01").ok(t).
		says(t, "updated host nas2.home.internal -> 192.168.1.52 (aa:bb:cc:00:00:01) (was 192.168.1.50, nas.home.internal)")

	drctl(t, r, "host", "add", "static.home.internal", "192.168.1.60", "--mac", "aa:bb:cc:00:00:02").
		fails(t, 1, `static.home.internal is already a static DNS record (A static.home.internal -> 192.168.1.5); delete it with "drctl dns delete static.home.internal"`)
	drctl(t, r, "host", "add", "nas2.home.internal", "192.168.1.60", "--mac", "aa:bb:cc:00:00:02").fails(t, 1, "already the DNS name of aa:bb:cc:00:00:01")
	drctl(t, r, "host", "add", "x.home.internal", "192.168.1.60").fails(t, 2, "--mac is required")
	drctl(t, r, "dns", "add", "nas2.home.internal", "192.168.1.8").fails(t, 1, "already the DNS name of device")

	drctl(t, r, "host", "add", "tv.home.internal", "192.168.1.70", "--mac", "aa:bb:cc:00:00:03").ok(t)
	list := drctl(t, r, "host", "list", "--format", "csv").ok(t).stdout
	if list != "name,ip,mac,device,network\nnas2.home.internal,192.168.1.52,aa:bb:cc:00:00:01,nas.home.internal,Default\ntv.home.internal,192.168.1.70,aa:bb:cc:00:00:03,tv.home.internal,Default\n" {
		t.Fatalf("host list:\n%s", list)
	}
	drctl(t, r, "dhcp", "list").ok(t).says(t, "tv.home.internal")

	drctl(t, r, "host", "delete", "tv.home.internal", "--keep-reservation").ok(t).says(t, "removed DNS name tv.home.internal from aa:bb:cc:00:00:03 (tv.home.internal) (kept reservation 192.168.1.70)")
	if c, _ := r.Client("aa:bb:cc:00:00:03"); !c.UseFixedIP || c.LocalDNSRecordEnabled {
		t.Fatalf("keep-reservation: %+v", c)
	}
	drctl(t, r, "host", "delete", "nas2.home.internal").ok(t).says(t, "removed reservation aa:bb:cc:00:00:01 (nas.home.internal) -> 192.168.1.52 and DNS name nas2.home.internal")
	if c, _ := r.Client("aa:bb:cc:00:00:01"); c.UseFixedIP || c.LocalDNSRecordEnabled {
		t.Fatalf("after host delete: %+v", c)
	}
	drctl(t, r, "host", "delete", "nas2.home.internal").fails(t, 1, "no host named nas2.home.internal")
}

// ---------- general ----------

func TestOneLoginPerCommand(t *testing.T) {
	r := fakerouter.New()
	defer r.Close()
	drctl(t, r, "host", "add", "nas.home.internal", "192.168.1.50", "--mac", "aa:bb:cc:00:00:01").ok(t)
	drctl(t, r, "dns", "import", writeTemp(t, "a.home.internal,192.168.1.1\nb.home.internal,192.168.1.2\nc.home.internal,192.168.1.3\n")).ok(t)
	if n := r.LoginCount(); n != 2 {
		t.Fatalf("logins = %d, want one per command", n)
	}
}

func TestLoginLimitRetry(t *testing.T) {
	r := fakerouter.New()
	defer r.Close()
	drctl(t, r, "dns", "list").ok(t)
	r.SetLoginLimit(1)
	go func() {
		time.Sleep(100 * time.Millisecond)
		r.SetLoginLimit(0)
	}()
	res := drctl(t, r, "dns", "list").ok(t)
	if !strings.Contains(res.stderr, "router login limit reached, retrying") {
		t.Fatalf("stderr %q", res.stderr)
	}
	r.SetLoginLimit(r.LoginCount())
	drctl(t, r, "dns", "list", "--login-retry", "0").fails(t, 1, "HTTP 429")
}

func TestUsageAndPassword(t *testing.T) {
	r := fakerouter.New()
	defer r.Close()
	drctl(t, r).ok(t).says(t, "drctl dhcp add")
	drctl(t, r, "version").ok(t).says(t, "drctl dev (")
	drctl(t, r, "nope").fails(t, 2, `unknown command "nope"`)
	drctl(t, r, "dns").fails(t, 2, "needs a subcommand")
	drctl(t, r, "dns", "list", "--bogus").fails(t, 2, "flag provided but not defined: -bogus")

	var out, errOut bytes.Buffer
	code := Run(context.Background(), []string{"dns", "list"}, Env{Stdout: &out, Stderr: &errOut, Insecure: true,
		Getenv: func(k string) string {
			if k == "DREAMROUTER_HOST" {
				return r.Host()
			}
			return ""
		}})
	if code != 1 || !strings.Contains(errOut.String(), "no password: set DREAMROUTER_PASSWORD or UNIFI_PASS") {
		t.Fatalf("exit %d, %q", code, errOut.String())
	}
}

func TestDNSListShowsHostNames(t *testing.T) {
	r := fakerouter.New()
	defer r.Close()
	r.PutDNS(fakerouter.DNSRecord{RecordType: "A", Key: "nas.home.internal", Value: "192.168.1.50", Enabled: true})
	drctl(t, r, "host", "add", "wtrpro0.test.com", "192.168.1.124", "--mac", "c8:ff:bf:05:d7:58").ok(t)

	table := drctl(t, r, "dns", "list").ok(t).stdout
	lines := strings.Split(strings.TrimSpace(table), "\n")
	if len(lines) != 3 || !strings.Contains(lines[1], "nas.home.internal") ||
		!strings.Contains(lines[2], "wtrpro0.test.com") || !strings.Contains(lines[2], "192.168.1.124") ||
		!strings.Contains(lines[2], "host (c8:ff:bf:05:d7:58, wtrpro0.test.com)") {
		t.Fatalf("table:\n%s", table)
	}

	var objs []map[string]string
	if err := json.Unmarshal([]byte(drctl(t, r, "dns", "list", "--format", "json").ok(t).stdout), &objs); err != nil {
		t.Fatal(err)
	}
	if len(objs) != 2 || objs[0]["source"] != "static" || objs[1]["source"] != "host" || objs[1]["mac"] != "c8:ff:bf:05:d7:58" {
		t.Fatalf("json: %v", objs)
	}

	res := drctl(t, r, "dns", "list", "--format", "csv").ok(t)
	if strings.Contains(res.stdout, "wtrpro0") || !strings.Contains(res.stdout, "nas.home.internal") {
		t.Fatalf("csv should list static records only:\n%s", res.stdout)
	}
	if !strings.Contains(res.stderr, "1 device DNS name(s) not included in the CSV") {
		t.Fatalf("missing csv note: %q", res.stderr)
	}
	drctl(t, r, "dns", "import", writeTemp(t, res.stdout)).ok(t).says(t, "created 0, updated 0, unchanged 1")

	if out := drctl(t, r, "dns", "list", "--static").ok(t).stdout; strings.Contains(out, "wtrpro0") {
		t.Fatalf("--static shows host names:\n%s", out)
	}
	if out := drctl(t, r, "dns", "list", "--type", "AAAA").ok(t).stdout; strings.Contains(out, "wtrpro0") {
		t.Fatalf("--type AAAA shows an A host name:\n%s", out)
	}
	drctl(t, r, "dns", "list", "--name", "wtrpro0.test.com").ok(t).says(t, "wtrpro0.test.com")

	drctl(t, r, "dns", "delete", "wtrpro0.test.com").fails(t, 1, `wtrpro0.test.com is the DNS name of device wtrpro0.test.com (c8:ff:bf:05:d7:58, 192.168.1.124), not a static record; remove it with "drctl host delete wtrpro0.test.com"`)
	if c, _ := r.Client("c8:ff:bf:05:d7:58"); !c.LocalDNSRecordEnabled {
		t.Fatal("dns delete touched the host")
	}
}

func TestFormatVersion(t *testing.T) {
	info := func(mainVersion string, settings ...string) *debug.BuildInfo {
		bi := &debug.BuildInfo{GoVersion: "go1.26.5", Main: debug.Module{Path: "github.com/liketed/drctl", Version: mainVersion}}
		for i := 0; i+1 < len(settings); i += 2 {
			bi.Settings = append(bi.Settings, debug.BuildSetting{Key: settings[i], Value: settings[i+1]})
		}
		return bi
	}
	for _, tc := range []struct {
		name, version string
		info          *debug.BuildInfo
		want          string
	}{
		{"go install @commit", "dev", info("v0.0.0-20260928220345-71d2dbb77da8"),
			"drctl dev (commit 71d2dbb, committed 2026-09-28 22:03 UTC, go1.26.5)"},
		{"go install after a tag", "dev", info("v0.1.1-0.20260928220345-71d2dbb77da8"),
			"drctl dev (commit 71d2dbb, committed 2026-09-28 22:03 UTC, go1.26.5)"},
		{"go install @tag", "dev", info("v0.1.0"), "drctl v0.1.0 (go1.26.5)"},
		{"go build in a clone", "dev", info("(devel)", "vcs.revision", "71d2dbb77da894a034d950f18d22118ccb25ee0a",
			"vcs.time", "2026-09-28T22:03:45Z", "vcs.modified", "false"),
			"drctl dev (commit 71d2dbb, committed 2026-09-28 22:03 UTC, go1.26.5)"},
		{"go build with local changes", "dev", info("(devel)", "vcs.revision", "71d2dbb77da894a0", "vcs.modified", "true"),
			"drctl dev (commit 71d2dbb with uncommitted changes, go1.26.5)"},
		{"go build in a clone (Go 1.24+ stamps a pseudo-version)", "dev", info("v0.0.0-20260928220345-71d2dbb77da8+dirty",
			"vcs.revision", "71d2dbb77da894a034d950f18d22118ccb25ee0a", "vcs.time", "2026-09-28T22:03:45Z", "vcs.modified", "true"),
			"drctl dev (commit 71d2dbb with uncommitted changes, committed 2026-09-28 22:03 UTC, go1.26.5)"},
		{"version from -ldflags", "0.2.0", info("v0.0.0-20260928220345-71d2dbb77da8"),
			"drctl 0.2.0 (commit 71d2dbb, committed 2026-09-28 22:03 UTC, go1.26.5)"},
		{"no build info", "dev", nil, "drctl dev"},
	} {
		if got := formatVersion(tc.version, tc.info); got != tc.want {
			t.Errorf("%s:\n got  %q\n want %q", tc.name, got, tc.want)
		}
	}
}

func TestNetworkListAndShow(t *testing.T) {
	r := fakerouter.New()
	defer r.Close()
	list := drctl(t, r, "network", "list").ok(t).stdout
	if !strings.Contains(list, "Default  192.168.1.0/24  on    192.168.1.6 - 192.168.1.254  localdomain  off") || strings.Contains(list, "Internet 1") {
		t.Fatalf("network list (WAN connections must be left out):\n%s", list)
	}
	drctl(t, r, "network", "show").ok(t).says(t, "Network Default (192.168.1.0/24)", "Network boot     off", "TFTP server      (not set)")
	drctl(t, r, "network", "show", "default", "--format", "csv").ok(t).says(t, "name,subnet,dhcp,dhcp_start,dhcp_stop,domain,boot_enabled,boot_server,boot_file,tftp_server,dns_servers,lease_seconds,ntp_servers\nDefault,192.168.1.0/24,on,")
	drctl(t, r, "network", "show", "IoT").fails(t, 1, `no network named "IoT" (networks: Default 192.168.1.1/24)`)
}

func TestNetworkBoot(t *testing.T) {
	r := fakerouter.New()
	defer r.Close()
	stored := func() map[string]any { return r.Network("Default") }

	drctl(t, r, "network", "boot", "--server", "192.168.1.20", "--file", "netboot.xyz.efi", "--dry-run").ok(t).
		says(t, "would update network boot on Default:", "network boot  off -> on", "boot server   - -> 192.168.1.20", "boot file     - -> netboot.xyz.efi")
	if _, ok := stored()["dhcpd_boot_enabled"]; ok {
		t.Fatal("dry run changed the router")
	}

	drctl(t, r, "network", "boot", "Default", "--server", "192.168.1.20", "--file", "netboot.xyz.efi").ok(t).
		says(t, "updated network boot on Default: on, server 192.168.1.20, file netboot.xyz.efi", "next time they ask for an address")
	if n := stored(); n["dhcpd_boot_enabled"] != true || n["dhcpd_boot_server"] != "192.168.1.20" || n["dhcpd_boot_filename"] != "netboot.xyz.efi" || n["dhcpd_start"] != "192.168.1.6" {
		t.Fatalf("stored %v", n)
	}
	drctl(t, r, "network", "boot", "--server", "192.168.1.20", "--file", "netboot.xyz.efi").ok(t).says(t, "unchanged network boot on Default")
	drctl(t, r, "network", "list").ok(t).says(t, "localdomain  on")

	drctl(t, r, "network", "boot", "--tftp-server", "tftp.home.internal").ok(t).says(t, "TFTP server tftp.home.internal")
	res := drctl(t, r, "network", "boot", "--off").ok(t)
	res.says(t, "updated network boot on Default: off (server 192.168.1.20 and file netboot.xyz.efi kept, not active), TFTP server tftp.home.internal",
		"TFTP server tftp.home.internal is still handed out (DHCP option 66); use --no-tftp")
	if n := stored(); n["dhcpd_boot_enabled"] != false || n["dhcpd_boot_filename"] != "netboot.xyz.efi" {
		t.Fatalf("after --off: %v", n)
	}
	drctl(t, r, "network", "show").ok(t).says(t, "Network boot     off (stored: server 192.168.1.20, file netboot.xyz.efi)")
	drctl(t, r, "network", "boot", "--no-tftp").ok(t)
	if stored()["dhcpd_tftp_server"] != "" {
		t.Fatal("--no-tftp did not clear the TFTP server")
	}

	// A boot server outside the subnet is allowed, with a warning.
	res = drctl(t, r, "network", "boot", "--server", "10.0.0.5", "--file", "a.efi").ok(t)
	if !strings.Contains(res.stderr, "boot server 10.0.0.5 is outside Default's subnet (192.168.1.0/24)") {
		t.Fatalf("missing warning: %q", res.stderr)
	}

	for _, tc := range []struct {
		args []string
		code int
		want string
	}{
		{[]string{"--server", "boot.home.internal", "--file", "a.efi"}, 1, "must be an IPv4 address"},
		{[]string{"--server", "192.168.1.20", "--file", "a,b.efi"}, 1, "spaces or commas"},
		{[]string{"--server", "192.168.1.20", "--file", "a b.efi"}, 1, "spaces or commas"},
		{[]string{"--tftp-server", "a,b"}, 1, "without spaces or commas"},
		{[]string{"--server", "192.168.1.20"}, 2, "needs both --server and --file"},
		{[]string{"--off", "--file", "a.efi"}, 2, "--off can't be combined"},
		{[]string{"--tftp-server", "x", "--no-tftp"}, 2, "can't be combined"},
		{[]string{}, 2, "nothing to change"},
		{[]string{"IoT", "--off"}, 1, `no network named "IoT"`},
	} {
		drctl(t, r, append([]string{"network", "boot"}, tc.args...)...).fails(t, tc.code, tc.want)
	}
	if stored()["dhcpd_boot_server"] != "10.0.0.5" {
		t.Fatal("a rejected command changed the router")
	}
}

func TestLeases(t *testing.T) {
	r := fakerouter.New()
	defer r.Close()
	fixed := time.Date(2026, 10, 2, 21, 0, 0, 0, time.UTC)
	now = func() time.Time { return fixed }
	defer func() { now = time.Now }()
	exp := func(d time.Duration) float64 { return float64(fixed.Add(d).Unix()) }

	r.PutLease(fakerouter.Lease{IP: "192.168.1.37", MAC: "aa:bb:cc:00:00:37", Hostname: "tv", OUI: "Samsung", Status: "online", ClientType: "WIRED", ExpiresUnix: exp(17*time.Hour + 5*time.Minute)})
	r.PutLease(fakerouter.Lease{IP: "192.168.1.6", MAC: "aa:bb:cc:00:00:06", Hostname: "macbook", OUI: "Apple", Status: "online", ClientType: "WIRELESS", ExpiresUnix: exp(23*time.Hour + 41*time.Minute)})
	r.PutLease(fakerouter.Lease{IP: "192.168.1.12", MAC: "aa:bb:cc:00:00:12", Hostname: "pxe", OUI: "Raspberry Pi", Status: "offline", ExpiresUnix: exp(3*24*time.Hour + 4*time.Hour)})
	r.PutClient(fakerouter.Client{MAC: "aa:bb:cc:00:00:12", UseFixedIP: true, FixedIP: "192.168.1.12", NetworkID: fakerouter.NetworkID,
		LocalDNSRecord: "pxe.home.internal", LocalDNSRecordEnabled: true})
	r.PutClient(fakerouter.Client{MAC: "aa:bb:cc:00:00:37", Hostname: "tv", LastIP: "192.168.1.37"})

	res := drctl(t, r, "leases", "list").ok(t)
	lines := strings.Split(strings.TrimSpace(res.stdout), "\n")
	if len(lines) != 4 || !strings.HasPrefix(lines[1], "192.168.1.6 ") || !strings.HasPrefix(lines[3], "192.168.1.37") {
		t.Fatalf("leases should be sorted by IP:\n%s", res.stdout)
	}
	for _, want := range []string{"23h 41m", "17h 5m", "3d 4h", "yes (pxe.home.internal)", "offline", "Raspberry Pi"} {
		if !strings.Contains(res.stdout, want) {
			t.Fatalf("table missing %q:\n%s", want, res.stdout)
		}
	}
	if !strings.Contains(res.stderr, "3 leases (1 reserved)") {
		t.Fatalf("summary: %q", res.stderr)
	}
	csvOut := drctl(t, r, "leases", "list", "--format", "csv").ok(t).stdout
	if !strings.HasPrefix(csvOut, "ip,mac,name,hostname,vendor,status,connection,expires,reserved,dns_name,network\n192.168.1.6,aa:bb:cc:00:00:06,macbook,macbook,Apple,online,wireless,2026-10-03T20:41:00Z,false,,Default\n") {
		t.Fatalf("csv:\n%s", csvOut)
	}
	drctl(t, r, "leases", "list", "--network", "default").ok(t).says(t, "192.168.1.37")
	drctl(t, r, "leases", "list", "--network", "IoT").fails(t, 1, `no network named "IoT"`)

	// reserve: by IP, by MAC, as a host, dry run, already reserved.
	drctl(t, r, "leases", "reserve", "192.168.1.37", "--name", "Living room TV", "--dry-run").ok(t).says(t, "would create aa:bb:cc:00:00:37 -> 192.168.1.37 (Living room TV)")
	if c, _ := r.Client("aa:bb:cc:00:00:37"); c.UseFixedIP {
		t.Fatal("dry run reserved the device")
	}
	drctl(t, r, "leases", "reserve", "192.168.1.37", "--name", "Living room TV").ok(t).says(t, "created aa:bb:cc:00:00:37 -> 192.168.1.37 (Living room TV)")
	if c, _ := r.Client("aa:bb:cc:00:00:37"); !c.UseFixedIP || c.FixedIP != "192.168.1.37" || c.Name != "Living room TV" || c.NetworkID != fakerouter.NetworkID {
		t.Fatalf("stored %+v", c)
	}
	drctl(t, r, "leases", "reserve", "AA-BB-CC-00-00-37").ok(t).says(t, "unchanged aa:bb:cc:00:00:37 -> 192.168.1.37")
	drctl(t, r, "leases", "list").ok(t).says(t, "192.168.1.37  aa:bb:cc:00:00:37")
	drctl(t, r, "leases", "reserve", "aa:bb:cc:00:00:06", "--dns-name", "macbook.home.internal").ok(t).
		says(t, "created host macbook.home.internal -> 192.168.1.6 (aa:bb:cc:00:00:06)")
	if c, _ := r.Client("aa:bb:cc:00:00:06"); !c.LocalDNSRecordEnabled || c.LocalDNSRecord != "macbook.home.internal" {
		t.Fatalf("host: %+v", c)
	}
	drctl(t, r, "leases", "list").ok(t).says(t, "yes (macbook.home.internal)")

	drctl(t, r, "leases", "reserve", "192.168.1.99").fails(t, 1, `no current DHCP lease for 192.168.1.99`)
	drctl(t, r, "leases", "reserve", "aa:bb:cc:00:00:99").fails(t, 1, `no current DHCP lease for aa:bb:cc:00:00:99`)
	drctl(t, r, "leases", "reserve", "nonsense").fails(t, 2, `neither an IPv4 address nor a MAC address`)
	drctl(t, r, "leases", "reserve", "192.168.1.12", "--dns-name", "a b").fails(t, 1, "must not be empty or contain whitespace")
	// 13 of the commands above log in (the last two are rejected first);
	// reserve must not log in a second time for its dhcp/host add step.
	if n := r.LoginCount(); n != 13 {
		t.Fatalf("%d logins for 13 commands; want exactly one each", n)
	}
}
