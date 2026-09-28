package cli

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/liketed/dreamrouter-go/check"
	"github.com/liketed/dreamrouter-go/unifi"
)

var dnsCSVHeader = []string{"type", "name", "value", "ttl", "priority", "weight", "port", "enabled"}

func dnsRow(r unifi.DNSRecord) []string {
	return []string{r.RecordType, r.Key, r.Value, itoa(r.TTL), itoa(r.Priority), itoa(r.Weight), itoa(r.Port),
		strconv.FormatBool(r.Enabled)}
}

func itoa(v int64) string { return strconv.FormatInt(v, 10) }

func sortDNS(records []unifi.DNSRecord) {
	sort.SliceStable(records, func(i, j int) bool {
		a, b := records[i], records[j]
		if a.Key != b.Key {
			return a.Key < b.Key
		}
		if a.RecordType != b.RecordType {
			return a.RecordType < b.RecordType
		}
		return a.Value < b.Value
	})
}

func describeDNS(r unifi.DNSRecord) string {
	s := fmt.Sprintf("%s %s -> %s", r.RecordType, r.Key, r.Value)
	var extra []string
	for _, f := range []struct {
		name string
		v    int64
	}{{"ttl", r.TTL}, {"priority", r.Priority}, {"weight", r.Weight}, {"port", r.Port}} {
		if f.v != 0 {
			extra = append(extra, fmt.Sprintf("%s %d", f.name, f.v))
		}
	}
	if !r.Enabled {
		extra = append(extra, "disabled")
	}
	if len(extra) > 0 {
		s += " (" + strings.Join(extra, ", ") + ")"
	}
	return s
}

// sameSettings reports whether two records match apart from their ID.
func sameSettings(a, b unifi.DNSRecord) bool {
	a.ID, b.ID = "", ""
	return a == b
}

// dnsEntry is one line of "dns list": a static record, or a device's DNS
// name (set with "drctl host"), which the router serves like an A record.
type dnsEntry struct {
	rec    unifi.DNSRecord
	host   bool
	mac    string
	device string
}

