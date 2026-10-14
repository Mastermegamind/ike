package store

import "time"

// RemoteServer is another Ike server managed from this panel.
type RemoteServer struct {
	ID       string    `json:"id"`
	Name     string    `json:"name"`
	Address  string    `json:"address"` // host:2089
	Group    string    `json:"group,omitempty"`
	Hostname string    `json:"hostname,omitempty"`
	AddedAt  time.Time `json:"added_at"`
	LastSeen time.Time `json:"last_seen,omitempty"`
	Version  string    `json:"version,omitempty"`
	Status   string    `json:"status,omitempty"` // online | offline
}

// JoinToken lets one server join; stored hashed.
type JoinToken struct {
	Hash    string    `json:"hash"`
	Name    string    `json:"name"`
	Group   string    `json:"group,omitempty"`
	Address string    `json:"address,omitempty"` // override for servers behind NAT
	Expires time.Time `json:"expires"`
}

// DBCluster is a MariaDB or Redis primary with replicas on other servers.
type DBCluster struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Engine    string    `json:"engine"`  // mariadb | redis
	Primary   string    `json:"primary"` // server ID ("local" = this server)
	Replicas  []string  `json:"replicas"`
	CreatedAt time.Time `json:"created_at"`
}
