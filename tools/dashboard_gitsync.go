package tools

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"path"
	"slices"

	"github.com/grafana/grafana-openapi-client-go/models"
	mcpgrafana "github.com/grafana/mcp-grafana/v2"
)

// Git Sync marks every resource it owns with these annotations. Grafana
// rejects classic and Kubernetes API writes to such resources; they must be
// committed through the provisioning repository files API instead.
const (
	annoKeyManagedBy  = "grafana.app/managedBy"
	annoKeyManagerID  = "grafana.app/managerId"
	annoKeySourcePath = "grafana.app/sourcePath"

	folderAPIGroup   = "folder.grafana.app"
	folderAPIVersion = "v1beta1"
)

// repoSource locates a Git Sync managed resource: the repository slug and the
// file (or, for folders, directory) path inside it.
type repoSource struct {
	Repo string
	Path string
}

// repoSourceOf returns where a Kubernetes object lives in a provisioning
// repository, or nil when the object is not managed by one.
func repoSourceOf(obj map[string]interface{}) *repoSource {
	annotations := safeObject(safeObject(obj, "metadata"), "annotations")
	if safeString(annotations, annoKeyManagedBy) != "repo" {
		return nil
	}
	repo := safeString(annotations, annoKeyManagerID)
	if repo == "" {
		return nil
	}
	return &repoSource{Repo: repo, Path: safeString(annotations, annoKeySourcePath)}
}

// gitSyncCapable reports whether the dashboard.grafana.app API is served, the
// precondition for Git Sync (Grafana 12+).
func gitSyncCapable(ctx context.Context, k8s *mcpgrafana.KubernetesClient) (bool, error) {
	if k8s == nil {
		return false, nil
	}
	versions, err := k8s.GroupVersions(ctx, dashboardAPIGroup)
	if err != nil {
		return false, fmt.Errorf("determine %s capability: %w", dashboardAPIGroup, err)
	}
	return slices.Contains(versions, dashboardReadVersion), nil
}

// folderRepoSource returns the repository directory backing a folder, or nil
// when the folder is not managed by Git Sync.
func folderRepoSource(ctx context.Context, k8s *mcpgrafana.KubernetesClient, folderUID string) (*repoSource, error) {
	ns, err := mcpgrafana.GrafanaNamespace(ctx)
	if err != nil {
		return nil, err
	}
	desc := mcpgrafana.ResourceDescriptor{Group: folderAPIGroup, Version: folderAPIVersion, Resource: "folders"}
	folder, err := k8s.Get(ctx, desc, ns, folderUID)
	if isK8sNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get folder %q via k8s api: %w", folderUID, err)
	}
	return repoSourceOf(folder), nil
}

// saveDashboardToRepo commits a dashboard to its provisioning repository: PUT
// replaces an existing file, POST creates a new one. On the repository's
// configured branch Grafana applies the change immediately; on any other ref
// it only lands once that branch is merged.
func saveDashboardToRepo(ctx context.Context, method string, src repoSource, apiVersion, uid string, spec map[string]interface{}, args UpdateDashboardParams) (*models.PostDashboardOKBody, error) {
	spec = maps.Clone(spec)
	// Database identifiers have no meaning in a repository file.
	delete(spec, "id")
	delete(spec, "version")

	body, err := json.Marshal(map[string]interface{}{
		"apiVersion": dashboardAPIGroup + "/" + apiVersion,
		"kind":       "Dashboard",
		"metadata":   map[string]interface{}{"name": uid},
		"spec":       spec,
	})
	if err != nil {
		return nil, fmt.Errorf("encode dashboard: %w", err)
	}

	title := safeString(spec, "title")
	message := args.Message
	if message == "" {
		message = fmt.Sprintf("Update dashboard %q", title)
		if method == http.MethodPost {
			message = fmt.Sprintf("Create dashboard %q", title)
		}
	}
	query := url.Values{"message": []string{message}}
	if args.Ref != "" {
		query.Set("ref", args.Ref)
	}

	status, respBody, err := provisioningFileRequest(ctx, method, src.Repo, src.Path, query, body)
	if err != nil {
		return nil, err
	}
	if status/100 != 2 {
		return nil, fmt.Errorf("commit dashboard %q to repository %q at %s: %s", uid, src.Repo, src.Path, provisioningErrorMessage(status, respBody))
	}

	result := "success"
	if args.Ref != "" {
		result = fmt.Sprintf("committed to branch %q of repository %q at %s; Grafana applies it when the branch is merged", args.Ref, src.Repo, src.Path)
	}
	return &models.PostDashboardOKBody{UID: &uid, Title: &title, Status: &result}, nil
}