func dnsList(ctx context.Context, c *command) error {
	fs := c.flags("[--type T] [--name N] [--static] [--format table|csv|json]")
	typ := fs.String("type", "", "only records of this type")
	name := fs.String("name", "", "only records with exactly this name")
	static := fs.Bool("static", false, "only static records, not devices' DNS names (drctl host)")
	format := fs.String("format", "table", "output format: table, csv or json (csv lists static records only, for import)")
	if _, err := c.parse(0, 0); err != nil {
		return err
	}
	if *typ != "" {
		t, err := check.NormalizeType(*typ)
		if err != nil {
			return usageError{err.Error()}
		}
		*typ = t
	}
	api, err := c.connect()
	if err != nil {
		return err
	}
	records, err := api.ListDNS(ctx)
	if err != nil {
		return err
	}
	var entries []dnsEntry
	for _, r := range records {
		entries = append(entries, dnsEntry{rec: r})
	}
	// Device DNS names aren't static records, so the CSV (which is meant to be
	// re-imported with "dns import") leaves them out.
	var hostsOmitted int
	if !*static {
		clients, err := api.ListClients(ctx)
		if err != nil {
			return err
		}
		for _, d := range clients {
			if !(d.UseFixedIP && d.LocalDNSRecordEnabled && d.LocalDNSRecord != "") {
				continue
			}
			if *format == "csv" {
				hostsOmitted++
				continue
			}
			entries = append(entries, dnsEntry{
				rec:  unifi.DNSRecord{RecordType: "A", Key: d.LocalDNSRecord, Value: d.FixedIP, Enabled: true},
				host: true, mac: d.MAC, device: d.DisplayName(),
			})
		}
	}
	sort.SliceStable(entries, func(i, j int) bool {
		a, b := entries[i].rec, entries[j].rec
		if a.Key != b.Key {
			return a.Key < b.Key
		}
		if a.RecordType != b.RecordType {
			return a.RecordType < b.RecordType
		}
		return a.Value < b.Value
	})
	var shown []dnsEntry
	for _, e := range entries {
		if (*typ == "" || e.rec.RecordType == *typ) && (*name == "" || e.rec.Key == check.NormalizeName(*name)) {
			shown = append(shown, e)
		}
	}
	switch *format {
	case "table":
		// A compact table: numeric fields only where set.
		var table [][]string
		for _, e := range shown {
			var details []string
			if e.host {
				label := e.mac
				if e.device != "" {
					label += ", " + e.device
				}
				details = append(details, "host ("+label+")")
			}
			for _, f := range []struct {
				name string
				v    int64
			}{{"ttl", e.rec.TTL}, {"priority", e.rec.Priority}, {"weight", e.rec.Weight}, {"port", e.rec.Port}} {
				if f.v != 0 {
					details = append(details, fmt.Sprintf("%s=%d", f.name, f.v))
				}
			}
			if !e.rec.Enabled {
				details = append(details, "disabled")
			}
			table = append(table, []string{e.rec.RecordType, e.rec.Key, e.rec.Value, strings.Join(details, " ")})
		}
		return output(c.env.Stdout, "table", []string{"type", "name", "value", "details"}, table)
	case "json":
		header := append(append([]string{}, dnsCSVHeader...), "source", "mac")
		var rows [][]string
		for _, e := range shown {
			source := "static"
			if e.host {
				source = "host"
			}
			rows = append(rows, append(dnsRow(e.rec), source, e.mac))
		}
		return output(c.env.Stdout, "json", header, rows)
	}
	var rows [][]string
	for _, e := range shown {
		rows = append(rows, dnsRow(e.rec))
	}
	if err := output(c.env.Stdout, *format, dnsCSVHeader, rows); err != nil {
		return err
	}
	if hostsOmitted > 0 {
		fmt.Fprintf(c.env.Stderr, "drctl: note: %d device DNS name(s) not included in the CSV; back them up with \"drctl host list --format csv\"\n", hostsOmitted)
	}
	return nil
}

// dnsFlags are the record settings shared by "dns add".
type dnsFlags struct {
	typ                         *string
	ttl, priority, weight, port *int64
	disabled                    *bool
}

func (c *command) dnsRecordFlags() dnsFlags {
	return dnsFlags{
		typ:      c.fs.String("type", "A", "record type: A, AAAA, CNAME, MX, NS, SRV or TXT"),
		ttl:      c.fs.Int64("ttl", 0, "TTL in seconds for A, AAAA and CNAME records (0 = automatic)"),
		priority: c.fs.Int64("priority", 0, "priority for MX and SRV records"),
		weight:   c.fs.Int64("weight", 0, "weight for SRV records"),
		port:     c.fs.Int64("port", 0, "port for SRV records"),
		disabled: c.fs.Bool("disabled", false, "create the record disabled (kept but not served)"),
	}
}

func dnsAdd(ctx context.Context, c *command) error {
	c.flags("NAME VALUE [--type A] [--ttl N] [--priority N] [--weight N] [--port N] [--disabled] [--append]")
	f := c.dnsRecordFlags()
	appendRec := c.fs.Bool("append", false, "add another record even if NAME already has a record of this type "+
		"(e.g. a second A record for round robin, or a backup MX)")
	args, err := c.parse(2, 2)
	if err != nil {
		return err
	}
	rec := unifi.DNSRecord{RecordType: *f.typ, Key: args[0], Value: args[1], Enabled: !*f.disabled,
		TTL: *f.ttl, Priority: *f.priority, Weight: *f.weight, Port: *f.port}
	if err := check.DNSRecord(&rec); err != nil {
		return err
	}
	api, err := c.connect()
	if err != nil {
		return err
	}
	records, err := api.ListDNS(ctx)
	if err != nil {
		return err
	}
	if err := c.checkNotHostName(ctx, api, rec.Key); err != nil {
		return err
	}
	_, err = c.upsertDNS(ctx, api, records, rec, *appendRec)
	return err
}

