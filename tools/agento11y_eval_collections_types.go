package tools

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
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
// read and read-write variants of agento11y_manage_eval_collections. The ID and
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
// agento11y_manage_eval_collections. Operations it does not handle return
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

const agento11yEvalCollectionAllOperations = agento11yEvalCollectionReadOperations + ", save_conversation, delete_saved_conversation, create_collection, update_collection, delete_collection, add_collection_members, remove_collection_member"

// ManageAgento11yEvalCollectionsReadParams is the param struct for the read-only
// version of agento11y_manage_eval_collections.
type ManageAgento11yEvalCollectionsReadParams struct {
	agento11yEvalCollectionFields

	Operation    string `json:"operation" jsonschema:"required,enum=list_saved_conversations,enum=get_saved_conversation,enum=list_collections_for_saved_conversation,enum=list_collections,enum=get_collection,enum=list_collection_members,description=The operation to perform: 'list_saved_conversations' for the bookmarked conversations in this tenant\\, 'get_saved_conversation' for one bookmark\\, 'list_collections_for_saved_conversation' for the collections one bookmark belongs to\\, 'list_collections' for the collections in this tenant\\, 'get_collection' for one collection\\, 'list_collection_members' for the saved conversations in a collection"`
	SavedID      string `json:"saved_id,omitempty" jsonschema:"description=Saved conversation ID (required for 'get_saved_conversation' and 'list_collections_for_saved_conversation')"`
	CollectionID string `json:"collection_id,omitempty" jsonschema:"description=Collection ID (required for 'get_collection' and 'list_collection_members')"`
}

func (p ManageAgento11yEvalCollectionsReadParams) readRequest() agento11yEvalCollectionReadRequest {
	return agento11yEvalCollectionReadRequest{
		agento11yEvalCollectionFields: p.agento11yEvalCollectionFields,
		SavedID:                       p.SavedID,
		CollectionID:                  p.CollectionID,
	}
}

func (p ManageAgento11yEvalCollectionsReadParams) validate() error {
	err := p.readRequest().validateOperation(p.Operation)
	if errors.Is(err, errAgento11yUnknownOperation) {
		return fmt.Errorf("unknown operation %q, must be one of: %s", p.Operation, agento11yEvalCollectionReadOperations)
	}
	return err
}

// ManageAgento11yEvalCollectionsReadWriteParams is the param struct for the
// read-write version of agento11y_manage_eval_collections.
type ManageAgento11yEvalCollectionsReadWriteParams struct {
	agento11yEvalCollectionFields

	Operation      string            `json:"operation" jsonschema:"required,enum=list_saved_conversations,enum=get_saved_conversation,enum=list_collections_for_saved_conversation,enum=list_collections,enum=get_collection,enum=list_collection_members,enum=save_conversation,enum=delete_saved_conversation,enum=create_collection,enum=update_collection,enum=delete_collection,enum=add_collection_members,enum=remove_collection_member,description=The operation to perform. Reads: 'list_saved_conversations'\\, 'get_saved_conversation'\\, 'list_collections_for_saved_conversation'\\, 'list_collections'\\, 'get_collection'\\, 'list_collection_members'. Writes: 'save_conversation' (bookmark a conversation)\\, 'delete_saved_conversation'\\, 'create_collection'\\, 'update_collection' (PATCH\\, partial)\\, 'delete_collection'\\, 'add_collection_members'\\, 'remove_collection_member'"`
	SavedID        string            `json:"saved_id,omitempty" jsonschema:"description=Saved conversation ID. Required for 'get_saved_conversation'\\, 'list_collections_for_saved_conversation'\\, 'delete_saved_conversation'\\, and 'remove_collection_member'. Optional for 'save_conversation'\\, which derives 'saved-<conversation_id>' when it is omitted. Only letters\\, digits\\, '_'\\, '.'\\, ':'\\, and '-' are accepted."`
	CollectionID   string            `json:"collection_id,omitempty" jsonschema:"description=Collection ID. Required for 'get_collection'\\, 'list_collection_members'\\, 'update_collection'\\, 'delete_collection'\\, 'add_collection_members'\\, and 'remove_collection_member'. Rejected by 'create_collection' (the API assigns a UUID) and by 'save_conversation' (bookmarking cannot also add to a collection)."`
	ConversationID string            `json:"conversation_id,omitempty" jsonschema:"description=Conversation to bookmark (required for 'save_conversation'). Find one with agento11y_manage_conversations."`
	Name           string            `json:"name,omitempty" jsonschema:"description=Human-readable name. Required for 'save_conversation' and 'create_collection'; optional for 'update_collection'\\, where an omitted name is left unchanged and a blank one is rejected."`
	Description    *string           `json:"description,omitempty" jsonschema:"description=Collection description (for 'create_collection' and 'update_collection'). On 'update_collection' an omitted description is left unchanged and an explicitly empty string clears it."`
	Tags           map[string]string `json:"tags,omitempty" jsonschema:"description=Optional string tags to store on a bookmark (for 'save_conversation')"`
	SavedIDs       []string          `json:"saved_ids,omitempty" jsonschema:"description=Saved conversation IDs to add to a collection (required\\, non-empty\\, for 'add_collection_members'; rejected by 'create_collection'\\, which always creates an empty collection). Every ID must already be a saved conversation; bookmark it with 'save_conversation' first. Re-adding a member is a no-op."`
}

