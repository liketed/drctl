// Command dream_router7_dns_manager creates, updates or deletes local DNS A
// records on a UniFi gateway.
//
// Usage:
//
//	dream_router7_dns_manager [USER [PASSWORD]] HOSTNAME IP [--host 192.168.1.1] [--site default]
//	dream_router7_dns_manager --delete [USER [PASSWORD]] HOSTNAME
//	dream_router7_dns_manager --csv FILE [USER [PASSWORD]]
//	dream_router7_dns_manager --delete --csv FILE [USER [PASSWORD]]
//
// --csv FILE adds or updates every "hostname,ip" line in FILE with a single login.
// With --delete it deletes every hostname in FILE instead; the IP column is then
// optional, and when present the record is only deleted if it still has that IP.
// --dry-run shows what would change without changing anything.
//
// If USER is omitted it is read from the UNIFI_USER env var, defaulting to "admin".
// If PASSWORD is omitted it is read from the UNIFI_PASS env var, otherwise prompted.
//
// USER/PASSWORD must be a *local* UniFi OS admin account (not a UI.com SSO/2FA
// account).
package main

import (
	"bytes"
	"crypto/tls"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/term"
)

type record = map[string]any

type unifi struct {
	base   string
	dnsURL string
	csrf   string
	client *http.Client
}

func newUniFi(host, site string) *unifi {
	jar, _ := cookiejar.New(nil)
	base := "https://" + host
	return &unifi{
		base:   base,
		dnsURL: base + "/proxy/network/v2/api/site/" + site + "/static-dns",
		client: &http.Client{
			Jar:     jar,
			Timeout: 15 * time.Second,
			// The gateway uses a self-signed certificate.
			Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}},
		},
	}
}

func (u *unifi) request(method, url string, body any, out any) {
	var data io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			fatal("encoding request: %v", err)
		}
		data = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, url, data)
	if err != nil {
		fatal("%s %s failed: %v", method, url, err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if u.csrf != "" {
		req.Header.Set("X-CSRF-Token", u.csrf)
	}
	resp, err := u.client.Do(req)
	if err != nil {
		fatal("%s %s failed: %v", method, url, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		fatal("%s %s failed: HTTP %d %s", method, url, resp.StatusCode, raw)
	}
	if t := resp.Header.Get("X-Updated-CSRF-Token"); t != "" {
		u.csrf = t
	} else if t := resp.Header.Get("X-CSRF-Token"); t != "" {
		u.csrf = t
	}
	if out != nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, out); err != nil {
			fatal("%s %s: unexpected response: %v", method, url, err)
		}
	}
}

func (u *unifi) login(username, password string) {
	u.request("POST", u.base+"/api/auth/login",
		map[string]any{"username": username, "password": password, "rememberMe": false}, nil)
}

func (u *unifi) listRecords() []record {
	var records []record
	u.request("GET", u.dnsURL, nil, &records)
	return records
}

func (u *unifi) create(r record)            { u.request("POST", u.dnsURL, r, nil) }
func (u *unifi) update(id string, r record) { u.request("PUT", u.dnsURL+"/"+id, r, nil) }
func (u *unifi) delete(id string)           { u.request("DELETE", u.dnsURL+"/"+id, nil, nil) }

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}

var prog = filepath.Base(os.Args[0])

func usage() string {
	return fmt.Sprintf("usage: %[1]s [-h] [--host HOST] [--site SITE] [--dry-run] [USER [PASSWORD]] HOSTNAME IP\n"+
		"       %[1]s [-h] [--host HOST] [--site SITE] [--dry-run] --delete [USER [PASSWORD]] HOSTNAME\n"+
		"       %[1]s [-h] [--host HOST] [--site SITE] [--dry-run] [--delete] --csv FILE [USER [PASSWORD]]\n", prog)
}

func usageError(msg string) {
	fmt.Fprint(os.Stderr, usage())
	fmt.Fprintf(os.Stderr, "%s: error: %s\n", prog, msg)
	os.Exit(2)
}

type options struct {
	host, site, csv string
	delete, dryRun  bool
	haveCSV         bool
	positional      []string
}

