package tools

import (
	"fmt"
	"regexp"
	"time"
)

// Wire types for the saved conversations and collections of the Agent
// Observability eval control plane (/eval/saved-conversations and
// /eval/collections on the grafana-agento11y-app plugin resources proxy).

// agento11ySavedIDPattern mirrors the server-side saved conversation ID
// pattern, which is looser than the evaluator and rule IDs: hyphens and colons
// are allowed.
var agento11ySavedIDPattern = regexp.MustCompile(`^[A-Za-z0-9_.:-]+$`)

// agento11yDerivedSavedID is the saved conversation ID used when the caller does
// not supply one. The API has no ID generation, and this is what the plugin UI
// derives.
func agento11yDerivedSavedID(conversationID string) string {
	return "saved-" + conversationID
}

// Agento11ySavedConversation is a bookmarked conversation from
// /eval/saved-conversations. The list paths also fill in the enrichment fields
// and the embedded collection chips, so listing a page needs no follow-up call
// per row.
type Agento11ySavedConversation struct {
	SavedID        string            `json:"saved_id"`
	ConversationID string            `json:"conversation_id"`
	Name           string            `json:"name"`
	Source         string            `json:"source"` // telemetry, manual
	Tags           map[string]string `json:"tags,omitempty"`
	SavedBy        string            `json:"saved_by,omitempty"`
	TenantID       string            `json:"tenant_id,omitempty"`
	CreatedAt      time.Time         `json:"created_at,omitzero"`
	UpdatedAt      time.Time         `json:"updated_at,omitzero"`

	// Enrichment fields, populated by the list paths from the conversations table.
	GenerationCount int               `json:"generation_count"`
	TotalTokens     int64             `json:"total_tokens"`
	AgentNames      []string          `json:"agent_names,omitempty"`
	Models          []string          `json:"models,omitempty"`
	ModelProviders  map[string]string `json:"model_providers,omitempty"`

	// Collections has three distinct wire states, so it is a pointer:
	// nil means the response was not enriched (get and create answers), an empty
	// slice means an enriched row with no memberships, and a populated slice
	// carries the chips. Flattening nil into [] would tell a caller that a row
	// has no collections when the server never looked.
	Collections *[]Agento11yCollectionRef `json:"collections,omitempty"`
}

// Agento11yCollection is a named group of saved conversations from /eval/collections.
type Agento11yCollection struct {
	CollectionID string    `json:"collection_id"`
	Name         string    `json:"name"`
	Description  string    `json:"description,omitempty"`
	MemberCount  int       `json:"member_count"`
	TenantID     string    `json:"tenant_id,omitempty"`
	CreatedBy    string    `json:"created_by,omitempty"`
	UpdatedBy    string    `json:"updated_by,omitempty"`
	CreatedAt    time.Time `json:"created_at,omitzero"`
	UpdatedAt    time.Time `json:"updated_at,omitzero"`
}

// Agento11yCollectionRef is the trimmed collection form embedded in enriched
// saved conversation rows. It has no member count.
type Agento11yCollectionRef struct {
	CollectionID string `json:"collection_id"`
	Name         string `json:"name"`
}

// Agento11ySavedConversationsResponse is the envelope of
// GET /eval/saved-conversations. It is separate from agento11yListResponse
// because only this route reports total_count.
type Agento11ySavedConversationsResponse struct {
	Items      []Agento11ySavedConversation `json:"items"`
	NextCursor string                       `json:"next_cursor,omitempty"`
	TotalCount int64                        `json:"total_count"`
}

// agento11yEvalCollectionFields are the filter and pagination parameters shared by the
// read and write variants of agento11y_evals_read/agento11y_evals_write. The ID and
// body parameters are declared on each variant separately, because their
// guidance names the operations that use them and the read variant must not
// advertise operations it rejects.
type agento11yEvalCollectionFields struct {
	Source string `json:"source,omitempty" jsonschema:"enum=telemetry,enum=manual,description=Saved conversation source filter: 'telemetry' for bookmarked production conversations\\, 'manual' for hand-built ones (for 'list_saved_conversations')"`
	Limit  int    `json:"limit,omitempty" jsonschema:"description=Maximum number of results per page (default 50\\, max 500) (for the paginated list operations)"`
	Cursor string `json:"cursor,omitempty" jsonschema:"description=Pagination cursor from a previous response's next_cursor\\, echoed back exactly and never constructed or incremented. A cursor belongs to the operation and filters that produced it: 'list_saved_conversations' returns an opaque numeric value while 'list_collections' and 'list_collection_members' return the last row ID\\, so they are not interchangeable."`
}

// agento11yEvalCollectionReadRequest is the input of a saved conversation or collection
// read operation, assembled by either tool variant.
type agento11yEvalCollectionReadRequest struct {
	agento11yEvalCollectionFields

	SavedID      string
	CollectionID string
}

// validateOperation validates the read operations of
// agento11y_evals_read/agento11y_evals_write. Operations it does not handle return
// errAgento11yUnknownOperation.
func (r agento11yEvalCollectionReadRequest) validateOperation(operation string) error {
	switch operation {
	case "list_saved_conversations":
		return validateAgento11ySource(r.Source)
	case "get_saved_conversation", "list_collections_for_saved_conversation":
		if r.SavedID == "" {
			return fmt.Errorf("saved_id is required for %q operation", operation)
		}
		return nil
	case "list_collections":
		return nil
	case "get_collection", "list_collection_members":
		if r.CollectionID == "" {
			return fmt.Errorf("collection_id is required for %q operation", operation)
		}
		return nil
	default:
		return errAgento11yUnknownOperation
	}
}

// validateAgento11ySource rejects a source the API answers 400 for.
func validateAgento11ySource(source string) error {
	switch source {
	case "", "telemetry", "manual":
		return nil
	default:
		return fmt.Errorf("unknown source %q, must be one of: telemetry, manual", source)
	}
}

const agento11yEvalCollectionReadOperations = "list_saved_conversations, get_saved_conversation, list_collections_for_saved_conversation, list_collections, get_collection, list_collection_members"
