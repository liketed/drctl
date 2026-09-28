// Package fakerouter is an in-memory stand-in for a Dream Router 7's UniFi OS
// login and Network application API, for tests. It mimics behaviour observed
// on a real router: cookie + CSRF sessions, the v2 static DNS API, the classic
// client (DHCP reservation) API with its error codes, and the login limit.
package fakerouter

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
)

const (
	Username  = "admin"
	Password  = "secret"
	NetworkID = "net-default"
)

type DNSRecord struct {
	ID         string `json:"_id"`
	RecordType string `json:"record_type"`
	Key        string `json:"key"`
	Value      string `json:"value"`
	Enabled    bool   `json:"enabled"`
	TTL        int64  `json:"ttl"`
	Priority   int64  `json:"priority"`
	Weight     int64  `json:"weight"`
	Port       int64  `json:"port"`
}

type Client struct {
	ID                    string `json:"_id"`
	MAC                   string `json:"mac"`
	Name                  string `json:"name,omitempty"`
	Hostname              string `json:"hostname,omitempty"`
	UseFixedIP            bool   `json:"use_fixedip"`
	FixedIP               string `json:"fixed_ip,omitempty"`
	NetworkID             string `json:"network_id,omitempty"`
	LocalDNSRecord        string `json:"local_dns_record,omitempty"`
	LocalDNSRecordEnabled bool   `json:"local_dns_record_enabled"`
	LastIP                string `json:"last_ip,omitempty"`
}

type Router struct {
	*httptest.Server

	mu         sync.Mutex
	dns        []DNSRecord
	clients    []Client
	nextID     int
	sessions   map[string]bool
	Logins     int
	LoginLimit int // successful logins allowed in total before 429; 0 = unlimited
}

// New starts a fake router with one network, "Default", 192.168.1.1/24.
func New() *Router {
	r := &Router{sessions: map[string]bool{}}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/auth/login", r.login)
	mux.HandleFunc("/proxy/network/v2/api/site/default/static-dns", r.dnsCollection)
	mux.HandleFunc("/proxy/network/v2/api/site/default/static-dns/", r.dnsItem)
	mux.HandleFunc("/proxy/network/api/s/default/rest/networkconf", r.networks)
	mux.HandleFunc("/proxy/network/api/s/default/rest/user", r.userCollection)
	mux.HandleFunc("/proxy/network/api/s/default/rest/user/", r.userItem)
	mux.HandleFunc("/proxy/network/api/s/default/cmd/stamgr", r.stamgr)
	r.Server = httptest.NewTLSServer(mux)
	return r
}

// Host returns host:port of the server.
func (r *Router) Host() string { return strings.TrimPrefix(r.URL, "https://") }

func (r *Router) id() string { r.nextID++; return fmt.Sprintf("%024x", r.nextID) }

// DNS returns a copy of the static DNS records.
func (r *Router) DNS() []DNSRecord {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]DNSRecord(nil), r.dns...)
}

// Clients returns a copy of the clients.
func (r *Router) Clients() []Client {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]Client(nil), r.clients...)
}

// PutDNS stores a record directly and returns its ID.
func (r *Router) PutDNS(rec DNSRecord) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	rec.ID = r.id()
	r.dns = append(r.dns, rec)
	return rec.ID
}

// PutClient stores a client directly (e.g. a device seen on the network).
func (r *Router) PutClient(c Client) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	c.ID = r.id()
	r.clients = append(r.clients, c)
	return c.ID
}

// Client returns the client with the given MAC.
func (r *Router) Client(mac string) (Client, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, c := range r.clients {
		if c.MAC == mac {
			return c, true
		}
	}
	return Client{}, false
}

func (r *Router) SetLoginLimit(n int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.LoginLimit = n
}

func (r *Router) LoginCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.Logins
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// v2 and login error format.
func v2Err(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, map[string]any{"code": code, "message": msg, "errorCode": status})
}

// Classic error format.
func classicErr(w http.ResponseWriter, status int, code string) {
	writeJSON(w, status, map[string]any{"meta": map[string]any{"rc": "error", "msg": code}, "data": []any{}})
}

func classicOK(w http.ResponseWriter, data any) {
	writeJSON(w, http.StatusOK, map[string]any{"meta": map[string]any{"rc": "ok"}, "data": data})
}

