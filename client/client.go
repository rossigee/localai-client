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
	defer resp.Body.Close()
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

// MCPServer is a remote MCP endpoint attached to an agent.
type MCPServer struct {
	URL   string `json:"url"`
	Token string `json:"token,omitempty"`
}

// Agent represents a LocalAI agent definition.
type Agent struct {
	Name         string `json:"name"`
	Model        string `json:"model"`
	Description  string `json:"description,omitempty"`
	SystemPrompt string `json:"system_prompt,omitempty"`
	// SelectedSkills is LocalAI's per-agent skill selection. The field is named
	// selected_skills in the API; a plain "skills" key is ignored server-side.
	SelectedSkills []string    `json:"selected_skills,omitempty"`
	EnableSkills   *bool       `json:"enable_skills,omitempty"`
	MCPServers     []MCPServer `json:"mcp_servers,omitempty"`
	AllowedTools   []string    `json:"allowed_tools,omitempty"`
	ExcludedTools  []string    `json:"excluded_tools,omitempty"`
	// APIKey is the key the agent uses to call LocalAI's own API. When LocalAI
	// has authentication enabled it generates a per-user key at creation time,
	// but only for callers that have a row in the users table. Callers
	// authenticated with a legacy API key have no such row, so the key has to
	// be supplied explicitly or creation fails with a user_api_keys foreign key
	// violation.
	APIKey string `json:"api_key,omitempty"`
	// EnableKB turns on the knowledge base for this agent.
	EnableKB *bool `json:"enable_kb,omitempty"`
	// KbAsTools exposes the knowledge base as MCP tools.
	KbAsTools *bool `json:"kb_as_tools,omitempty"`
	// LongTermMemory enables long-term memory (notes) for this agent.
	LongTermMemory *bool `json:"long_term_memory,omitempty"`
	// SummaryLongTermMemory enables summary of long-term memory.
	SummaryLongTermMemory *bool `json:"summary_long_term_memory,omitempty"`
	// KbAutoSearch enables automatic KB search.
	KbAutoSearch *bool `json:"kb_auto_search,omitempty"`
	// Raw holds any extra fields returned by the API.
	Raw map[string]any `json:"-"`
}

// ListAgents returns all agents.
func (c *Client) ListAgents(ctx context.Context) ([]Agent, error) {
	// The list endpoint returns {"agents": ["name", ...], "agentCount": n}.
	var resp struct {
		Agents []string `json:"agents"`
	}
	if err := c.do(ctx, http.MethodGet, "/api/agents", nil, &resp); err != nil {
		return nil, err
	}
	out := make([]Agent, 0, len(resp.Agents))
	for _, name := range resp.Agents {
		out = append(out, Agent{Name: name})
	}
	return out, nil
}

// GetAgent returns a single agent.
func (c *Client) GetAgent(ctx context.Context, name string) (*Agent, error) {
	// /api/agents/{name} only reports whether the agent is active; the config
	// lives at /api/agents/{name}/config.
	var out Agent
	if err := c.do(ctx, http.MethodGet, "/api/agents/"+url.PathEscape(name)+"/config", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// CreateAgent creates a new agent.
func (c *Client) CreateAgent(ctx context.Context, a Agent) (*Agent, error) {
	var out Agent
	if err := c.do(ctx, http.MethodPost, "/api/agents", a, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// UpdateAgent updates an existing agent.
func (c *Client) UpdateAgent(ctx context.Context, name string, a Agent) (*Agent, error) {
	var out Agent
	if err := c.do(ctx, http.MethodPut, "/api/agents/"+url.PathEscape(name), a, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeleteAgent deletes an agent.
func (c *Client) DeleteAgent(ctx context.Context, name string) error {
	return c.do(ctx, http.MethodDelete, "/api/agents/"+url.PathEscape(name), nil, nil)
}

// Skill represents a LocalAI skill definition.
type Skill struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	// Content is the skill's instructions, returned and accepted as "content".
	Content string `json:"content,omitempty"`
	// Raw holds any extra fields.
	Raw map[string]any `json:"-"`
}

// ListSkills returns all skills.
func (c *Client) ListSkills(ctx context.Context) ([]Skill, error) {
	var out []Skill
	if err := c.do(ctx, http.MethodGet, "/api/agents/skills", nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// GetSkill returns a single skill.
func (c *Client) GetSkill(ctx context.Context, name string) (*Skill, error) {
	var out Skill
	if err := c.do(ctx, http.MethodGet, "/api/agents/skills/"+url.PathEscape(name), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// CreateSkill creates a new skill.
func (c *Client) CreateSkill(ctx context.Context, s Skill) (*Skill, error) {
	var out Skill
	if err := c.do(ctx, http.MethodPost, "/api/agents/skills", s, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// UpdateSkill updates an existing skill.
func (c *Client) UpdateSkill(ctx context.Context, name string, s Skill) (*Skill, error) {
	var out Skill
	if err := c.do(ctx, http.MethodPut, "/api/agents/skills/"+url.PathEscape(name), s, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeleteSkill deletes a skill.
func (c *Client) DeleteSkill(ctx context.Context, name string) error {
	return c.do(ctx, http.MethodDelete, "/api/agents/skills/"+url.PathEscape(name), nil, nil)
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
