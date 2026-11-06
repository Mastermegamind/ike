package store

import "time"

// APIToken signs in scripts, the ike CLI and billing systems as a person.
type APIToken struct {
	ID       string    `json:"id"`
	Hash     string    `json:"hash"`
	Name     string    `json:"name"`
	Username string    `json:"username"`
	ReadOnly bool      `json:"read_only"`
	Created  time.Time `json:"created"`
	LastUsed time.Time `json:"last_used,omitzero"`
	Expires  time.Time `json:"expires,omitzero"` // zero = never
	// Limited tokens may only use the areas in Scopes (area → "read" |
	// "write"). Other tokens have the person's full access.
	Limited bool              `json:"limited,omitempty"`
	Scopes  map[string]string `json:"scopes,omitempty"`
	// Accounts limits the token to some of the person's hosting accounts.
	Accounts []string `json:"accounts,omitempty"`
	// AllowIPs limits where the token may be used from (IPs or CIDR ranges).
	AllowIPs []string `json:"allow_ips,omitempty"`
}

// TokenAreas are the parts of the API a token can be limited to.
var TokenAreas = []string{"sites", "dns", "mail", "databases", "files", "backups", "containers", "security", "billing", "accounts", "server"}

// PackageLimits cap what an account on a hosting package can have.
// 0 means unlimited for counts and sizes.
type PackageLimits struct {
	Sites        int  `json:"sites"`
	Domains      int  `json:"domains"` // extra domains (aliases) and DNS zones
	Databases    int  `json:"databases"`
	EmailDomains int  `json:"email_domains"`
	Mailboxes    int  `json:"mailboxes"`
	DiskMB       int  `json:"disk_mb"`
	BandwidthGB  int  `json:"bandwidth_gb"` // per calendar month
	CPUPercent   int  `json:"cpu_percent"`
	MemoryMB     int  `json:"memory_mb"`
	Processes    int  `json:"processes"`
	PHPWorkers   int  `json:"php_workers"`
	Shell        bool `json:"shell"`      // SSH shell and the web terminal
	Containers   bool `json:"containers"` // Docker containers and stacks
	Backups      bool `json:"backups"`    // own backup plans
}

// Package is a hosting plan.
type Package struct {
	ID       string        `json:"id"`
	Name     string        `json:"name"`
	Owner    string        `json:"owner,omitempty"` // reseller username; "" = the admin's
	Price    int64         `json:"price"`           // minor units per interval
	Currency string        `json:"currency"`
	Interval string        `json:"interval"` // month | year | none
	Limits   PackageLimits `json:"limits"`
	Created  time.Time     `json:"created"`
}

// Subscription bills an account for its package on a schedule.
type Subscription struct {
	ID          string    `json:"id"`
	Account     string    `json:"account"`
	PackageID   string    `json:"package_id"`
	Price       int64     `json:"price"`
	Currency    string    `json:"currency"`
	Interval    string    `json:"interval"` // month | year
	NextDue     time.Time `json:"next_due"`
	Status      string    `json:"status"` // active | cancelled
	LastInvoice string    `json:"last_invoice,omitempty"`
	CreatedBy   string    `json:"created_by"`
	CreatedAt   time.Time `json:"created_at"`
}

// Branding is a white-label look for the admin's or a reseller's customers.
type Branding struct {
	ProductName   string   `json:"product_name"`
	Logo          string   `json:"logo,omitempty"` // data: URL, PNG/SVG/WebP, ≤ 200 KB
	PrimaryColor  string   `json:"primary_color"`
	SupportEmail  string   `json:"support_email"`
	SupportURL    string   `json:"support_url"`
	HidePoweredBy bool     `json:"hide_powered_by"`
	Hostnames     []string `json:"hostnames"` // panel addresses that show this branding
}

// BWDay remembers the last bandwidth reading for an account today.
type BWDay struct {
	Day    string  `json:"day"`
	LastMB float64 `json:"last_mb"`
}

// SSOToken signs a person in once (billing systems' "log in to control panel").
type SSOToken struct {
	Hash     string    `json:"hash"`
	Username string    `json:"username"`
	Expires  time.Time `json:"expires"`
}
