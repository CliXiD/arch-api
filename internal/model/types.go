package model

import "time"

// NodeType is a node type of the vocabulary.
type NodeType struct {
	ID               string         `json:"id"`
	Kind             string         `json:"kind" enum:"application,infrastructure,network,external,team"`
	Name             string         `json:"name"`
	AttributesSchema map[string]any `json:"attributes_schema" doc:"JSON Schema of the node attributes."`
	Builtin          bool           `json:"builtin"`
}

// KindPair is a (from kind, to kind) pair an edge type allows.
type KindPair struct {
	From string `json:"from" enum:"application,infrastructure,network,external,team"`
	To   string `json:"to" enum:"application,infrastructure,network,external,team"`
}

// EdgeType is an edge type of the vocabulary.
type EdgeType struct {
	ID               string         `json:"id"`
	Name             string         `json:"name"`
	AttributesSchema map[string]any `json:"attributes_schema" doc:"JSON Schema of the edge attributes."`
	Allowed          []KindPair     `json:"allowed" nullable:"false" doc:"Allowed (from kind, to kind) pairs."`
	Builtin          bool           `json:"builtin"`
}

// Types is the vocabulary: node and edge types, each ordered by id.
type Types struct {
	NodeTypes []NodeType `json:"node_types" nullable:"false"`
	EdgeTypes []EdgeType `json:"edge_types" nullable:"false"`
}

// Team is an owning team.
type Team struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
}

// TeamList is every team, ordered by id.
type TeamList struct {
	Items []Team `json:"items" nullable:"false"`
}

// Environment is a model environment (dev, uat, prod, ...).
type Environment struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Sort int    `json:"sort"`
}

// EnvironmentList is every environment, ordered by sort.
type EnvironmentList struct {
	Items []Environment `json:"items" nullable:"false"`
}

// Node is a node of the graph in one environment.
type Node struct {
	Environment string         `json:"environment"`
	ID          string         `json:"id"`
	Kind        string         `json:"kind" enum:"application,infrastructure,network,external,team" doc:"Fixed by the type."`
	Type        string         `json:"type"`
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Owner       string         `json:"owner" doc:"Owning team id."`
	Attributes  map[string]any `json:"attributes"`
	Tags        []string       `json:"tags" nullable:"false"`
	Source      string         `json:"source" enum:"ui,yaml,discovery,import"`
	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
	Version     int            `json:"version"`
}

// Edge is a typed edge between two nodes of one environment.
type Edge struct {
	UID         string         `json:"uid" format:"uuid"`
	Environment string         `json:"environment"`
	Type        string         `json:"type"`
	From        string         `json:"from" doc:"Source node id."`
	To          string         `json:"to" doc:"Target node id."`
	Attributes  map[string]any `json:"attributes"`
	Source      string         `json:"source" enum:"ui,yaml,discovery,import"`
	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
	Version     int            `json:"version"`
}

// NodePage is one page of nodes, ordered by (environment, id).
type NodePage struct {
	Items      []Node  `json:"items" nullable:"false"`
	NextCursor *string `json:"next_cursor" nullable:"true" doc:"Cursor of the next page; null on the last page."`
}

// EdgePage is one page of edges, ordered by (environment, type, from, to).
type EdgePage struct {
	Items      []Edge  `json:"items" nullable:"false"`
	NextCursor *string `json:"next_cursor" nullable:"true" doc:"Cursor of the next page; null on the last page."`
}

// GraphNode is a node in the compact graph: no description, attributes or timestamps.
type GraphNode struct {
	ID    string   `json:"id"`
	Kind  string   `json:"kind" enum:"application,infrastructure,network,external,team"`
	Type  string   `json:"type"`
	Name  string   `json:"name"`
	Owner string   `json:"owner"`
	Tags  []string `json:"tags" nullable:"false"`
}

// GraphEdge is an edge in the compact graph.
type GraphEdge struct {
	UID        string         `json:"uid" format:"uuid"`
	Type       string         `json:"type"`
	From       string         `json:"from"`
	To         string         `json:"to"`
	Attributes map[string]any `json:"attributes"`
}

// Graph is one environment's whole graph: nodes by id, edges by (type, from, to).
type Graph struct {
	Environment string      `json:"environment"`
	Nodes       []GraphNode `json:"nodes" nullable:"false"`
	Edges       []GraphEdge `json:"edges" nullable:"false"`
}

// NodeFilter selects a page of nodes. Empty fields do not filter.
type NodeFilter struct {
	Environment string
	Kind        string
	Type        string
	Owner       string
	// Q is a case-insensitive substring of the id or name.
	Q      string
	Limit  int
	Cursor string
}

// EdgeFilter selects a page of edges. Empty fields do not filter.
type EdgeFilter struct {
	Environment string
	Type        string
	From        string
	To          string
	Limit       int
	Cursor      string
}