func (r *Router) login(w http.ResponseWriter, req *http.Request) {
	var body struct{ Username, Password string }
	_ = json.NewDecoder(req.Body).Decode(&body)
	r.mu.Lock()
	defer r.mu.Unlock()
	if body.Username != Username || body.Password != Password {
		v2Err(w, http.StatusForbidden, "AUTHENTICATION_FAILED_INVALID_CREDENTIALS", "Invalid username or password")
		return
	}
	if r.LoginLimit > 0 && r.Logins >= r.LoginLimit {
		v2Err(w, http.StatusTooManyRequests, "AUTHENTICATION_FAILED_LIMIT_REACHED", "You've reached the login attempt limit")
		return
	}
	r.Logins++
	token := fmt.Sprintf("session-%d", r.Logins)
	r.sessions[token] = true
	http.SetCookie(w, &http.Cookie{Name: "TOKEN", Value: token, Path: "/"})
	w.Header().Set("X-CSRF-Token", "csrf-1")
	writeJSON(w, http.StatusOK, map[string]any{"username": Username})
}

func (r *Router) authorised(w http.ResponseWriter, req *http.Request) bool {
	c, err := req.Cookie("TOKEN")
	if err != nil || !r.sessions[c.Value] {
		v2Err(w, http.StatusUnauthorized, "api.err.UserNotAuthenticated", "User not authenticated")
		return false
	}
	if req.Method != http.MethodGet && req.Header.Get("X-CSRF-Token") != "csrf-1" {
		v2Err(w, http.StatusForbidden, "api.err.InvalidCsrf", "Invalid CSRF token")
		return false
	}
	return true
}

// ---- static DNS (v2) ----

func (r *Router) dnsCollection(w http.ResponseWriter, req *http.Request) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.authorised(w, req) {
		return
	}
	switch req.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, r.dns)
	case http.MethodPost:
		var rec DNSRecord
		_ = json.NewDecoder(req.Body).Decode(&rec)
		if !r.validateDNS(w, rec, "") {
			return
		}
		rec.ID = r.id()
		r.dns = append(r.dns, rec)
		writeJSON(w, http.StatusOK, rec)
	default:
		v2Err(w, http.StatusMethodNotAllowed, "", "Method Not Allowed")
	}
}

func (r *Router) dnsItem(w http.ResponseWriter, req *http.Request) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.authorised(w, req) {
		return
	}
	id := req.URL.Path[strings.LastIndex(req.URL.Path, "/")+1:]
	idx := -1
	for i, rec := range r.dns {
		if rec.ID == id {
			idx = i
		}
	}
	if req.Method != http.MethodPut && req.Method != http.MethodDelete {
		v2Err(w, http.StatusMethodNotAllowed, "", "Method Not Allowed")
		return
	}
	if idx < 0 {
		v2Err(w, http.StatusNotFound, "api.err.StaticDnsRecordNotFound", "Static DNS Record not found")
		return
	}
	if req.Method == http.MethodDelete {
		r.dns = append(r.dns[:idx], r.dns[idx+1:]...)
		w.WriteHeader(http.StatusOK)
		return
	}
	var rec DNSRecord
	_ = json.NewDecoder(req.Body).Decode(&rec)
	if !r.validateDNS(w, rec, id) {
		return
	}
	rec.ID = id
	r.dns[idx] = rec
	writeJSON(w, http.StatusOK, rec)
}

func (r *Router) validateDNS(w http.ResponseWriter, rec DNSRecord, selfID string) bool {
	if rec.RecordType == "A" {
		if ip := net.ParseIP(rec.Value); ip == nil || ip.To4() == nil {
			v2Err(w, http.StatusBadRequest, "api.err.StaticDnsRecordInvalidParameters", "Invalid IPv4 Address")
			return false
		}
	}
	for _, o := range r.dns {
		if o.ID != selfID && o.Key == rec.Key && o.RecordType == rec.RecordType && o.Value == rec.Value {
			v2Err(w, http.StatusBadRequest, "api.err.StaticDnsRecordAlreadyExists", "Static DNS Record Already Exists")
			return false
		}
	}
	for _, c := range r.clients {
		if c.UseFixedIP && c.LocalDNSRecordEnabled && strings.EqualFold(c.LocalDNSRecord, rec.Key) {
			v2Err(w, http.StatusBadRequest, "api.err.StaticDnsOverlapsWithDeviceLocalDns", "Static DNS overlaps with device local DNS")
			return false
		}
	}
	return true
}

// ---- networks and clients (classic) ----

func (r *Router) networks(w http.ResponseWriter, req *http.Request) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.authorised(w, req) {
		return
	}
	classicOK(w, []map[string]any{
		{"_id": NetworkID, "name": "Default", "purpose": "corporate", "ip_subnet": "192.168.1.1/24",
			"dhcpd_enabled": true, "dhcpd_start": "192.168.1.6", "dhcpd_stop": "192.168.1.254", "domain_name": "localdomain"},
		{"_id": "net-wan", "name": "Internet 1", "purpose": "wan"},
	})
}

