package proto

// ListStatelessSessionsRequest is the request body for listing stateless
// sessions.
type ListStatelessSessionsRequest struct {
	ServiceFilter string `json:"service_filter,omitempty"`
}

// SessionSummary is a lightweight session representation used in proto-layer
// responses for stateless session listings.
type SessionSummary struct {
	ID           string `json:"id"`
	Title        string `json:"title"`
	Service      string `json:"service"`
	MessageCount int    `json:"message_count"`
	CreatedAt    int64  `json:"created_at"`
	UpdatedAt    int64  `json:"updated_at"`
}

// ListStatelessSessionsResponse is the response body for listing stateless
// sessions.
type ListStatelessSessionsResponse struct {
	Sessions []SessionSummary `json:"sessions"`
}

// PruneStatelessSessionRequest is the request body for pruning messages from
// a stateless session.
type PruneStatelessSessionRequest struct {
	Before string `json:"before"`
	DryRun bool   `json:"dry_run"`
}

// PruneStatelessSessionResponse is the response body for pruning messages
// from a stateless session.
type PruneStatelessSessionResponse struct {
	SessionID      string `json:"session_id"`
	MessagesPruned int    `json:"messages_pruned"`
}