func (p ManageAgento11yEvalCollectionsReadWriteParams) readRequest() agento11yEvalCollectionReadRequest {
	return agento11yEvalCollectionReadRequest{
		agento11yEvalCollectionFields: p.agento11yEvalCollectionFields,
		SavedID:                       p.SavedID,
		CollectionID:                  p.CollectionID,
	}
}

func (p ManageAgento11yEvalCollectionsReadWriteParams) validate() error {
	err := p.readRequest().validateOperation(p.Operation)
	if !errors.Is(err, errAgento11yUnknownOperation) {
		return err
	}

	switch p.Operation {
	case "save_conversation":
		if p.ConversationID == "" {
			return fmt.Errorf("conversation_id is required for 'save_conversation' operation")
		}
		if p.CollectionID != "" {
			return fmt.Errorf("collection_id is not accepted by 'save_conversation' operation: bookmarking and adding to a collection are two calls, so run 'save_conversation' first and then 'add_collection_members' with the resulting saved_id")
		}
		if strings.TrimSpace(p.Name) == "" {
			return fmt.Errorf("name is required for 'save_conversation' operation")
		}
		// Both the supplied and the derived ID are checked here, so an invalid ID
		// fails with the rule spelled out instead of a bare 400 from the API.
		if p.SavedID != "" {
			if !agento11ySavedIDPattern.MatchString(p.SavedID) {
				return fmt.Errorf("saved_id %q is invalid: only letters, digits, '_', '.', ':', and '-' are accepted", p.SavedID)
			}
			return nil
		}
		if derived := agento11yDerivedSavedID(p.ConversationID); !agento11ySavedIDPattern.MatchString(derived) {
			return fmt.Errorf("the saved_id derived from conversation_id, %q, is invalid: only letters, digits, '_', '.', ':', and '-' are accepted, so pass an explicit saved_id", derived)
		}
		return nil
	case "delete_saved_conversation":
		if p.SavedID == "" {
			return fmt.Errorf("saved_id is required for 'delete_saved_conversation' operation")
		}
		return nil
	case "create_collection":
		if strings.TrimSpace(p.Name) == "" {
			return fmt.Errorf("name is required for 'create_collection' operation")
		}
		// The API assigns the ID, so a supplied one would be dropped and the caller
		// would reuse an ID the collection does not have.
		if p.CollectionID != "" {
			return fmt.Errorf("collection_id is not accepted by 'create_collection' operation, which gets a server-assigned UUID: use the collection_id from the response for follow-up calls")
		}
		// The API creates an empty collection; these would be dropped silently and
		// the caller would be told the members are in it.
		if len(p.SavedIDs) > 0 {
			return fmt.Errorf("saved_ids is not accepted by 'create_collection' operation, which always creates an empty collection: run 'add_collection_members' with the returned collection_id to fill it")
		}
		return nil
	case "update_collection":
		if p.CollectionID == "" {
			return fmt.Errorf("collection_id is required for 'update_collection' operation")
		}
		if p.Name == "" && p.Description == nil {
			return fmt.Errorf("at least one of name or description is required for 'update_collection' operation (an omitted field is left unchanged)")
		}
		if p.Name != "" && strings.TrimSpace(p.Name) == "" {
			return fmt.Errorf("name must not be blank for 'update_collection' operation (omit it to leave the name unchanged)")
		}
		return nil
	case "delete_collection":
		if p.CollectionID == "" {
			return fmt.Errorf("collection_id is required for 'delete_collection' operation")
		}
		return nil
	case "add_collection_members":
		if p.CollectionID == "" {
			return fmt.Errorf("collection_id is required for 'add_collection_members' operation")
		}
		if len(p.SavedIDs) == 0 {
			return fmt.Errorf("saved_ids is required for 'add_collection_members' operation and must contain at least one saved conversation ID")
		}
		return nil
	case "remove_collection_member":
		if p.CollectionID == "" {
			return fmt.Errorf("collection_id is required for 'remove_collection_member' operation")
		}
		if p.SavedID == "" {
			return fmt.Errorf("saved_id is required for 'remove_collection_member' operation")
		}
		return nil
	default:
		return fmt.Errorf("unknown operation %q, must be one of: %s", p.Operation, agento11yEvalCollectionAllOperations)
	}
}
