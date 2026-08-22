package tools

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// listAgento11ySavedConversations lists bookmarked conversations. Its cursor is
// an opaque numeric keyset value, not the last saved_id the collection member
// route returns.
func (c *Client) listAgento11ySavedConversations(ctx context.Context, source string, limit int, cursor string) (*Agento11ySavedConversationsResponse, error) {
	query := agento11yPageQuery(limit, cursor)
	if source != "" {
		query.Set("source", source)
	}

	resp, err := fetchAgento11yJSON[Agento11ySavedConversationsResponse](ctx, c, http.MethodGet, "/eval/saved-conversations", query, nil)
	if err != nil {
		return nil, err
	}
	return &resp, nil
}

// agento11yBareNotFound is what a route served by Go's default 404 handler
// answers with: http.NotFound writes exactly "404 page not found\n". Matching
// the body and not only the status leaves a descriptive 404 from the plugin proxy
// (an uninstalled plugin, or a route an older plugin build does not have) alone.
const agento11yBareNotFound = "status 404: 404 page not found"

// getAgento11ySavedConversation fetches one bookmark. It is the only route in the
// set that answers a missing ID with a body naming neither the resource nor the
// ID, so that one body is remapped, with the cause wrapped. Every other route
// answers descriptively and is passed through unchanged.
func (c *Client) getAgento11ySavedConversation(ctx context.Context, savedID string) (*Agento11ySavedConversation, error) {
	resp, err := fetchAgento11yJSON[Agento11ySavedConversation](ctx, c, http.MethodGet, "/eval/saved-conversations/"+url.PathEscape(savedID), nil, nil)
	if err != nil {
		if strings.Contains(err.Error(), agento11yBareNotFound) {
			return nil, fmt.Errorf("saved conversation %q not found: %w", savedID, err)
		}
		return nil, err
	}
	return &resp, nil
}

// listAgento11yCollectionsForSavedConversation is the reverse lookup from one
// bookmark to its collections. It is unpaginated and always answers with an
// empty next_cursor. Prefer the collections embedded in list rows over calling
// this per row.
func (c *Client) listAgento11yCollectionsForSavedConversation(ctx context.Context, savedID string) (*agento11yListResponse[Agento11yCollection], error) {
	resp, err := fetchAgento11yJSON[agento11yListResponse[Agento11yCollection]](ctx, c, http.MethodGet, "/eval/saved-conversations/"+url.PathEscape(savedID)+"/collections", nil, nil)
	if err != nil {
		return nil, err
	}
	return &resp, nil
}

// listAgento11yCollections lists the collections of the tenant.
// Its cursor is the last collection_id of the page, not a row offset.
func (c *Client) listAgento11yCollections(ctx context.Context, limit int, cursor string) (*agento11yListResponse[Agento11yCollection], error) {
	resp, err := fetchAgento11yJSON[agento11yListResponse[Agento11yCollection]](ctx, c, http.MethodGet, "/eval/collections", agento11yPageQuery(limit, cursor), nil)
	if err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *Client) getAgento11yCollection(ctx context.Context, collectionID string) (*Agento11yCollection, error) {
	resp, err := fetchAgento11yJSON[Agento11yCollection](ctx, c, http.MethodGet, "/eval/collections/"+url.PathEscape(collectionID), nil, nil)
	if err != nil {
		return nil, err
	}
	return &resp, nil
}

// listAgento11yCollectionMembers lists the saved conversations of a collection.
// The items are the same enriched saved conversations the list route returns,
// and the cursor is the last saved_id of the page.
func (c *Client) listAgento11yCollectionMembers(ctx context.Context, collectionID string, limit int, cursor string) (*agento11yListResponse[Agento11ySavedConversation], error) {
	resp, err := fetchAgento11yJSON[agento11yListResponse[Agento11ySavedConversation]](ctx, c, http.MethodGet, "/eval/collections/"+url.PathEscape(collectionID)+"/members", agento11yPageQuery(limit, cursor), nil)
	if err != nil {
		return nil, err
	}
	return &resp, nil
}