// checkNotHostName fails if name is already a device's DNS name ("drctl host").
func (c *command) checkNotHostName(ctx context.Context, api *unifi.Client, name string) error {
	clients, err := api.ListClients(ctx)
	if err != nil {
		return err
	}
	for _, d := range clients {
		if d.UseFixedIP && d.LocalDNSRecordEnabled && strings.EqualFold(d.LocalDNSRecord, name) {
			return fmt.Errorf("%s is already the DNS name of device %s (%s, %s); see \"drctl host list\"",
				name, d.DisplayName(), d.MAC, d.FixedIP)
		}
	}
	return nil
}

// upsertDNS creates rec, or updates the existing record of the same type and
// name: unchanged if identical, updated if there is exactly one with another
// value. With appendRec, a record with a new value is always added. It
// returns "created", "updated" or "unchanged".
func (c *command) upsertDNS(ctx context.Context, api *unifi.Client, records []unifi.DNSRecord, rec unifi.DNSRecord, appendRec bool) (string, error) {
	var same []unifi.DNSRecord
	for _, r := range records {
		if r.RecordType == rec.RecordType && r.Key == rec.Key {
			same = append(same, r)
		}
	}
	for _, r := range same {
		if r.Value == rec.Value {
			rec.ID = r.ID
			if sameSettings(r, rec) {
				c.out("unchanged %s", describeDNS(rec))
				return "unchanged", nil
			}
			return "updated", c.updateDNS(ctx, api, rec, r)
		}
	}
	if len(same) == 0 || appendRec {
		if !c.dryRun {
			if _, err := api.CreateDNS(ctx, rec); err != nil {
				return "", err
			}
		}
		c.out("%s %s", c.verb("created", "create"), describeDNS(rec))
		return "created", nil
	}
	if len(same) > 1 {
		var values []string
		for _, r := range same {
			values = append(values, r.Value)
		}
		return "", fmt.Errorf("%s already has %d %s records (%s); use --append to add another, "+
			"or \"drctl dns delete\" first", rec.Key, len(same), rec.RecordType, strings.Join(values, ", "))
	}
	rec.ID = same[0].ID
	return "updated", c.updateDNS(ctx, api, rec, same[0])
}

func (c *command) updateDNS(ctx context.Context, api *unifi.Client, rec, old unifi.DNSRecord) error {
	if !c.dryRun {
		if _, err := api.UpdateDNS(ctx, rec); err != nil {
			return err
		}
	}
	c.out("%s %s (was %s)", c.verb("updated", "update"), describeDNS(rec), strings.TrimPrefix(describeDNS(old), old.RecordType+" "+old.Key+" -> "))
	return nil
}

func dnsDelete(ctx context.Context, c *command) error {
	fs := c.flags("NAME [--type T] [--value V] [--all]")
	typ := fs.String("type", "", "only delete records of this type")
	value := fs.String("value", "", "only delete the record with this value")
	all := fs.Bool("all", false, "delete every matching record when several match")
	args, err := c.parse(1, 1)
	if err != nil {
		return err
	}
	if *typ != "" {
		t, err := check.NormalizeType(*typ)
		if err != nil {
			return usageError{err.Error()}
		}
		*typ = t
	}
	name := check.NormalizeName(args[0])
	api, err := c.connect()
	if err != nil {
		return err
	}
	records, err := api.ListDNS(ctx)
	if err != nil {
		return err
	}
	var matches []unifi.DNSRecord
	for _, r := range records {
		if r.Key == name && (*typ == "" || r.RecordType == *typ) && (*value == "" || r.Value == *value) {
			matches = append(matches, r)
		}
	}
	sortDNS(matches)
	switch {
	case len(matches) == 0:
		if *typ == "" || *typ == "A" {
			clients, err := api.ListClients(ctx)
			if err != nil {
				return err
			}
			for _, d := range clients {
				if d.UseFixedIP && d.LocalDNSRecordEnabled && strings.EqualFold(d.LocalDNSRecord, name) {
					device := d.MAC + ", " + d.FixedIP
					if n := d.DisplayName(); n != "" {
						device = n + " (" + device + ")"
					}
					return fmt.Errorf("%s is the DNS name of device %s, not a static record; "+
						"remove it with \"drctl host delete %s\"", name, device, d.LocalDNSRecord)
				}
			}
		}
		return fmt.Errorf("no record found for %s", describeFilter(name, *typ, *value))
	case len(matches) > 1 && !*all:
		var list []string
		for _, m := range matches {
			list = append(list, "  "+describeDNS(m))
		}
		return fmt.Errorf("%d records match %s; add --type or --value to pick one, or --all to delete them all:\n%s",
			len(matches), describeFilter(name, *typ, *value), strings.Join(list, "\n"))
	}
	for _, m := range matches {
		if !c.dryRun {
			if err := api.DeleteDNS(ctx, m.ID); err != nil {
				return err
			}
		}
		c.out("%s %s", c.verb("deleted", "delete"), describeDNS(m))
	}
	return nil
}

