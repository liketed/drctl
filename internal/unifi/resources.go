package unifi

import (
	"context"
	"fmt"
	"net/http"
)

// DNSRecord is a static DNS record (Settings → Routing → DNS). Numeric fields
// a record type doesn't use are 0; a TTL of 0 means automatic.
type DNSRecord struct {
	ID         string `json:"_id,omitempty"`
	RecordType string `json:"record_type"`
	Key        string `json:"key"`
	Value      string `json:"value"`
	Enabled    bool   `json:"enabled"`
	TTL        int64  `json:"ttl"`
	Priority   int64  `json:"priority"`
	Weight     int64  `json:"weight"`
	Port       int64  `json:"port"`
}

// ListDNS returns all static DNS records.
func (c *Client) ListDNS(ctx context.Context) ([]DNSRecord, error) {
	var out []DNSRecord
	err := c.do(ctx, http.MethodGet, c.v2("/static-dns"), nil, &out)
	return out, err
}

// CreateDNS adds a record and returns it as stored.
func (c *Client) CreateDNS(ctx context.Context, r DNSRecord) (DNSRecord, error) {
	r.ID = ""
	var out DNSRecord
	err := c.do(ctx, http.MethodPost, c.v2("/static-dns"), r, &out)
	return out, err
}

// UpdateDNS replaces the record with r.ID and returns it as stored.
func (c *Client) UpdateDNS(ctx context.Context, r DNSRecord) (DNSRecord, error) {
	var out DNSRecord
	err := c.do(ctx, http.MethodPut, c.v2("/static-dns/"+r.ID), r, &out)
	return out, err
}

// DeleteDNS removes the record with the given ID.
func (c *Client) DeleteDNS(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodDelete, c.v2("/static-dns/"+id), nil, nil)
}

// Network is a LAN from Settings → Networks.
type Network struct {
	ID          string `json:"_id"`
	Name        string `json:"name"`
	Purpose     string `json:"purpose"`
	Subnet      string `json:"ip_subnet"` // gateway address with prefix, e.g. 192.168.1.1/24
	DHCPEnabled bool   `json:"dhcpd_enabled"`
	DHCPStart   string `json:"dhcpd_start"`
	DHCPStop    string `json:"dhcpd_stop"`
	DomainName  string `json:"domain_name"`
}

// ListNetworks returns the configured networks.
func (c *Client) ListNetworks(ctx context.Context) ([]Network, error) {
	var out []Network
	err := c.classicDo(ctx, http.MethodGet, "/rest/networkconf", nil, &out)
	return out, err
}

// ClientDevice is a device the Network application knows about. A DHCP reservation
// is a client with UseFixedIP set; its DNS name is LocalDNSRecord, which the
// router only serves while UseFixedIP is set.
type ClientDevice struct {
	ID                    string `json:"_id,omitempty"`
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

// DisplayName is the client's name, falling back to its hostname.
func (d ClientDevice) DisplayName() string {
	if d.Name != "" {
		return d.Name
	}
	return d.Hostname
}

// ListClients returns every client the Network application knows about.
func (c *Client) ListClients(ctx context.Context) ([]ClientDevice, error) {
	var out []ClientDevice
	err := c.classicDo(ctx, http.MethodGet, "/rest/user", nil, &out)
	return out, err
}

// CreateClient adds a client (e.g. a reservation for a device not seen yet).
func (c *Client) CreateClient(ctx context.Context, fields map[string]any) (ClientDevice, error) {
	var out []ClientDevice
	if err := c.classicDo(ctx, http.MethodPost, "/rest/user", fields, &out); err != nil {
		return ClientDevice{}, err
	}
	if len(out) == 0 {
		return ClientDevice{}, fmt.Errorf("POST /rest/user: empty response")
	}
	return out[0], nil
}

// UpdateClient changes the given fields of a client and returns it as stored.
func (c *Client) UpdateClient(ctx context.Context, id string, fields map[string]any) (ClientDevice, error) {
	var out []ClientDevice
	if err := c.classicDo(ctx, http.MethodPut, "/rest/user/"+id, fields, &out); err != nil {
		return ClientDevice{}, err
	}
	if len(out) == 0 {
		return ClientDevice{}, fmt.Errorf("PUT /rest/user/%s: empty response", id)
	}
	return out[0], nil
}

// ForgetClient removes a client entirely, including its name and history.
func (c *Client) ForgetClient(ctx context.Context, mac string) error {
	return c.classicDo(ctx, http.MethodPost, "/cmd/stamgr", map[string]any{"cmd": "forget-sta", "macs": []string{mac}}, nil)
}
