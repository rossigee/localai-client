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

	_, err := c.GetAgent(context.Background(), "missing", "")
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

	_, err := c.GetAgent(context.Background(), "bookkeeping", "")
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
	agents, err := c.ListAgents(context.Background(), "")
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
	out, err := c.CreateAgent(context.Background(), AgentWrite{Name: "reviewer", Model: "m1", Collections: []string{"docs"}}, "")
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
	if err := c.DeleteAgent(context.Background(), "a2", ""); err != nil {
		t.Fatalf("DeleteAgent: %v", err)
	}
}

func TestAgentOwnerScopeAddsUserID(t *testing.T) {
	cases := []struct {
		name     string
		method   string
		path     string
		response string
		call     func(*Client) error
	}{
		{
			name:     "list",
			method:   http.MethodGet,
			path:     "/api/chats/agents",
			response: `{"agents":[]}`,
			call: func(c *Client) error {
				_, err := c.ListAgents(context.Background(), "alice")
				return err
			},
		},
		{
			name:     "get",
			method:   http.MethodGet,
			path:     "/api/chats/agents/a1",
			response: `{"id":"a1","owner":"alice"}`,
			call: func(c *Client) error {
				_, err := c.GetAgent(context.Background(), "a1", "alice")
				return err
			},
		},
		{
			name:     "create",
			method:   http.MethodPost,
			path:     "/api/chats/agents",
			response: `{"id":"a2","owner":"alice"}`,
			call: func(c *Client) error {
				_, err := c.CreateAgent(context.Background(), AgentWrite{Name: "reviewer"}, "alice")
				return err
			},
		},
		{
			name:     "update",
			method:   http.MethodPut,
			path:     "/api/chats/agents/a2",
			response: `{"id":"a2","owner":"alice"}`,
			call: func(c *Client) error {
				_, err := c.UpdateAgent(context.Background(), "a2", AgentWrite{Name: "reviewer"}, "alice")
				return err
			},
		},
		{
			name:   "delete",
			method: http.MethodDelete,
			path:   "/api/chats/agents/a2",
			call: func(c *Client) error {
				return c.DeleteAgent(context.Background(), "a2", "alice")
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method != tc.method {
					t.Errorf("method = %s, want %s", r.Method, tc.method)
				}
				if r.URL.Path != tc.path {
					t.Errorf("path = %s, want %s", r.URL.Path, tc.path)
				}
				if got := r.URL.Query().Get("user_id"); got != "alice" {
					t.Errorf("user_id = %q, want alice", got)
				}
				_, _ = io.WriteString(w, tc.response)
			})
			if err := tc.call(c); err != nil {
				t.Fatalf("call: %v", err)
			}
		})
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

func TestListCollectionsReadsNames(t *testing.T) {
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/collections" {
			t.Errorf("request = %s %s, want GET /api/collections", r.Method, r.URL.Path)
		}
		_, _ = io.WriteString(w, `{"collections":["docs","runbooks"],"count":2}`)
	})
	names, err := c.ListCollections(context.Background())
	if err != nil {
		t.Fatalf("ListCollections: %v", err)
	}
	if len(names) != 2 || names[0] != "docs" {
		t.Errorf("names = %v, want docs and runbooks", names)
	}
}

func TestAddCollectionSourceSendsURLAndInterval(t *testing.T) {
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/collections/docs/sources" {
			t.Errorf("request = %s %s, want POST /api/collections/docs/sources", r.Method, r.URL.Path)
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["url"] != "https://example.com/a" || body["update_interval"] != float64(60) {
			t.Errorf("body = %v", body)
		}
		_, _ = io.WriteString(w, `{"status":"ok"}`)
	})
	if _, err := c.AddCollectionSource(context.Background(), "docs", "https://example.com/a", 60); err != nil {
		t.Fatalf("AddCollectionSource: %v", err)
	}
}