func describeFilter(name, typ, value string) string {
	s := name
	if typ != "" {
		s = typ + " " + s
	}
	if value != "" {
		s += " -> " + value
	}
	return s
}

// dnsImport adds/updates (or with --delete, deletes) every record in a CSV
// file with a single login. Two formats are accepted:
//
//	nas.home.internal,192.168.1.50                 name,ip  (A records)
//	type,name,value,ttl,priority,weight,port,enabled   header row, any type
//	                                                   (the output of "dns list --format csv")
//
// For --delete the value column is optional; when given, a record is only
// deleted if it still has that value.
func dnsImport(ctx context.Context, c *command) error {
	fs := c.flags("FILE [--delete]   (FILE may be - for stdin)")
	del := fs.Bool("delete", false, "delete the records in FILE instead of adding them")
	args, err := c.parse(1, 1)
	if err != nil {
		return err
	}
	path := args[0]
	header, lines, err := readCSV(path, c.env.stdin(), "type")
	if err != nil {
		return err
	}
	var batch []unifi.DNSRecord
	seen := map[string]bool{}
	for _, l := range lines {
		rec, err := parseDNSLine(header, l, *del)
		if err != nil {
			return lineErr(path, l, "%v", err)
		}
		key := rec.RecordType + " " + rec.Key + " " + rec.Value
		if seen[key] {
			return lineErr(path, l, "duplicate record %s", strings.TrimSpace(describeFilter(rec.Key, rec.RecordType, rec.Value)))
		}
		seen[key] = true
		batch = append(batch, rec)
	}
	api, err := c.connect()
	if err != nil {
		return err
	}
	records, err := api.ListDNS(ctx)
	if err != nil {
		return err
	}
	if *del {
		return c.importDNSDelete(ctx, api, records, batch)
	}
	clients, err := api.ListClients(ctx)
	if err != nil {
		return err
	}
	for _, rec := range batch {
		for _, d := range clients {
			if d.UseFixedIP && d.LocalDNSRecordEnabled && strings.EqualFold(d.LocalDNSRecord, rec.Key) {
				return fmt.Errorf("%s is already the DNS name of device %s (%s); nothing was changed", rec.Key, d.DisplayName(), d.MAC)
			}
		}
	}
	// Records of the same type and name in the file are added side by side
	// (round robin, several MX); a single record replaces a single existing one.
	perName := map[string]int{}
	for _, rec := range batch {
		perName[rec.RecordType+" "+rec.Key]++
	}
	counts := map[string]int{}
	for _, rec := range batch {
		action, err := c.upsertDNS(ctx, api, records, rec, perName[rec.RecordType+" "+rec.Key] > 1)
		if err != nil {
			return fmt.Errorf("%s: %w (records before this one were applied)", describeDNS(rec), err)
		}
		counts[action]++
	}
	c.summary(counts, "created", "updated", "unchanged")
	return nil
}

