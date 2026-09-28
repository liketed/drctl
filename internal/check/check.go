// Package check validates DNS records, MAC addresses and DHCP reservations
// before they are sent to the router, mirroring the router's own rules so
// mistakes are reported clearly and nothing half-applied is left behind.
package check

import (
	"fmt"
	"net"
	"net/netip"
	"regexp"
	"strings"

	"github.com/liketed/drctl/internal/unifi"
)

// RecordTypes are the static DNS record types the router supports.
var RecordTypes = []string{"A", "AAAA", "CNAME", "MX", "NS", "SRV", "TXT"}

// Which numeric fields each type may set to a non-zero value; the router
// rejects others.
var allowedFields = map[string]map[string]bool{
	"A":     {"ttl": true},
	"AAAA":  {"ttl": true},
	"CNAME": {"ttl": true},
	"MX":    {"priority": true},
	"NS":    {},
	"SRV":   {"priority": true, "weight": true, "port": true},
	"TXT":   {},
}

var (
	// Names end up in dnsmasq config lines such as host-record=NAME,IP, so
	// whitespace and commas would corrupt them.
	namePattern = regexp.MustCompile(`^[^\s,]+$`)
	srvPattern  = regexp.MustCompile(`^_[^.\s,]+\._[^.\s,]+\.[^\s,]+$`)
)

// NormalizeType upper-cases a record type and checks it is supported.
func NormalizeType(t string) (string, error) {
	t = strings.ToUpper(strings.TrimSpace(t))
	for _, rt := range RecordTypes {
		if t == rt {
			return t, nil
		}
	}
	return "", fmt.Errorf("unsupported record type %q (supported: %s)", t, strings.Join(RecordTypes, ", "))
}

// NormalizeName trims whitespace and a trailing dot from a DNS name.
func NormalizeName(name string) string {
	return strings.TrimSuffix(strings.TrimSpace(name), ".")
}

// Name checks a DNS name.
func Name(name string) error {
	if !namePattern.MatchString(name) {
		return fmt.Errorf("invalid name %q: must not be empty or contain whitespace or commas", name)
	}
	return nil
}

// DNSRecord checks a record against the router's rules for its type. It
// normalises the value where that is safe (e.g. IP address formatting).
func DNSRecord(r *unifi.DNSRecord) error {
	t, err := NormalizeType(r.RecordType)
	if err != nil {
		return err
	}
	r.RecordType = t
	r.Key = NormalizeName(r.Key)
	if err := Name(r.Key); err != nil {
		return err
	}
	for field, v := range map[string]int64{"ttl": r.TTL, "priority": r.Priority, "weight": r.Weight, "port": r.Port} {
		if v < 0 || (field != "ttl" && v > 65535) {
			return fmt.Errorf("%s %d is out of range", field, v)
		}
		if v != 0 && !allowedFields[t][field] {
			return fmt.Errorf("%s cannot be set for %s records (only %s)", field, t, describeAllowed(field))
		}
	}
	if t == "SRV" && !srvPattern.MatchString(r.Key) {
		return fmt.Errorf(`SRV records need a name of the form "_service._protocol.domain", e.g. "_sip._tcp.home.internal"`)
	}
	v := strings.TrimSpace(r.Value)
	switch t {
	case "A":
		a, err := netip.ParseAddr(v)
		if err != nil || !a.Is4() {
			return fmt.Errorf("invalid IPv4 address %q for A record", r.Value)
		}
		v = a.String()
	case "AAAA":
		a, err := netip.ParseAddr(v)
		if err != nil || !a.Is6() || a.Is4In6() {
			return fmt.Errorf("invalid IPv6 address %q for AAAA record", r.Value)
		}
		v = a.String()
	case "NS":
		a, err := netip.ParseAddr(v)
		if err != nil {
			return fmt.Errorf("NS value %q must be the IP address of the DNS server to forward %s to "+
				"(the router implements NS records as conditional forwarders)", r.Value, r.Key)
		}
		v = a.String()
	case "CNAME", "MX", "SRV":
		v = NormalizeName(v)
		if !namePattern.MatchString(v) {
			return fmt.Errorf("%s value %q must be a hostname (no whitespace or commas)", t, r.Value)
		}
		if t == "CNAME" && strings.EqualFold(v, r.Key) {
			return fmt.Errorf("a CNAME cannot point to itself")
		}
	case "TXT":
		v = r.Value // TXT text is kept exactly as given
		if err := txt(v); err != nil {
			return err
		}
	}
	r.Value = v
	return nil
}