// parseArgs accepts flags anywhere on the command line, like Python's argparse.
func parseArgs(args []string) options {
	o := options{host: "192.168.1.1", site: "default"}
	for i := 0; i < len(args); i++ {
		a := args[i]
		name, value, hasValue := strings.Cut(a, "=")
		switch name {
		case "-h", "--help":
			fmt.Print(usage())
			fmt.Print(`
Create, update or delete local DNS A records on a UniFi gateway.

If USER is omitted it is read from the UNIFI_USER env var, defaulting to "admin".
If PASSWORD is omitted it is read from the UNIFI_PASS env var, otherwise prompted.

USER/PASSWORD must be a *local* UniFi OS admin account (not a UI.com SSO/2FA account).

options:
  -h, --help   show this help message and exit
  --delete     delete the A record for HOSTNAME instead of creating it
  --csv FILE   add or update every "hostname,ip" line in FILE with a single login
               (with --delete: delete every hostname in FILE)
  --dry-run    show what would change without changing anything
  --host HOST  router address (default 192.168.1.1)
  --site SITE  UniFi Network site name (default "default")
`)
			os.Exit(0)
		case "--delete":
			o.delete = true
		case "--dry-run":
			o.dryRun = true
		case "--host", "--site", "--csv":
			if !hasValue {
				if i+1 >= len(args) {
					usageError("argument " + name + ": expected one argument")
				}
				i++
				value = args[i]
			}
			switch name {
			case "--host":
				o.host = value
			case "--site":
				o.site = value
			default:
				o.csv, o.haveCSV = value, true
			}
		case "--":
			o.positional = append(o.positional, args[i+1:]...)
			return o
		default:
			if strings.HasPrefix(a, "-") && len(a) > 1 {
				usageError("unrecognized arguments: " + a)
			}
			o.positional = append(o.positional, a)
		}
	}
	return o
}

func readPassword(user string) string {
	if p := os.Getenv("UNIFI_PASS"); p != "" {
		return p
	}
	fmt.Fprintf(os.Stderr, "Password for %s: ", user)
	b, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(os.Stderr)
	if err != nil {
		fatal("reading password: %v", err)
	}
	return string(b)
}

func parseIP(value, prefix string) string {
	addr, err := netip.ParseAddr(strings.TrimSpace(value))
	if err != nil || !addr.Is4() {
		fatal("%sinvalid IPv4 address '%s'", prefix, value)
	}
	return addr.String()
}

func normalizeName(hostname string) string {
	return strings.TrimSuffix(strings.TrimSpace(hostname), ".")
}

// entry is one CSV line; ip is "" when the IP column is absent (ipOptional only).
type entry struct{ name, ip string }

// readCSV returns the "hostname,ip" entries in path, exiting on the first bad line.
// With ipOptional, lines may be just "hostname".
func readCSV(path string, ipOptional bool) []entry {
	f, err := os.Open(path)
	if err != nil {
		fatal("cannot read %s: %v", path, errors.Unwrap(err))
	}
	defer f.Close()
	r := csv.NewReader(f)
	r.FieldsPerRecord = -1
	r.TrimLeadingSpace = true
	r.LazyQuotes = true
	var entries []entry
	seen := map[string]bool{}
	for {
		row, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			fatal("%s: %v", path, err)
		}
		line, _ := r.FieldPos(0)
		if strings.TrimSpace(strings.Join(row, "")) == "" || strings.HasPrefix(strings.TrimSpace(row[0]), "#") {
			continue
		}
		prefix := fmt.Sprintf("%s:%d: ", path, line)
		if len(row) != 2 && !(ipOptional && len(row) == 1) {
			expected := "hostname,ip"
			if ipOptional {
				expected = "hostname[,ip]"
			}
			fatal("%sexpected '%s', got '%s'", prefix, expected, strings.Join(row, ","))
		}
		name := normalizeName(row[0])
		if name == "" {
			fatal("%smissing hostname", prefix)
		}
		var ip string
		if !ipOptional || (len(row) == 2 && strings.TrimSpace(row[1]) != "") {
			ip = parseIP(row[1], prefix)
		}
		if seen[name] {
			fatal("%sduplicate hostname %s", prefix, name)
		}
		seen[name] = true
		entries = append(entries, entry{name, ip})
	}
	if len(entries) == 0 {
		fatal("%s: no records found", path)
	}
	return entries
}

func findRecord(records []record, name string) record {
	for _, r := range records {
		if r["record_type"] == "A" && r["key"] == name {
			return r
		}
	}
	return nil
}

