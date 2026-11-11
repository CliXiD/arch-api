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
	Source      string         `json:"source" enum:"ui,api,yaml,discovery,import"`
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
	Source      string         `json:"source" enum:"ui,api,yaml,discovery,import"`
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

// Actor is who writes: recorded in every change_log row. Admin may write anything; anyone else
// writes only nodes owned by one of Teams.
type Actor struct {
	// Subject is the token's sub; for a service account, its client id.
	Subject string
	// Username is preferred_username; for a service account, its client id.
	Username string
	// Kind is `user` or `service`.
	Kind  string
	Admin bool
	Teams []string
}

// NodeCreate is the body of POST /nodes.
type NodeCreate struct {
	Environment string         `json:"environment"`
	ID          string         `json:"id" pattern:"^[a-z0-9][a-z0-9-]{1,62}$" doc:"Unique in the environment."`
	Type        string         `json:"type" doc:"Node type id."`
	Name        string         `json:"name" minLength:"1"`
	Description string         `json:"description,omitempty"`
	Owner       string         `json:"owner" doc:"Owning team id."`
	Tags        []string       `json:"tags,omitempty" nullable:"false"`
	Attributes  map[string]any `json:"attributes,omitempty" doc:"Checked against the type's attributes_schema and for secrets."`
}

// NodePatch is the body of PATCH /nodes/{environment}/{id}. Each present field replaces the
// stored value whole; absent fields are kept.
type NodePatch struct {
	Name        *string        `json:"name,omitempty" minLength:"1" nullable:"false"`
	Description *string        `json:"description,omitempty" nullable:"false"`
	Owner       *string        `json:"owner,omitempty" nullable:"false" doc:"Owning team id; needs rights on the old and the new team."`
	Tags        *[]string      `json:"tags,omitempty" nullable:"false"`
	Attributes  map[string]any `json:"attributes,omitempty" doc:"Replaces every attribute."`
}

// EdgeCreate is the body of POST /edges.
type EdgeCreate struct {
	Environment string         `json:"environment"`
	Type        string         `json:"type" doc:"Edge type id."`
	From        string         `json:"from" doc:"Source node id, in the environment."`
	To          string         `json:"to" doc:"Target node id, in the environment."`
	Attributes  map[string]any `json:"attributes,omitempty" doc:"Checked against the type's attributes_schema and for secrets."`
}

// EdgePatch is the body of PATCH /edges/{uid}.
type EdgePatch struct {
	Attributes map[string]any `json:"attributes" doc:"Replaces every attribute."`
}

// TeamCreate is the body of POST /teams.
type TeamCreate struct {
	ID   string `json:"id" pattern:"^[a-z0-9][a-z0-9-]{1,62}$"`
	Name string `json:"name" minLength:"1"`
}

// TeamPatch is the body of PATCH /teams/{id}.
type TeamPatch struct {
	Name string `json:"name" minLength:"1"`
}

// EnvironmentCreate is the body of POST /environments.
type EnvironmentCreate struct {
	ID   string `json:"id" pattern:"^[a-z0-9][a-z0-9-]{1,62}$"`
	Name string `json:"name" minLength:"1"`
	Sort int32  `json:"sort" doc:"Display order; unique."`
}

// EnvironmentPatch is the body of PATCH /environments/{id}. Absent fields are kept.
type EnvironmentPatch struct {
	Name *string `json:"name,omitempty" minLength:"1" nullable:"false"`
	Sort *int32  `json:"sort,omitempty" nullable:"false" doc:"Display order; unique."`
}

// NodeTypeCreate is the body of POST /admin/types/node-types. The type is never built-in.
type NodeTypeCreate struct {
	ID               string         `json:"id" pattern:"^[a-z0-9][a-z0-9-]{1,62}$"`
	Kind             string         `json:"kind" enum:"application,infrastructure,network,external,team" doc:"Fixed once created."`
	Name             string         `json:"name" minLength:"1"`
	AttributesSchema map[string]any `json:"attributes_schema" doc:"JSON Schema of the node attributes, in the documented subset."`
}

// NodeTypePatch is the body of PATCH /admin/types/node-types/{id}. Each present field replaces the
// stored value whole; absent fields are kept.
type NodeTypePatch struct {
	Name             *string        `json:"name,omitempty" minLength:"1" nullable:"false"`
	AttributesSchema map[string]any `json:"attributes_schema,omitempty" doc:"Replaces the schema; every existing node of the type must still pass it."`
}

// EdgeTypeCreate is the body of POST /admin/types/edge-types. The type is never built-in.
type EdgeTypeCreate struct {
	ID               string         `json:"id" pattern:"^[a-z][a-z0-9_]{1,62}$"`
	Name             string         `json:"name" minLength:"1"`
	AttributesSchema map[string]any `json:"attributes_schema" doc:"JSON Schema of the edge attributes, in the documented subset."`
	Allowed          []KindPair     `json:"allowed" minItems:"1" uniqueItems:"true" nullable:"false" doc:"Allowed (from kind, to kind) pairs."`
}

// EdgeTypePatch is the body of PATCH /admin/types/edge-types/{id}. Each present field replaces the
// stored value whole; absent fields are kept.
type EdgeTypePatch struct {
	Name             *string        `json:"name,omitempty" minLength:"1" nullable:"false"`
	AttributesSchema map[string]any `json:"attributes_schema,omitempty" doc:"Replaces the schema; every existing edge of the type must still pass it."`
	Allowed          *[]KindPair    `json:"allowed,omitempty" minItems:"1" uniqueItems:"true" nullable:"false" doc:"Replaces the pairs; every existing edge of the type must still use one of them."`
}

// Change is one change_log row: a created, updated or deleted entity.
type Change struct {
	ID        int64     `json:"id"`
	At        time.Time `json:"at"`
	Actor     string    `json:"actor" doc:"Subject of the token; for a service account, its client id."`
	ActorName string    `json:"actor_name" doc:"Username; for a service account, its client id."`
	ActorKind string    `json:"actor_kind" enum:"user,service"`
	Source    string    `json:"source" enum:"ui,api,yaml,discovery,import"`
	Entity    string    `json:"entity" enum:"node,edge,type,team,environment"`
	EntityKey string    `json:"entity_key" doc:"Node <environment>/<id>, edge uid, type node/<id> or edge/<id>, team or environment id."`
	Action    string    `json:"action" enum:"create,update,delete"`
	// Before is the entity's API JSON before the change; null on create.
	Before map[string]any `json:"before" nullable:"true" doc:"The entity before the change; null on create."`
	// After is the entity's API JSON after the change; null on delete.
	After map[string]any `json:"after" nullable:"true" doc:"The entity after the change; null on delete."`
}

// HistoryPage is one page of change_log rows, ordered by (at, id) descending.
type HistoryPage struct {
	Items      []Change `json:"items" nullable:"false"`
	NextCursor *string  `json:"next_cursor" nullable:"true" doc:"Cursor of the next page; null on the last page."`
}
