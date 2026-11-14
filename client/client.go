package client

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/clientcredentials"
)

// Config holds connection settings for a LocalAI instance.
//
// APIKey is the preferred credential: LocalAI only accepts API keys (legacy
// LOCALAI_API_KEY or per-user keys) or browser session IDs on its API, so an
// OIDC access token obtained through the client credentials grant is rejected
// with "An authentication key is required". TokenURL/ClientID/ClientSecret are
// retained only for deployments that front LocalAI with something that does
// accept OAuth2 bearer tokens.
type Config struct {
	BaseURL               string
	APIKey                string
	TokenURL              string
	ClientID              string
	ClientSecret          string
	RootCACertificate     string
	TLSInsecureSkipVerify bool
}

// Client is a LocalAI admin API client.
type Client struct {
	cfg  Config
	http *http.Client
}

// New creates a Client using client-credentials OAuth2 against Keycloak.
// If RootCACertificate is set, a custom TLS pool is used to verify the server
// certificate instead of the system pool.
func New(ctx context.Context, cfg Config) (*Client, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	cfg.APIKey = strings.TrimSpace(cfg.APIKey)

	hasAPIKey := cfg.APIKey != ""
	hasOAuth := cfg.TokenURL != "" && cfg.ClientID != "" && cfg.ClientSecret != ""
	if !hasAPIKey && !hasOAuth {
		return nil, fmt.Errorf("either an API key or tokenURL/clientID/clientSecret must be configured")
	}

	tlsConfig := &tls.Config{
		MinVersion:         tls.VersionTLS12,
		InsecureSkipVerify: cfg.TLSInsecureSkipVerify,
	}
	if cfg.RootCACertificate != "" {
		pool, err := x509.SystemCertPool()
		if err != nil {
			return nil, fmt.Errorf("cannot load system CA pool: %w", err)
		}
		if !pool.AppendCertsFromPEM([]byte(cfg.RootCACertificate)) {
			return nil, fmt.Errorf("rootCACertificate contains no valid PEM certificates")
		}
		tlsConfig.RootCAs = pool
	}

	base := &http.Client{Transport: &http.Transport{TLSClientConfig: tlsConfig}}

	// A static API key is attached per request by do(). It does not need an
	// OAuth2 token source, so the TLS transport is used as-is.
	if hasAPIKey {
		return &Client{cfg: cfg, http: base}, nil
	}

	// The oauth2 token source borrows the transport from the context, and the
	// same transport is then used for the API calls, so both the token request
	// and the API request verify against the configured pool.
	oauthCfg := clientcredentials.Config{
		ClientID:     cfg.ClientID,
		ClientSecret: cfg.ClientSecret,
		TokenURL:     cfg.TokenURL,
	}

	return &Client{
		cfg:  cfg,
		http: oauthCfg.Client(context.WithValue(ctx, oauth2.HTTPClient, base)),
	}, nil
}

func (c *Client) do(ctx context.Context, method, path string, body any, out any) error {
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(c.cfg.BaseURL, "/")+path, r)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.cfg.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.cfg.APIKey)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &APIError{Method: method, Path: path, StatusCode: resp.StatusCode, Body: string(raw)}
	}
	if out != nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, out); err != nil {
			return fmt.Errorf("decode: %w: %s", err, string(raw))
		}
	}
	return nil
}

// APIError is returned when LocalAI answers with a non-2xx status.
type APIError struct {
	Method     string
	Path       string
	StatusCode int
	Body       string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("localai %s %s: %d: %s", e.Method, e.Path, e.StatusCode, e.Body)
}

// IsNotFound reports whether err is a LocalAI 404, which means the object does
// not exist rather than that the request failed.
func IsNotFound(err error) bool {
	var apiErr *APIError
	return errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusNotFound
}

// AgentFolderWrite chooses the folder a saved agent works in.
type AgentFolderWrite struct {
	Path string `json:"path"`
	Mode string `json:"mode,omitempty"`
}

