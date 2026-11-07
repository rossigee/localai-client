package client

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func testClient(t *testing.T, h http.HandlerFunc) (*Client, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	c, err := New(context.Background(), Config{BaseURL: srv.URL, APIKey: "test-key"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c, srv
}

func TestNewRequiresCredentials(t *testing.T) {
	if _, err := New(context.Background(), Config{BaseURL: "https://localai.example.com"}); err == nil {
		t.Fatal("expected an error when no credential is configured")
	}
	if _, err := New(context.Background(), Config{
		BaseURL:  "https://localai.example.com",
		TokenURL: "https://id.example.com/token",
	}); err == nil {
		t.Fatal("expected an error when the OAuth2 set is incomplete")
	}
}

func TestNewRejectsInvalidRootCA(t *testing.T) {
	_, err := New(context.Background(), Config{
		BaseURL:           "https://localai.example.com",
		APIKey:            "test-key",
		RootCACertificate: "not a certificate",
	})
	if err == nil {
		t.Fatal("expected an error for a root CA that holds no certificates")
	}
}

func TestListAgentsParsesEnvelope(t *testing.T) {
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/agents" {
			t.Errorf("path = %q, want /api/agents", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Errorf("Authorization = %q, want %q", got, "Bearer test-key")
		}
		_, _ = io.WriteString(w, `{"agents":["bookkeeping","maintenance-manager"],"agentCount":2}`)
	})

	agents, err := c.ListAgents(context.Background())
	if err != nil {
		t.Fatalf("ListAgents: %v", err)
	}
	if len(agents) != 2 {
		t.Fatalf("got %d agents, want 2", len(agents))
	}
	if agents[0].Name != "bookkeeping" || agents[1].Name != "maintenance-manager" {
		t.Errorf("agent names = %q, %q", agents[0].Name, agents[1].Name)
	}
}

func TestGetAgentReadsConfigEndpoint(t *testing.T) {
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/agents/bookkeeping/config" {
			t.Errorf("path = %q, want /api/agents/bookkeeping/config", r.URL.Path)
		}
		_, _ = io.WriteString(w, `{
			"name": "bookkeeping",
			"model": "qwen3-8b",
			"description": "Bookkeeping via Odoo MCP only.",
			"system_prompt": "You keep the books.",
			"selected_skills": ["golder-bookkeeping"],
			"enable_skills": true,
			"mcp_servers": [{"url": "http://odoo-mcp.localai.svc.cluster.local:8080/stream"}]
		}`)
	})

	agent, err := c.GetAgent(context.Background(), "bookkeeping")
	if err != nil {
		t.Fatalf("GetAgent: %v", err)
	}
	if agent.Model != "qwen3-8b" {
		t.Errorf("model = %q, want qwen3-8b", agent.Model)
	}
	if agent.SystemPrompt != "You keep the books." {
		t.Errorf("system prompt = %q", agent.SystemPrompt)
	}
	if len(agent.SelectedSkills) != 1 || agent.SelectedSkills[0] != "golder-bookkeeping" {
		t.Errorf("selected skills = %v", agent.SelectedSkills)
	}
	if agent.EnableSkills == nil || !*agent.EnableSkills {
		t.Errorf("enable skills = %v, want true", agent.EnableSkills)
	}
	if len(agent.MCPServers) != 1 || agent.MCPServers[0].URL == "" {
		t.Errorf("mcp servers = %v", agent.MCPServers)
	}
}

func TestCreateAgentSendsAPIBody(t *testing.T) {
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/agents" {
			t.Errorf("request = %s %s, want POST /api/agents", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Content-Type"); got != "application/json" {
			t.Errorf("Content-Type = %q, want application/json", got)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		if body["api_key"] != "agent-key" {
			t.Errorf("api_key = %v, want agent-key", body["api_key"])
		}
		skills, ok := body["selected_skills"].([]any)
		if !ok || len(skills) != 1 {
			t.Errorf("selected_skills = %v", body["selected_skills"])
		}
		_, _ = io.WriteString(w, `{"name":"bookkeeping"}`)
	})

	enabled := true
	if _, err := c.CreateAgent(context.Background(), Agent{
		Name:           "bookkeeping",
		Model:          "qwen3-8b",
		SelectedSkills: []string{"golder-bookkeeping"},
		EnableSkills:   &enabled,
		APIKey:         "agent-key",
	}); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
}