// agento11yEvalCollectionRead runs the saved conversation and collection read
// operations shared by the read and read-write variants of
// agento11y_evals_read/agento11y_evals_write. Operations it does not handle return
// errAgento11yUnknownOperation.
func (c *Client) agento11yEvalCollectionRead(ctx context.Context, operation string, r agento11yEvalCollectionReadRequest) (any, error) {
	switch operation {
	case "list_saved_conversations":
		return c.listAgento11ySavedConversations(ctx, r.Source, r.Limit, r.Cursor)
	case "get_saved_conversation":
		return c.getAgento11ySavedConversation(ctx, r.SavedID)
	case "list_collections_for_saved_conversation":
		return c.listAgento11yCollectionsForSavedConversation(ctx, r.SavedID)
	case "list_collections":
		return c.listAgento11yCollections(ctx, r.Limit, r.Cursor)
	case "get_collection":
		return c.getAgento11yCollection(ctx, r.CollectionID)
	case "list_collection_members":
		return c.listAgento11yCollectionMembers(ctx, r.CollectionID, r.Limit, r.Cursor)
	default:
		return nil, errAgento11yUnknownOperation
	}
}

// saveAgento11yConversation bookmarks a live conversation. An omitted saved_id
// is derived by agento11yDerivedSavedID. saved_by is overwritten by the backend
// from the caller identity, so it is never sent.
func (c *Client) saveAgento11yConversation(ctx context.Context, savedID, conversationID, name string, tags map[string]string) (*Agento11ySavedConversation, error) {
	if savedID == "" {
		savedID = agento11yDerivedSavedID(conversationID)
	}

	body := map[string]any{
		"saved_id":        savedID,
		"conversation_id": conversationID,
		"name":            name,
	}
	if len(tags) > 0 {
		body["tags"] = tags
	}

	resp, err := fetchAgento11yJSON[Agento11ySavedConversation](ctx, c, http.MethodPost, "/eval/saved-conversations", nil, body)
	if err != nil {
		return nil, err
	}
	return &resp, nil
}

// deleteAgento11ySavedConversation removes a bookmark. The API answers 204 and
// is idempotent, and the delete also drops the bookmark from every collection
// it belonged to.
func (c *Client) deleteAgento11ySavedConversation(ctx context.Context, savedID string) error {
	_, err := c.fetchAgento11y(ctx, http.MethodDelete, "/eval/saved-conversations/"+url.PathEscape(savedID), nil, nil)
	return err
}

// createAgento11yCollection creates a collection. The collection ID is a
// server-assigned UUID and created_by comes from the caller identity, so
// neither is sent.
func (c *Client) createAgento11yCollection(ctx context.Context, name string, description *string) (*Agento11yCollection, error) {
	body := map[string]any{"name": name}
	if description != nil {
		body["description"] = *description
	}

	resp, err := fetchAgento11yJSON[Agento11yCollection](ctx, c, http.MethodPost, "/eval/collections", nil, body)
	if err != nil {
		return nil, err
	}
	return &resp, nil
}

// updateAgento11yCollection patches a collection. Only the fields the caller
// supplied are sent, because the API reads name and description as optional
// pointers: an absent field is left unchanged while an explicitly empty
// description clears it.
func (c *Client) updateAgento11yCollection(ctx context.Context, collectionID, name string, description *string) (*Agento11yCollection, error) {
	body := map[string]any{}
	if name != "" {
		body["name"] = name
	}
	if description != nil {
		body["description"] = *description
	}

	resp, err := fetchAgento11yJSON[Agento11yCollection](ctx, c, http.MethodPatch, "/eval/collections/"+url.PathEscape(collectionID), nil, body)
	if err != nil {
		return nil, err
	}
	return &resp, nil
}