// upsert creates or updates the A record for name and returns "created", "updated" or "unchanged".
func upsert(api *unifi, records []record, name, ip string, dryRun bool) string {
	current := findRecord(records, name)
	rec := record{"key": name, "record_type": "A", "value": ip, "enabled": true,
		"ttl": 0, "port": 0, "priority": 0, "weight": 0}
	enabled, ok := current["enabled"].(bool)
	switch {
	case current == nil:
		if dryRun {
			fmt.Printf("would create %s -> %s\n", name, ip)
		} else {
			api.create(rec)
			fmt.Printf("created %s -> %s\n", name, ip)
		}
		return "created"
	case current["value"] != ip || (ok && !enabled):
		if dryRun {
			fmt.Printf("would update %s -> %s (was %v)\n", name, ip, current["value"])
		} else {
			merged := record{}
			for k, v := range current {
				merged[k] = v
			}
			for k, v := range rec {
				merged[k] = v
			}
			api.update(fmt.Sprint(current["_id"]), merged)
			fmt.Printf("updated %s -> %s (was %v)\n", name, ip, current["value"])
		}
		return "updated"
	default:
		fmt.Printf("unchanged %s -> %s\n", name, ip)
		return "unchanged"
	}
}

// deleteRecord deletes the A record for name and returns "deleted", "not found" or "skipped".
// If expectedIP is set, the record is only deleted while it still has that IP.
func deleteRecord(api *unifi, records []record, name, expectedIP string, dryRun, missingOK bool) string {
	current := findRecord(records, name)
	if current == nil {
		if !missingOK {
			fatal("no A record found for %s", name)
		}
		fmt.Printf("not found %s\n", name)
		return "not found"
	}
	if expectedIP != "" && current["value"] != expectedIP {
		fmt.Printf("skipped %s (is %v, file says %s)\n", name, current["value"], expectedIP)
		return "skipped"
	}
	if dryRun {
		fmt.Printf("would delete %s (was %v)\n", name, current["value"])
	} else {
		api.delete(fmt.Sprint(current["_id"]))
		fmt.Printf("deleted %s (was %v)\n", name, current["value"])
	}
	return "deleted"
}

func main() {
	o := parseArgs(os.Args[1:])

	positional := o.positional
	var batch []entry
	var name, ip string
	if o.haveCSV {
		if len(positional) > 2 {
			if o.delete {
				usageError("expected --delete --csv FILE [USER [PASSWORD]]")
			}
			usageError("expected --csv FILE [USER [PASSWORD]]")
		}
		batch = readCSV(o.csv, o.delete)
	} else {
		// The last one (or two) positionals are HOSTNAME [IP]; anything before is USER [PASSWORD].
		var ipArg string
		if !o.delete && len(positional) >= 2 {
			ipArg, positional = positional[len(positional)-1], positional[:len(positional)-1]
		}
		if len(positional) == 0 || len(positional) > 3 || (ipArg == "" && !o.delete) {
			if o.delete {
				usageError("expected --delete [USER [PASSWORD]] HOSTNAME")
			}
			usageError("expected [USER [PASSWORD]] HOSTNAME IP")
		}
		name, positional = normalizeName(positional[len(positional)-1]), positional[:len(positional)-1]
		if !o.delete {
			ip = parseIP(ipArg, "")
		}
	}
	user := os.Getenv("UNIFI_USER")
	if user == "" {
		user = "admin"
	}
	if len(positional) >= 1 {
		user = positional[0]
	}
	var password string
	if len(positional) == 2 {
		password = positional[1]
	} else {
		password = readPassword(user)
	}

	api := newUniFi(o.host, o.site)
	api.login(user, password)
	records := api.listRecords()

	if o.haveCSV && o.delete {
		counts := map[string]int{}
		for _, e := range batch {
			counts[deleteRecord(api, records, e.name, e.ip, o.dryRun, true)]++
		}
		verb := "deleted"
		if o.dryRun {
			verb = "dry run: would delete"
		}
		fmt.Printf("\n%s %d, not found %d, skipped %d\n",
			verb, counts["deleted"], counts["not found"], counts["skipped"])
		return
	}

	if o.haveCSV {
		counts := map[string]int{}
		for _, e := range batch {
			counts[upsert(api, records, e.name, e.ip, o.dryRun)]++
		}
		if o.dryRun {
			fmt.Printf("\ndry run: would create %d, would update %d, unchanged %d\n",
				counts["created"], counts["updated"], counts["unchanged"])
		} else {
			fmt.Printf("\ncreated %d, updated %d, unchanged %d\n",
				counts["created"], counts["updated"], counts["unchanged"])
		}
		return
	}

	if o.delete {
		deleteRecord(api, records, name, "", o.dryRun, false)
		return
	}

	upsert(api, records, name, ip, o.dryRun)
}