// saveFullJSONToRepoIfManaged routes a full-JSON save through Git Sync when
// the target dashboard or folder is repository-managed. handled is false when
// neither is, and the caller should use the regular save path.
func saveFullJSONToRepoIfManaged(ctx context.Context, args UpdateDashboardParams) (result *models.PostDashboardOKBody, handled bool, err error) {
	k8s := mcpgrafana.KubernetesClientFromContext(ctx)
	if ok, err := gitSyncCapable(ctx, k8s); !ok || err != nil {
		return nil, false, err
	}

	isV2 := isV2DashboardJSON(args.Dashboard)
	uid, _ := args.Dashboard["uid"].(string)
	spec := maps.Clone(args.Dashboard)
	if isV2 {
		delete(spec, "uid")
	}

	var existing *dashboardResult
	if uid != "" {
		existing, err = fetchDashboardViaK8s(ctx, k8s, uid, true)
		switch {
		case isK8sNotFound(err):
			existing = nil
		case err != nil:
			return nil, false, fmt.Errorf("check existing dashboard %s: %w", uid, err)
		}
	}

	if existing != nil {
		if src := repoSourceOf(existing.Object); src != nil {
			if !args.Overwrite {
				return nil, true, fmt.Errorf("dashboard %q already exists in repository %q; set overwrite=true to replace it", uid, src.Repo)
			}
			if isV2 != existing.IsV2 {
				return nil, true, fmt.Errorf("dashboard %q is stored as %s; send a body in the same schema (fetch it with get_dashboard_by_uid)", uid, existing.APIVersion)
			}
			if args.FolderUID != "" && existing.Meta != nil && args.FolderUID != existing.Meta.FolderUID {
				return nil, true, fmt.Errorf("dashboard %q is managed by repository %q; moving it to another folder means moving its file, which update_dashboard does not do", uid, src.Repo)
			}
			res, err := saveDashboardToRepo(ctx, http.MethodPut, *src, existing.APIVersion, uid, spec, args)
			return res, true, err
		}
	}

	if args.FolderUID == "" {
		return nil, false, nil
	}
	folderSrc, err := folderRepoSource(ctx, k8s, args.FolderUID)
	if err != nil || folderSrc == nil {
		return nil, false, err
	}
	if existing != nil {
		return nil, true, fmt.Errorf("dashboard %q already exists outside repository %q, and Git Sync will not take over an unmanaged dashboard. Delete the existing dashboard first, or save under a new uid", uid, folderSrc.Repo)
	}

	apiVersion := dashboardReadVersion
	if isV2 {
		versions, err := k8s.GroupVersions(ctx, dashboardAPIGroup)
		if err != nil {
			return nil, true, fmt.Errorf("determine %s capability: %w", dashboardAPIGroup, err)
		}
		if apiVersion = preferredV2Version(versions); apiVersion == "" {
			return nil, true, fmt.Errorf("this Grafana does not serve a v2 version of %s, so a v2 dashboard cannot be created", dashboardAPIGroup)
		}
	}
	if uid == "" {
		uid = newShortUID()
		if !isV2 {
			spec["uid"] = uid
		}
	}
	src := repoSource{Repo: folderSrc.Repo, Path: path.Join(folderSrc.Path, uid+".json")}
	res, err := saveDashboardToRepo(ctx, http.MethodPost, src, apiVersion, uid, spec, args)
	return res, true, err
}

// newShortUID returns a random 14-character lowercase alphanumeric uid, the
// same shape Grafana generates.
func newShortUID() string {
	const alphabet = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, 14)
	_, _ = rand.Read(b)
	for i := range b {
		b[i] = alphabet[int(b[i])%len(alphabet)]
	}
	return string(b)
}