func TestRemoveCollectionSourceDeletesWithURLBody(t *testing.T) {
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete || r.URL.Path != "/api/collections/docs/sources" {
			t.Errorf("request = %s %s, want DELETE /api/collections/docs/sources", r.Method, r.URL.Path)
		}
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["url"] != "https://example.com/a" {
			t.Errorf("body = %v", body)
		}
		_, _ = io.WriteString(w, `{"status":"ok"}`)
	})
	if _, err := c.RemoveCollectionSource(context.Background(), "docs", "https://example.com/a"); err != nil {
		t.Fatalf("RemoveCollectionSource: %v", err)
	}
}

func TestListModelCapabilitiesReadsDataEnvelope(t *testing.T) {
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/models/capabilities" {
			t.Errorf("request = %s %s, want GET /api/models/capabilities", r.Method, r.URL.Path)
		}
		_, _ = io.WriteString(w, `{"data":[{"id":"m1","disabled":true,"pinned":false}]}`)
	})
	caps, err := c.ListModelCapabilities(context.Background())
	if err != nil {
		t.Fatalf("ListModelCapabilities: %v", err)
	}
	if len(caps) != 1 || caps[0].ID != "m1" || !caps[0].Disabled || caps[0].Pinned {
		t.Errorf("caps = %+v, want m1 disabled and not pinned", caps)
	}
}

func TestSetModelEnabledPutsActionPath(t *testing.T) {
	for _, tc := range []struct {
		enabled bool
		want    string
	}{{true, "/models/toggle-state/zen/enable"}, {false, "/models/toggle-state/zen/disable"}} {
		c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPut || r.URL.Path != tc.want {
				t.Errorf("request = %s %s, want PUT %s", r.Method, r.URL.Path, tc.want)
			}
			_, _ = io.WriteString(w, `{"success":true}`)
		})
		if err := c.SetModelEnabled(context.Background(), "zen", tc.enabled); err != nil {
			t.Fatalf("SetModelEnabled(%v): %v", tc.enabled, err)
		}
	}
}

func TestSetModelPinnedPutsActionPath(t *testing.T) {
	for _, tc := range []struct {
		pinned bool
		want   string
	}{{true, "/models/toggle-pinned/zen/pin"}, {false, "/models/toggle-pinned/zen/unpin"}} {
		c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPut || r.URL.Path != tc.want {
				t.Errorf("request = %s %s, want PUT %s", r.Method, r.URL.Path, tc.want)
			}
			_, _ = io.WriteString(w, `{"success":true}`)
		})
		if err := c.SetModelPinned(context.Background(), "zen", tc.pinned); err != nil {
			t.Fatalf("SetModelPinned(%v): %v", tc.pinned, err)
		}
	}
}

func TestApplyModelFromGallerySendsEntryAndName(t *testing.T) {
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/models/apply" {
			t.Errorf("request = %s %s, want POST /models/apply", r.Method, r.URL.Path)
		}
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["id"] != "qwen3-8b" || body["name"] != "zen" || body["variant"] != "" {
			t.Errorf("body = %v", body)
		}
		_, _ = io.WriteString(w, `{"id":"job-1","status_url":"https://localai.example/models/jobs/job-1"}`)
	})
	out, err := c.ApplyModelFromGallery(context.Background(), GalleryApplyRequest{ID: "qwen3-8b", Name: "zen"})
	if err != nil {
		t.Fatalf("ApplyModelFromGallery: %v", err)
	}
	if out.ID != "job-1" {
		t.Errorf("job ID = %q, want job-1", out.ID)
	}
}

func TestGetModelJobReadsStatus(t *testing.T) {
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/models/jobs/job-1" {
			t.Errorf("request = %s %s, want GET /models/jobs/job-1", r.Method, r.URL.Path)
		}
		_, _ = io.WriteString(w, `{"processed":true,"progress":100,"message":"done","error":"disk full"}`)
	})
	st, err := c.GetModelJob(context.Background(), "job-1")
	if err != nil {
		t.Fatalf("GetModelJob: %v", err)
	}
	if !st.Processed || st.Error != "disk full" {
		t.Errorf("status = %+v, want processed with error", st)
	}
}