// txt mirrors the router's rules: double quotes only around the whole value,
// and each line at most 255 characters.
func txt(v string) error {
	if v == "" {
		return fmt.Errorf("TXT value must not be empty")
	}
	inner := v
	if len(v) >= 2 && strings.HasPrefix(v, `"`) && strings.HasSuffix(v, `"`) {
		inner = v[1 : len(v)-1]
	}
	if strings.Contains(inner, `"`) {
		return fmt.Errorf(`TXT value: double quotes are only allowed around the whole value, e.g. "\"hello world\""`)
	}
	for _, line := range strings.Split(v, "\n") {
		if len(line) > 255 {
			return fmt.Errorf("TXT value: each line must be at most 255 characters")
		}
	}
	return nil
}

func describeAllowed(field string) string {
	var types []string
	for _, t := range RecordTypes {
		if allowedFields[t][field] {
			types = append(types, t)
		}
	}
	return strings.Join(types, ", ") + " records"
}

// MAC normalises a MAC address to lower-case colon form (aa:bb:cc:dd:ee:ff),
// accepting colons, dashes or dots as separators.
func MAC(s string) (string, error) {
	hw, err := net.ParseMAC(strings.TrimSpace(s))
	if err != nil || len(hw) != 6 {
		return "", fmt.Errorf("invalid MAC address %q", s)
	}
	return hw.String(), nil
}

// IPv4 parses an IPv4 address.
func IPv4(s string) (netip.Addr, error) {
	a, err := netip.ParseAddr(strings.TrimSpace(s))
	if err != nil || !a.Is4() {
		return netip.Addr{}, fmt.Errorf("invalid IPv4 address %q", s)
	}
	return a, nil
}

// NetworkFor picks the network a reservation belongs to: the one named (if
// name is set) or the one whose subnet contains ip. It checks that ip is a
// usable host address in that subnet.
func NetworkFor(networks []unifi.Network, ip netip.Addr, name string) (unifi.Network, error) {
	var candidates []unifi.Network
	for _, n := range networks {
		if n.Subnet == "" {
			continue
		}
		if name != "" && !strings.EqualFold(n.Name, name) && n.ID != name {
			continue
		}
		candidates = append(candidates, n)
	}
	if name != "" && len(candidates) == 0 {
		return unifi.Network{}, fmt.Errorf("no network named %q (networks: %s)", name, networkNames(networks))
	}
	for _, n := range candidates {
		prefix, err := netip.ParsePrefix(n.Subnet)
		if err != nil || !prefix.Masked().Contains(ip) {
			continue
		}
		gateway := prefix.Addr()
		p := prefix.Masked()
		if ip == p.Addr() || ip == broadcast(p) {
			return unifi.Network{}, fmt.Errorf("%s is the network or broadcast address of %s", ip, p)
		}
		if ip == gateway {
			return unifi.Network{}, fmt.Errorf("%s is the router's own address on network %q", ip, n.Name)
		}
		return n, nil
	}
	if name != "" {
		return unifi.Network{}, fmt.Errorf("%s is not in network %q (%s)", ip, candidates[0].Name, candidates[0].Subnet)
	}
	return unifi.Network{}, fmt.Errorf("%s is not in any network's subnet (networks: %s)", ip, networkNames(networks))
}

func broadcast(p netip.Prefix) netip.Addr {
	a := p.Addr().As4()
	hostBits := 32 - p.Bits()
	v := uint32(a[0])<<24 | uint32(a[1])<<16 | uint32(a[2])<<8 | uint32(a[3])
	v |= (1 << hostBits) - 1
	return netip.AddrFrom4([4]byte{byte(v >> 24), byte(v >> 16), byte(v >> 8), byte(v)})
}

func networkNames(networks []unifi.Network) string {
	var names []string
	for _, n := range networks {
		if n.Subnet != "" {
			names = append(names, fmt.Sprintf("%s %s", n.Name, n.Subnet))
		}
	}
	if len(names) == 0 {
		return "none"
	}
	return strings.Join(names, ", ")
}