func (r *Router) userCollection(w http.ResponseWriter, req *http.Request) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.authorised(w, req) {
		return
	}
	switch req.Method {
	case http.MethodGet:
		classicOK(w, r.clients)
	case http.MethodPost:
		var fields map[string]any
		_ = json.NewDecoder(req.Body).Decode(&fields)
		mac, _ := fields["mac"].(string)
		if hw, err := net.ParseMAC(mac); err != nil || len(hw) != 6 {
			classicErr(w, http.StatusBadRequest, "api.err.InvalidPayload")
			return
		}
		for _, c := range r.clients {
			if c.MAC == mac {
				classicErr(w, http.StatusBadRequest, "api.err.MacUsed")
				return
			}
		}
		c := Client{ID: r.id(), MAC: mac}
		if !r.applyFields(w, &c, fields) {
			return
		}
		r.clients = append(r.clients, c)
		classicOK(w, []Client{c})
	default:
		classicErr(w, http.StatusMethodNotAllowed, "api.err.MethodNotAllowed")
	}
}

func (r *Router) userItem(w http.ResponseWriter, req *http.Request) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.authorised(w, req) {
		return
	}
	id := req.URL.Path[strings.LastIndex(req.URL.Path, "/")+1:]
	for i := range r.clients {
		if r.clients[i].ID != id {
			continue
		}
		if req.Method != http.MethodPut { // the real router has no DELETE here
			classicErr(w, http.StatusNotFound, "api.err.NotFound")
			return
		}
		var fields map[string]any
		_ = json.NewDecoder(req.Body).Decode(&fields)
		c := r.clients[i]
		if !r.applyFields(w, &c, fields) {
			return
		}
		r.clients[i] = c
		classicOK(w, []Client{c})
		return
	}
	classicErr(w, http.StatusNotFound, "api.err.NotFound")
}

// applyFields updates c from a request and applies the router's checks.
func (r *Router) applyFields(w http.ResponseWriter, c *Client, f map[string]any) bool {
	if v, ok := f["name"].(string); ok {
		c.Name = v
	}
	if v, ok := f["use_fixedip"].(bool); ok {
		c.UseFixedIP = v
	}
	if v, ok := f["fixed_ip"].(string); ok {
		c.FixedIP = v
	}
	if v, ok := f["network_id"].(string); ok {
		c.NetworkID = v
	}
	if v, ok := f["local_dns_record"].(string); ok {
		c.LocalDNSRecord = v
	}
	if v, ok := f["local_dns_record_enabled"].(bool); ok {
		c.LocalDNSRecordEnabled = v
	}
	if c.UseFixedIP {
		_, subnet, _ := net.ParseCIDR("192.168.1.0/24")
		ip := net.ParseIP(c.FixedIP)
		if ip == nil || !subnet.Contains(ip) {
			classicErr(w, http.StatusBadRequest, "api.err.InvalidFixedIP")
			return false
		}
		for _, o := range r.clients {
			if o.ID != c.ID && o.UseFixedIP && o.FixedIP == c.FixedIP {
				classicErr(w, http.StatusBadRequest, "api.err.DuplicateFixedIP")
				return false
			}
		}
	}
	if c.LocalDNSRecordEnabled {
		if !c.UseFixedIP {
			classicErr(w, http.StatusBadRequest, "api.err.LocalDnsRecordRequiresFixedIp")
			return false
		}
		for _, d := range r.dns {
			if strings.EqualFold(d.Key, c.LocalDNSRecord) {
				classicErr(w, http.StatusBadRequest, "api.err.Invalid") // what the real router says
				return false
			}
		}
	}
	return true
}

func (r *Router) stamgr(w http.ResponseWriter, req *http.Request) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.authorised(w, req) {
		return
	}
	var body struct {
		Cmd  string   `json:"cmd"`
		MACs []string `json:"macs"`
	}
	_ = json.NewDecoder(req.Body).Decode(&body)
	if body.Cmd != "forget-sta" {
		classicErr(w, http.StatusBadRequest, "api.err.InvalidCommand")
		return
	}
	var kept []Client
	var forgotten []Client
	for _, c := range r.clients {
		if contains(body.MACs, c.MAC) {
			forgotten = append(forgotten, c)
		} else {
			kept = append(kept, c)
		}
	}
	r.clients = kept
	classicOK(w, forgotten)
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