func TestCreateAgentTaskReturnsID(t *testing.T) {
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/agent/tasks" {
			t.Errorf("request = %s %s, want POST /api/agent/tasks", r.Method, r.URL.Path)
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["name"] != "digest" || body["prompt"] != "Summarise the inbox" {
			t.Errorf("body = %v", body)
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"id":"t1"}`)
	})
	id, err := c.CreateAgentTask(context.Background(), AgentTaskWrite{Name: "digest", Prompt: "Summarise the inbox"})
	if err != nil {
		t.Fatalf("CreateAgentTask: %v", err)
	}
	if id != "t1" {
		t.Errorf("id = %q, want t1", id)
	}
}

func TestGetAgentTaskReadsFields(t *testing.T) {
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/agent/tasks/t1" {
			t.Errorf("request = %s %s, want GET /api/agent/tasks/t1", r.Method, r.URL.Path)
		}
		_, _ = io.WriteString(w, `{"id":"t1","name":"digest","description":"","model":"m1","prompt":"p","enabled":false,"created_at":"2026-11-15T00:00:00Z","updated_at":"2026-11-15T00:00:00Z"}`)
	})
	task, err := c.GetAgentTask(context.Background(), "t1")
	if err != nil {
		t.Fatalf("GetAgentTask: %v", err)
	}
	if task.Name != "digest" || task.Enabled == nil || *task.Enabled {
		t.Errorf("task = %+v, want digest disabled", task)
	}
}

func TestUpdateAgentTaskPutsFullBody(t *testing.T) {
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut || r.URL.Path != "/api/agent/tasks/t1" {
			t.Errorf("request = %s %s, want PUT /api/agent/tasks/t1", r.Method, r.URL.Path)
		}
		_, _ = io.WriteString(w, `{"message":"Task updated"}`)
	})
	if err := c.UpdateAgentTask(context.Background(), "t1", AgentTaskWrite{Name: "digest", Prompt: "p"}); err != nil {
		t.Fatalf("UpdateAgentTask: %v", err)
	}
}

func TestDeleteAgentTaskUsesIDPath(t *testing.T) {
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete || r.URL.Path != "/api/agent/tasks/t1" {
			t.Errorf("request = %s %s, want DELETE /api/agent/tasks/t1", r.Method, r.URL.Path)
		}
		_, _ = io.WriteString(w, `{"message":"Task deleted"}`)
	})
	if err := c.DeleteAgentTask(context.Background(), "t1"); err != nil {
		t.Fatalf("DeleteAgentTask: %v", err)
	}
}

func TestCreateTriggerSendsScheduleAndReturnsTrigger(t *testing.T) {
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/chats/triggers" {
			t.Errorf("request = %s %s, want POST /api/chats/triggers", r.Method, r.URL.Path)
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["kind"] != "schedule" || body["schedule"] != "daily 08:00" || body["enabled"] != true {
			t.Errorf("body = %v", body)
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"id":"tr1","name":"digest","prompt":"p","kind":"schedule","schedule":"daily 08:00","enabled":true,"summary":"daily"}`)
	})
	out, err := c.CreateTrigger(context.Background(), TriggerWrite{Name: "digest", Prompt: "p", Kind: "schedule", Schedule: "daily 08:00", Enabled: true})
	if err != nil {
		t.Fatalf("CreateTrigger: %v", err)
	}
	if out.ID != "tr1" {
		t.Errorf("ID = %q, want tr1", out.ID)
	}
}

func TestGetTriggerUsesIDPath(t *testing.T) {
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/chats/triggers/tr1" {
			t.Errorf("request = %s %s, want GET /api/chats/triggers/tr1", r.Method, r.URL.Path)
		}
		_, _ = io.WriteString(w, `{"id":"tr1","name":"digest","kind":"schedule","schedule":"daily 08:00","enabled":false}`)
	})
	out, err := c.GetTrigger(context.Background(), "tr1")
	if err != nil {
		t.Fatalf("GetTrigger: %v", err)
	}
	if out.Enabled {
		t.Errorf("enabled = true, want false")
	}
}