func TestIsNotFound(t *testing.T) {
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, "not found")
	})

	_, err := c.GetAgent(context.Background(), "missing")
	if err == nil {
		t.Fatal("expected an error for a 404 response")
	}
	if !IsNotFound(err) {
		t.Errorf("IsNotFound(%v) = false, want true", err)
	}

	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error is not an *APIError: %v", err)
	}
	if apiErr.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", apiErr.StatusCode)
	}
	if apiErr.Method != http.MethodGet || apiErr.Path != "/api/agents/missing/config" {
		t.Errorf("error carries %s %s", apiErr.Method, apiErr.Path)
	}
}

func TestServerErrorIsNotNotFound(t *testing.T) {
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, "boom")
	})

	_, err := c.GetAgent(context.Background(), "bookkeeping")
	if err == nil {
		t.Fatal("expected an error for a 500 response")
	}
	if IsNotFound(err) {
		t.Error("IsNotFound = true for a 500 response, want false")
	}
}

func TestSkillContentUsesContentKey(t *testing.T) {
	b, err := json.Marshal(Skill{Name: "golder-test", Content: "instructions"})
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if got["content"] != "instructions" {
		t.Errorf("content = %v, want instructions; body = %s", got["content"], b)
	}
}

func TestImportModelSendsConfigVerbatim(t *testing.T) {
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/models/import" {
			t.Errorf("request = %s %s, want POST /models/import", r.Method, r.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		if body["name"] != "zen-qwen3.8-flash" {
			t.Errorf("name = %v, want zen-qwen3.8-flash", body["name"])
		}
		if body["backend"] != "localai-proxy" {
			t.Errorf("backend = %v, want localai-proxy", body["backend"])
		}
		params, ok := body["parameters"].(map[string]any)
		if !ok || params["model"] != "qwen3.8-flash" {
			t.Errorf("parameters = %v", body["parameters"])
		}
		_, _ = io.WriteString(w, `{"success":true,"message":"Model configuration created successfully","filename":"zen-qwen3.8-flash.yaml","pending_cleanup":0}`)
	})

	res, err := c.ImportModel(context.Background(), map[string]any{
		"name":       "zen-qwen3.8-flash",
		"backend":    "localai-proxy",
		"parameters": map[string]any{"model": "qwen3.8-flash"},
	})
	if err != nil {
		t.Fatalf("ImportModel: %v", err)
	}
	if !res.Success || res.Filename != "zen-qwen3.8-flash.yaml" {
		t.Errorf("result = %+v, want success with zen-qwen3.8-flash.yaml", res)
	}
}

func TestImportModelAsyncJobEnvelope(t *testing.T) {
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"uuid":"job-1","status":"https://localai.example.com/models/jobs/job-1"}`)
	})

	res, err := c.ImportModel(context.Background(), map[string]any{"name": "m"})
	if err != nil {
		t.Fatalf("ImportModel: %v", err)
	}
	if res.Success || res.UUID != "job-1" {
		t.Errorf("result = %+v, want queued job envelope", res)
	}
}

func TestDeleteModelPostsToDeletePath(t *testing.T) {
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/models/delete/zen-qwen3.8-flash" {
			t.Errorf("request = %s %s, want POST /models/delete/zen-qwen3.8-flash", r.Method, r.URL.Path)
		}
		_, _ = io.WriteString(w, `{"uuid":"job-2","status":"https://localai.example.com/models/jobs/job-2","deletion":true,"processed":true,"message":"completed"}`)
	})

	res, err := c.DeleteModel(context.Background(), "zen-qwen3.8-flash")
	if err != nil {
		t.Fatalf("DeleteModel: %v", err)
	}
	if !res.Deletion || res.UUID != "job-2" {
		t.Errorf("result = %+v, want deletion job envelope", res)
	}
}

func TestDeleteModelErrorIsNotNotFound(t *testing.T) {
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, "boom")
	})

	if _, err := c.DeleteModel(context.Background(), "zen-qwen3.8-flash"); err == nil {
		t.Fatal("expected an error for a 500 response")
	}
}
