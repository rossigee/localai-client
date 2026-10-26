# localai-client

Go client for the [LocalAI](https://localai.io/) admin API, used by
[`provider-localai`](https://github.com/rossigee/provider-localai).

It covers the parts of the API that a Kubernetes controller needs: agents,
skills and models, with an API key or OAuth2 bearer token, and optional
verification against a private CA.

Requires Go 1.27.3 or later.

## Usage

```go
c, err := client.New(ctx, client.Config{
    BaseURL:           "https://localai.example.com",
    APIKey:            os.Getenv("LOCALAI_API_KEY"),
    RootCACertificate: string(caPEM),
})
if err != nil {
    return err
}

agents, err := c.ListAgents(ctx)
```

`New` returns an error unless an API key or a full
`tokenURL`/`clientID`/`clientSecret` set is supplied. It does not panic.

## Authentication

`APIKey` is the preferred credential. LocalAI's API accepts only API keys
(the legacy `LOCALAI_API_KEY` value or a per-user key) or browser session IDs;
an OIDC access token obtained through the client-credentials grant is rejected
with `An authentication key is required`. `TokenURL`, `ClientID` and
`ClientSecret` exist for deployments that put something in front of LocalAI
that does accept OAuth2 bearer tokens.

## TLS

`RootCACertificate` appends the given PEM bundle to the system pool, so a
private CA can be trusted without disabling verification. TLS 1.2 is the
minimum. `TLSInsecureSkipVerify` exists but callers should not use it.

## Errors

Non-2xx responses return `*client.APIError`, carrying method, path, status and
body. Use `client.IsNotFound(err)` to distinguish a missing object from a
failed request:

```go
a, err := c.GetAgent(ctx, name)
if client.IsNotFound(err) {
    // create it
}
```

## API notes

- `GetAgent` reads `/api/agents/{name}/config`, because `/api/agents/{name}`
  only reports whether the agent is active.
- `ListAgents` parses the `{"agents": [...], "agentCount": n}` envelope.
- `Agent.SelectedSkills` is sent as `selected_skills`. Some LocalAI builds
  bind LocalAGI's `state.AgentConfig`, which has no such field, and drop it on
  write; in that case an empty selection means every skill is available.
- `Agent.APIKey` is sent as `api_key`, which stops LocalAI from minting a
  per-agent key. Minting only works for callers that have a row in LocalAI's
  `users` table, which a static API key does not.

## License

Apache License 2.0. See [LICENSE](LICENSE).