// deleteAgento11yCollection removes a collection and its memberships in one
// transaction. The API answers 204 and is idempotent.
func (c *Client) deleteAgento11yCollection(ctx context.Context, collectionID string) error {
	_, err := c.fetchAgento11y(ctx, http.MethodDelete, "/eval/collections/"+url.PathEscape(collectionID), nil, nil)
	return err
}

// addAgento11yCollectionMembers adds saved conversations to a collection. The
// backend existence-checks every ID, inserts with INSERT IGNORE so re-adding a
// member is a no-op, and takes added_by from the caller identity, so it is not
// sent. The response is only {"status":"ok"} and carries no membership data.
func (c *Client) addAgento11yCollectionMembers(ctx context.Context, collectionID string, savedIDs []string) error {
	body := map[string]any{"saved_ids": savedIDs}
	_, err := c.fetchAgento11y(ctx, http.MethodPost, "/eval/collections/"+url.PathEscape(collectionID)+"/members", nil, body)
	return err
}

// removeAgento11yCollectionMember drops one saved conversation from a
// collection. The API answers 204 and is idempotent; the bookmark itself is
// untouched.
func (c *Client) removeAgento11yCollectionMember(ctx context.Context, collectionID, savedID string) error {
	_, err := c.fetchAgento11y(ctx, http.MethodDelete, "/eval/collections/"+url.PathEscape(collectionID)+"/members/"+url.PathEscape(savedID), nil, nil)
	return err
}

// agento11yEvalCollectionWriteRequest is the input of a saved conversation or
// collection write operation, assembled by agento11y_evals_write from its flat
// params.
type agento11yEvalCollectionWriteRequest struct {
	SavedID        string
	CollectionID   string
	ConversationID string
	Name           string
	Description    *string
	Tags           map[string]string
	SavedIDs       []string
}

// agento11yEvalCollectionWrite runs the write operations of
// agento11y_evals_write's saved conversation and collection sub-domain.
// Operations it does not handle return errAgento11yUnknownOperation.
func (c *Client) agento11yEvalCollectionWrite(ctx context.Context, operation string, p agento11yEvalCollectionWriteRequest) (any, error) {
	switch operation {
	case "save_conversation":
		return c.saveAgento11yConversation(ctx, p.SavedID, p.ConversationID, p.Name, p.Tags)
	case "delete_saved_conversation":
		if err := c.deleteAgento11ySavedConversation(ctx, p.SavedID); err != nil {
			return nil, err
		}
		return fmt.Sprintf("Saved conversation %s deleted successfully (and removed from every collection it belonged to)", p.SavedID), nil
	case "create_collection":
		return c.createAgento11yCollection(ctx, p.Name, p.Description)
	case "update_collection":
		return c.updateAgento11yCollection(ctx, p.CollectionID, p.Name, p.Description)
	case "delete_collection":
		if err := c.deleteAgento11yCollection(ctx, p.CollectionID); err != nil {
			return nil, err
		}
		return fmt.Sprintf("Collection %s deleted successfully", p.CollectionID), nil
	case "add_collection_members":
		if err := c.addAgento11yCollectionMembers(ctx, p.CollectionID, p.SavedIDs); err != nil {
			return nil, err
		}
		// Phrased without an "added" count: some of the IDs may already have been
		// members, and the response carries no membership data to tell.
		return fmt.Sprintf("Collection %s now contains the %d requested saved conversation(s)", p.CollectionID, len(p.SavedIDs)), nil
	case "remove_collection_member":
		if err := c.removeAgento11yCollectionMember(ctx, p.CollectionID, p.SavedID); err != nil {
			return nil, err
		}
		return fmt.Sprintf("Saved conversation %s removed from collection %s successfully", p.SavedID, p.CollectionID), nil
	default:
		return nil, errAgento11yUnknownOperation
	}
}
