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
	if apiErr.Method != http.MethodGet || apiErr.Path != "/api/chats/agents/missing" {
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

func TestListAgentsReadsChatsEnvelope(t *testing.T) {
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/chats/agents" {
			t.Errorf("request = %s %s, want GET /api/chats/agents", r.Method, r.URL.Path)
		}
		_, _ = io.WriteString(w, `{"agents":[{"id":"a1","name":"reviewer","skills":["review"],"mcp_servers":[],"addons":[],"collections":["docs"],"network":"off","created":"2026-11-14T00:00:00Z","updated":"2026-11-14T00:00:00Z"}]}`)
	})
	agents, err := c.ListAgents(context.Background())
	if err != nil {
		t.Fatalf("ListAgents: %v", err)
	}
	if len(agents) != 1 || agents[0].ID != "a1" || agents[0].Name != "reviewer" || agents[0].Collections[0] != "docs" {
		t.Errorf("agents = %+v, want the one reviewer agent a1", agents)
	}
}

func TestCreateAgentSendsWriteBody(t *testing.T) {
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/chats/agents" {
			t.Errorf("request = %s %s, want POST /api/chats/agents", r.Method, r.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		if body["name"] != "reviewer" || body["model"] != "m1" {
			t.Errorf("body = %v, want name and model", body)
		}
		if _, ok := body["kb"]; ok {
			t.Errorf("body carries a kb key, which the chats API does not have")
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"id":"a2","name":"reviewer","model":"m1"}`)
	})
	out, err := c.CreateAgent(context.Background(), AgentWrite{Name: "reviewer", Model: "m1", Collections: []string{"docs"}})
	if err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	if out.ID != "a2" {
		t.Errorf("ID = %q, want a2", out.ID)
	}
}

func TestDeleteAgentUsesIDPath(t *testing.T) {
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete || r.URL.Path != "/api/chats/agents/a2" {
			t.Errorf("request = %s %s, want DELETE /api/chats/agents/a2", r.Method, r.URL.Path)
		}
		w.WriteHeader(http.StatusNoContent)
	})
	if err := c.DeleteAgent(context.Background(), "a2"); err != nil {
		t.Fatalf("DeleteAgent: %v", err)
	}
}

func TestGetSkillUsesLayerAndName(t *testing.T) {
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/chats/skills/personal/review" {
			t.Errorf("request = %s %s, want GET /api/chats/skills/personal/review", r.Method, r.URL.Path)
		}
		_, _ = io.WriteString(w, `{"name":"review","layer":"personal","description":"d","body":"text","editable":true}`)
	})
	s, err := c.GetSkill(context.Background(), "personal", "review")
	if err != nil {
		t.Fatalf("GetSkill: %v", err)
	}
	if s.Body != "text" || s.Layer != "personal" {
		t.Errorf("skill = %+v, want the personal review text", s)
	}
}

func TestUpdateSkillPutsDescriptionAndBody(t *testing.T) {
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut || r.URL.Path != "/api/chats/skills/shared/team" {
			t.Errorf("request = %s %s, want PUT /api/chats/skills/shared/team", r.Method, r.URL.Path)
		}
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["body"] != "new text" || body["description"] != "d2" {
			t.Errorf("body = %v", body)
		}
		_, _ = io.WriteString(w, `{"name":"team","layer":"shared","description":"d2","body":"new text","editable":true}`)
	})
	if _, err := c.UpdateSkill(context.Background(), "shared", "team", SkillUpdate{Description: "d2", Body: "new text"}); err != nil {
		t.Fatalf("UpdateSkill: %v", err)
	}
}