func TestUpdateAndDeleteTriggerUseIDPath(t *testing.T) {
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/chats/triggers/tr1" {
			t.Errorf("path = %s, want /api/chats/triggers/tr1", r.URL.Path)
		}
		switch r.Method {
		case http.MethodPut:
			_, _ = io.WriteString(w, `{"id":"tr1","name":"digest","kind":"schedule","enabled":true}`)
		case http.MethodDelete:
			_, _ = io.WriteString(w, `{"message":"deleted"}`)
		default:
			t.Errorf("method = %s", r.Method)
		}
	})
	if _, err := c.UpdateTrigger(context.Background(), "tr1", TriggerWrite{Name: "digest", Kind: "schedule", Enabled: true}); err != nil {
		t.Fatalf("UpdateTrigger: %v", err)
	}
	if err := c.DeleteTrigger(context.Background(), "tr1"); err != nil {
		t.Fatalf("DeleteTrigger: %v", err)
	}
}

func TestListBackendsReadsEveryPage(t *testing.T) {
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/backends" {
			t.Errorf("path = %s, want /api/backends", r.URL.Path)
		}
		if r.URL.Query().Get("term") != "llama-cpp" {
			t.Errorf("term = %q, want llama-cpp", r.URL.Query().Get("term"))
		}
		if r.URL.Query().Get("page") == "2" {
			_, _ = io.WriteString(w, `{"backends":[{"id":"b2","name":"llama-cpp-2","installed":false}],"totalPages":2}`)
			return
		}
		_, _ = io.WriteString(w, `{"backends":[{"id":"b1","name":"llama-cpp","gallery":"localai","installed":true}],"totalPages":2}`)
	})
	entries, err := c.ListBackends(context.Background(), "llama-cpp")
	if err != nil {
		t.Fatalf("ListBackends: %v", err)
	}
	if len(entries) != 2 || entries[0].Name != "llama-cpp" || !entries[0].Installed {
		t.Errorf("entries = %+v, want both pages with llama-cpp installed", entries)
	}
}

func TestInstallBackendPostsNameAndReturnsJob(t *testing.T) {
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/backends/install/llama-cpp" {
			t.Errorf("request = %s %s, want POST /api/backends/install/llama-cpp", r.Method, r.URL.Path)
		}
		_, _ = io.WriteString(w, `{"jobID":"job-1","message":"Backend installation started"}`)
	})
	job, err := c.InstallBackend(context.Background(), "llama-cpp")
	if err != nil {
		t.Fatalf("InstallBackend: %v", err)
	}
	if job != "job-1" {
		t.Errorf("job = %q, want job-1", job)
	}
}

func TestDeleteBackendPostsNameAndReturnsJob(t *testing.T) {
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/backends/delete/llama-cpp" {
			t.Errorf("request = %s %s, want POST /api/backends/delete/llama-cpp", r.Method, r.URL.Path)
		}
		_, _ = io.WriteString(w, `{"jobID":"job-2"}`)
	})
	job, err := c.DeleteBackend(context.Background(), "llama-cpp")
	if err != nil {
		t.Fatalf("DeleteBackend: %v", err)
	}
	if job != "job-2" {
		t.Errorf("job = %q, want job-2", job)
	}
}

func TestGetBackendJobReadsError(t *testing.T) {
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/backends/job/job-1" {
			t.Errorf("path = %s, want /api/backends/job/job-1", r.URL.Path)
		}
		_, _ = io.WriteString(w, `{"processed":true,"deletion":false,"queued":false,"progress":100,"message":"done","error":"disk full"}`)
	})
	st, err := c.GetBackendJob(context.Background(), "job-1")
	if err != nil {
		t.Fatalf("GetBackendJob: %v", err)
	}
	if !st.Processed || st.Error != "disk full" {
		t.Errorf("status = %+v, want processed with error", st)
	}
}