func (c *command) importDNSDelete(ctx context.Context, api *unifi.Client, records []unifi.DNSRecord, batch []unifi.DNSRecord) error {
	counts := map[string]int{}
	for _, want := range batch {
		var matches []unifi.DNSRecord
		for _, r := range records {
			if r.Key == want.Key && r.RecordType == want.RecordType {
				matches = append(matches, r)
			}
		}
		label := strings.TrimSpace(describeFilter(want.Key, want.RecordType, want.Value))
		var target *unifi.DNSRecord
		switch {
		case len(matches) == 0:
			c.out("not found %s", label)
			counts["not found"]++
			continue
		case want.Value != "":
			for i := range matches {
				if matches[i].Value == want.Value {
					target = &matches[i]
				}
			}
			if target == nil {
				var values []string
				for _, m := range matches {
					values = append(values, m.Value)
				}
				c.out("skipped %s (is %s)", label, strings.Join(values, ", "))
				counts["skipped"]++
				continue
			}
		case len(matches) > 1:
			c.out("skipped %s (%d records; add the value to pick one)", label, len(matches))
			counts["skipped"]++
			continue
		default:
			target = &matches[0]
		}
		if !c.dryRun {
			if err := api.DeleteDNS(ctx, target.ID); err != nil {
				return err
			}
		}
		c.out("%s %s", c.verb("deleted", "delete"), describeDNS(*target))
		counts["deleted"]++
	}
	c.summary(counts, "deleted", "not found", "skipped")
	return nil
}

func parseDNSLine(header []string, l csvLine, del bool) (unifi.DNSRecord, error) {
	rec := unifi.DNSRecord{Enabled: true}
	if header == nil {
		// Legacy format: name,ip for A records (ip optional with --delete).
		if len(l.fields) > 2 || (len(l.fields) == 1 && !del) {
			if del {
				return rec, fmt.Errorf("expected 'name[,ip]' or a header row starting with 'type', got %q", strings.Join(l.fields, ","))
			}
			return rec, fmt.Errorf("expected 'name,ip' or a header row starting with 'type', got %q", strings.Join(l.fields, ","))
		}
		rec.RecordType, rec.Key = "A", l.fields[0]
		if len(l.fields) == 2 {
			rec.Value = l.fields[1]
		}
	} else {
		rec.RecordType = l.column(header, "type")
		rec.Key = l.column(header, "name")
		rec.Value = l.column(header, "value")
		for _, f := range []struct {
			col string
			dst *int64
		}{{"ttl", &rec.TTL}, {"priority", &rec.Priority}, {"weight", &rec.Weight}, {"port", &rec.Port}} {
			if v := l.column(header, f.col); v != "" {
				n, err := strconv.ParseInt(v, 10, 64)
				if err != nil {
					return rec, fmt.Errorf("invalid %s %q", f.col, v)
				}
				*f.dst = n
			}
		}
		if v := l.column(header, "enabled"); v != "" {
			b, err := strconv.ParseBool(v)
			if err != nil {
				return rec, fmt.Errorf("invalid enabled value %q", v)
			}
			rec.Enabled = b
		}
	}
	if del && rec.Value == "" {
		t, err := check.NormalizeType(rec.RecordType)
		if err != nil {
			return rec, err
		}
		rec.RecordType, rec.Key = t, check.NormalizeName(rec.Key)
		return rec, check.Name(rec.Key)
	}
	return rec, check.DNSRecord(&rec)
}

// summary prints e.g. "created 2, updated 0, unchanged 1" after a blank line.
func (c *command) summary(counts map[string]int, keys ...string) {
	var parts []string
	for _, k := range keys {
		label := k
		if would, ok := map[string]string{"created": "would create", "updated": "would update",
			"deleted": "would delete", "cleared": "would clear"}[k]; ok && c.dryRun {
			label = would
		}
		parts = append(parts, fmt.Sprintf("%s %d", label, counts[k]))
	}
	prefix := ""
	if c.dryRun {
		prefix = "dry run: "
	}
	c.out("\n%s%s", prefix, strings.Join(parts, ", "))
}