// AgentFolder is the folder of an agent as LocalAI reports it.
type AgentFolder struct {
	Path  string `json:"path,omitempty"`
	Name  string `json:"name,omitempty"`
	Mode  string `json:"mode"`
	State string `json:"state"`
}

// AgentWrite is the body that creates or replaces a saved agent.
type AgentWrite struct {
	Name         string            `json:"name"`
	Description  string            `json:"description,omitempty"`
	Instructions string            `json:"instructions,omitempty"`
	Model        string            `json:"model,omitempty"`
	MCPServers   []string          `json:"mcp_servers,omitempty"`
	Addons       []string          `json:"addons,omitempty"`
	Collections  []string          `json:"collections,omitempty"`
	Skills       []string          `json:"skills,omitempty"`
	Network      string            `json:"network,omitempty"`
	Folder       *AgentFolderWrite `json:"folder,omitempty"`
}

// Agent is a saved agent as LocalAI reports it. ID identifies the agent in
// every later call.
type Agent struct {
	ID           string       `json:"id"`
	Name         string       `json:"name"`
	Description  string       `json:"description"`
	Instructions string       `json:"instructions"`
	Model        string       `json:"model,omitempty"`
	MCPServers   []string     `json:"mcp_servers"`
	Addons       []string     `json:"addons"`
	Collections  []string     `json:"collections"`
	Skills       []string     `json:"skills"`
	Network      string       `json:"network"`
	Folder       *AgentFolder `json:"folder,omitempty"`
	Created      time.Time    `json:"created"`
	Updated      time.Time    `json:"updated"`
}

// ListAgents returns the saved agents of the API key's user.
func (c *Client) ListAgents(ctx context.Context) ([]Agent, error) {
	var resp struct {
		Agents []Agent `json:"agents"`
	}
	if err := c.do(ctx, http.MethodGet, "/api/chats/agents", nil, &resp); err != nil {
		return nil, err
	}
	return resp.Agents, nil
}

