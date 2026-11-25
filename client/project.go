package client

import (
	"context"
	"net/http"
	"net/url"
)

// ProjectDefaults holds the default tool lists for new chats in a project.
type ProjectDefaults struct {
	Skills      []string `json:"skills,omitempty"`
	Addons      []string `json:"addons,omitempty"`
	Connectors  []string `json:"connectors,omitempty"`
	MCPServers  []string `json:"mcp_servers,omitempty"`
	Collections []string `json:"collections,omitempty"`
}

// ProjectAttached holds the always-on (attached) tool lists for a project.
type ProjectAttached struct {
	Skills      []string `json:"skills,omitempty"`
	Addons      []string `json:"addons,omitempty"`
	Connectors  []string `json:"connectors,omitempty"`
	MCPServers  []string `json:"mcp_servers,omitempty"`
	Collections []string `json:"collections,omitempty"`
}

// ProjectAccessGrant gives an OIDC group access to a project.
type ProjectAccessGrant struct {
	Name string `json:"name"`
	Mode string `json:"mode,omitempty"` // read or write
}

// ProjectAccess declares access grants for a project.
type ProjectAccess struct {
	Groups []ProjectAccessGrant `json:"groups,omitempty"`
}

// ProjectWrite is the body that creates or replaces a project.
type ProjectWrite struct {
	Name        string           `json:"name"`
	Description string           `json:"description,omitempty"`
	Defaults    *ProjectDefaults `json:"defaults,omitempty"`
	Attached    *ProjectAttached `json:"attached,omitempty"`
	Access      *ProjectAccess   `json:"access,omitempty"`
}

// Project is a project as LocalAI reports it.
type Project struct {
	ID          string           `json:"id,omitempty"`
	Slug        string           `json:"slug,omitempty"`
	Name        string           `json:"name"`
	Description string           `json:"description,omitempty"`
	Defaults    *ProjectDefaults `json:"defaults,omitempty"`
	Attached    *ProjectAttached `json:"attached,omitempty"`
	Access      *ProjectAccess   `json:"access,omitempty"`
	ChatCount   int              `json:"chat_count,omitempty"`
	Status      string           `json:"status,omitempty"`
	Archived    bool             `json:"archived,omitempty"`
	Owner       string           `json:"owner,omitempty"`
}

func projectPath(path, userID string) string {
	if userID == "" {
		return path
	}
	v := url.Values{}
	v.Set("user_id", userID)
	return path + "?" + v.Encode()
}

// ListProjects returns all projects of the API key's user. When userID is set,
// an administrator-scoped request is made on behalf of that user.
func (c *Client) ListProjects(ctx context.Context, userID string) ([]Project, error) {
	var out []Project
	path := projectPath("/api/chats/projects", userID)
	if err := c.do(ctx, http.MethodGet, path, nil, &out); err != nil {
		// Some endpoints wrap in an object
		var wrapped struct {
			Projects []Project `json:"projects"`
		}
		if err2 := c.do(ctx, http.MethodGet, path, nil, &wrapped); err2 != nil {
			return nil, err
		}
		return wrapped.Projects, nil
	}
	return out, nil
}

// GetProject returns a single project by id (slug).
func (c *Client) GetProject(ctx context.Context, id, userID string) (*Project, error) {
	var out Project
	if err := c.do(ctx, http.MethodGet, projectPath("/api/chats/projects/"+url.PathEscape(id), userID), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// CreateProject creates a new project.
func (c *Client) CreateProject(ctx context.Context, p ProjectWrite, userID string) (*Project, error) {
	var out Project
	if err := c.do(ctx, http.MethodPost, projectPath("/api/chats/projects", userID), p, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// UpdateProject updates an existing project (PATCH).
func (c *Client) UpdateProject(ctx context.Context, id string, p ProjectWrite, userID string) (*Project, error) {
	var out Project
	if err := c.do(ctx, http.MethodPatch, projectPath("/api/chats/projects/"+url.PathEscape(id), userID), p, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeleteProject deletes a project.
func (c *Client) DeleteProject(ctx context.Context, id, userID string) error {
	return c.do(ctx, http.MethodDelete, projectPath("/api/chats/projects/"+url.PathEscape(id), userID), nil, nil)
}

// ArchiveProject archives a project.
func (c *Client) ArchiveProject(ctx context.Context, id, userID string) error {
	return c.do(ctx, http.MethodPost, projectPath("/api/chats/projects/"+url.PathEscape(id)+"/archive", userID), nil, nil)
}

// RestoreProject restores an archived project.
func (c *Client) RestoreProject(ctx context.Context, id, userID string) error {
	return c.do(ctx, http.MethodPost, projectPath("/api/chats/projects/"+url.PathEscape(id)+"/restore", userID), nil, nil)
}
