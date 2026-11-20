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

// ProjectWrite is the body that creates or replaces a project.
type ProjectWrite struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Defaults    *ProjectDefaults `json:"defaults,omitempty"`
	Attached    *ProjectAttached `json:"attached,omitempty"`
}

// Project is a project as LocalAI reports it.
type Project struct {
	ID          string           `json:"id,omitempty"`
	Slug        string           `json:"slug,omitempty"`
	Name        string           `json:"name"`
	Description string           `json:"description,omitempty"`
	Defaults    *ProjectDefaults  `json:"defaults,omitempty"`
	Attached    *ProjectAttached  `json:"attached,omitempty"`
	ChatCount   int              `json:"chat_count,omitempty"`
	Status      string           `json:"status,omitempty"`
	Archived    bool             `json:"archived,omitempty"`
}

// ListProjects returns all projects of the API key's user.
func (c *Client) ListProjects(ctx context.Context) ([]Project, error) {
	var out []Project
	if err := c.do(ctx, http.MethodGet, "/api/chats/projects", nil, &out); err != nil {
		// Some endpoints wrap in an object
		var wrapped struct {
			Projects []Project `json:"projects"`
		}
		if err2 := c.do(ctx, http.MethodGet, "/api/chats/projects", nil, &wrapped); err2 != nil {
			return nil, err
		}
		return wrapped.Projects, nil
	}
	return out, nil
}

// GetProject returns a single project by id (slug).
func (c *Client) GetProject(ctx context.Context, id string) (*Project, error) {
	var out Project
	if err := c.do(ctx, http.MethodGet, "/api/chats/projects/"+url.PathEscape(id), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// CreateProject creates a new project.
func (c *Client) CreateProject(ctx context.Context, p ProjectWrite) (*Project, error) {
	var out Project
	if err := c.do(ctx, http.MethodPost, "/api/chats/projects", p, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// UpdateProject updates an existing project (PATCH).
func (c *Client) UpdateProject(ctx context.Context, id string, p ProjectWrite) (*Project, error) {
	var out Project
	if err := c.do(ctx, http.MethodPatch, "/api/chats/projects/"+url.PathEscape(id), p, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeleteProject deletes a project.
func (c *Client) DeleteProject(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodDelete, "/api/chats/projects/"+url.PathEscape(id), nil, nil)
}

// ArchiveProject archives a project.
func (c *Client) ArchiveProject(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodPost, "/api/chats/projects/"+url.PathEscape(id)+"/archive", nil, nil)
}

// RestoreProject restores an archived project.
func (c *Client) RestoreProject(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodPost, "/api/chats/projects/"+url.PathEscape(id)+"/restore", nil, nil)
}