// GetAgent returns one saved agent by ID.
func (c *Client) GetAgent(ctx context.Context, id string) (*Agent, error) {
	var out Agent
	if err := c.do(ctx, http.MethodGet, "/api/chats/agents/"+url.PathEscape(id), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// CreateAgent saves a new agent.
func (c *Client) CreateAgent(ctx context.Context, a AgentWrite) (*Agent, error) {
	var out Agent
	if err := c.do(ctx, http.MethodPost, "/api/chats/agents", a, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// UpdateAgent replaces the saved agent with the given ID.
func (c *Client) UpdateAgent(ctx context.Context, id string, a AgentWrite) (*Agent, error) {
	var out Agent
	if err := c.do(ctx, http.MethodPut, "/api/chats/agents/"+url.PathEscape(id), a, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeleteAgent deletes the saved agent with the given ID.
func (c *Client) DeleteAgent(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodDelete, "/api/chats/agents/"+url.PathEscape(id), nil, nil)
}

// Skill is one skill in the list, as LocalAI reports it.
type Skill struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Layer       string `json:"layer"`
	Addon       string `json:"addon,omitempty"`
	AddonTitle  string `json:"addon_title,omitempty"`
	Path        string `json:"path"`
	Enabled     bool   `json:"enabled"`
	Shadowed    string `json:"shadowed,omitempty"`
	ShadowedBy  string `json:"shadowed_by,omitempty"`
	Fallback    string `json:"fallback,omitempty"`
	Invalid     string `json:"invalid,omitempty"`
	Editable    bool   `json:"editable"`
}

// SkillDetail is one skill with its text.
type SkillDetail struct {
	Name        string `json:"name"`
	Layer       string `json:"layer"`
	Description string `json:"description"`
	Body        string `json:"body"`
	Editable    bool   `json:"editable"`
}

// SkillWrite creates a skill. Layer is "personal" or "shared".
type SkillWrite struct {
	Layer       string `json:"layer"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Body        string `json:"body"`
}

// SkillUpdate replaces the description and body of a skill.
type SkillUpdate struct {
	Description string `json:"description"`
	Body        string `json:"body"`
}

// ListSkills returns the skills the API key's user can see.
func (c *Client) ListSkills(ctx context.Context) ([]Skill, error) {
	var resp struct {
		Skills []Skill `json:"skills"`
	}
	if err := c.do(ctx, http.MethodGet, "/api/chats/skills", nil, &resp); err != nil {
		return nil, err
	}
	return resp.Skills, nil
}

// GetSkill returns one skill with its text. Layer is "personal" or "shared".
func (c *Client) GetSkill(ctx context.Context, layer, name string) (*SkillDetail, error) {
	var out SkillDetail
	if err := c.do(ctx, http.MethodGet, skillPath(layer, name), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// CreateSkill saves a new skill.
func (c *Client) CreateSkill(ctx context.Context, s SkillWrite) (*SkillDetail, error) {
	var out SkillDetail
	if err := c.do(ctx, http.MethodPost, "/api/chats/skills", s, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// UpdateSkill replaces the description and body of a skill.
func (c *Client) UpdateSkill(ctx context.Context, layer, name string, s SkillUpdate) (*SkillDetail, error) {
	var out SkillDetail
	if err := c.do(ctx, http.MethodPut, skillPath(layer, name), s, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeleteSkill deletes a skill.
func (c *Client) DeleteSkill(ctx context.Context, layer, name string) error {
	return c.do(ctx, http.MethodDelete, skillPath(layer, name), nil, nil)
}

func skillPath(layer, name string) string {
	return "/api/chats/skills/" + url.PathEscape(layer) + "/" + url.PathEscape(name)
}

// Ping checks connectivity.
func (c *Client) Ping(ctx context.Context) error {
	_, err := c.ListAgents(ctx)
	return err
}

var _ = time.Second

// Model represents a LocalAI model (minimal).
type Model struct {
	Name string `json:"id"`
}

// ListModels returns available models.
func (c *Client) ListModels(ctx context.Context) ([]Model, error) {
	var out struct {
		Data []Model `json:"data"`
	}
	if err := c.do(ctx, http.MethodGet, "/v1/models", nil, &out); err != nil {
		return nil, err
	}
	return out.Data, nil
}

// ImportModelResult is the response for POST /models/import. A config with no
// downloads is written synchronously and answered with Success true. A config
// that references remote assets (download_files entries, URI-valued
// model/mmproj) is enqueued on the gallery job queue and answered instead
// with a job envelope (UUID/Status), like a gallery install.
type ImportModelResult struct {
	Success  bool   `json:"success"`
	Message  string `json:"message"`
	Filename string `json:"filename"`
	// UUID and Status are set when the import was queued as a job.
	UUID   string `json:"uuid"`
	Status string `json:"status"`
}

// ImportModel writes a model configuration to LocalAI (POST /models/import).
// The map is sent verbatim as the config body; LocalAI writes it to
// /models/<name>.yaml and the model appears in the model list. Re-importing
// an existing name overwrites its configuration.
func (c *Client) ImportModel(ctx context.Context, cfg map[string]any) (*ImportModelResult, error) {
	var out ImportModelResult
	if err := c.do(ctx, http.MethodPost, "/models/import", cfg, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeleteModelResult is the job envelope LocalAI returns for asynchronous
// model operations (delete, or apply of a config that carries downloads).
type DeleteModelResult struct {
	UUID               string `json:"uuid"`
	Status             string `json:"status"`
	Deletion           bool   `json:"deletion"`
	Processed          bool   `json:"processed"`
	Message            string `json:"message"`
	GalleryElementName string `json:"gallery_element_name"`
}

// DeleteModel deletes a model from LocalAI (POST /models/delete/{name}).
// Deletion is asynchronous: LocalAI returns a job envelope and the model
// disappears from the model list once the job completes. Deleting a model
// that does not exist also returns a job envelope.
func (c *Client) DeleteModel(ctx context.Context, name string) (*DeleteModelResult, error) {
	var out DeleteModelResult
	if err := c.do(ctx, http.MethodPost, "/models/delete/"+url.PathEscape(name), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
