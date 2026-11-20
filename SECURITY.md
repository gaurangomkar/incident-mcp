# Security

incident-mcp is a read-only window onto production telemetry. The danger is less "the tool deletes a deployment" and more "the process is scoped too widely, or a secret in a log line is copied into a chat".

## Reporting a vulnerability

Open a private advisory:

https://github.com/gaurangomkar/incident-mcp/security/advisories/new

Do not file a public issue, and do not paste a live token into the report. Describe the impact and, if you have one, a minimal reproduction. We will answer on the advisory.

## What we consider in scope

- A tool or code path that creates, updates, patches, or deletes an object in Kubernetes, Prometheus, or Alertmanager.
- Namespace allowlist bypass.
- HTTP authentication bypass on `/mcp`, or the server listening on a non-loopback address without requiring a token.
- Tool arguments that choose the upstream URL (SSRF into the caller's network).
- A credential from the config file or a bearer token file being written to stdout, to a tool result, or to an info log.
- A panic or resource exhaustion triggered by a normal tool argument, as opposed to a huge upstream response, which is capped.

## What is out of scope

- Best-effort redaction missing a secret format we do not list below. Redaction is a backstop. Assume the model sees everything the credentials can read.
- An expensive PromQL query. The server caps the range and the number of samples it will ask for. Prometheus itself should still have query limits. A query the user typed can still be costly inside those limits.
- Data the configured credentials are allowed to read. If the kubeconfig is cluster-admin, the tools can read every allowed namespace. Use the RBAC in `examples/rbac.yaml` and set `kubernetes.allowed_namespaces`.

## How the process limits itself

- The Kubernetes client type exposes get, list, and log reads. There is no delete or apply path in this repository.
- Tool arguments do not include a URL. Prometheus and Alertmanager are the configured base URLs only.
- Every Kubernetes call checks the namespace against `kubernetes.allowed_namespaces` first. An empty list allows every namespace and logs a warning. Set the list.
- Each tool call has a timeout (`timeout`, default 20s, maximum 2m).
- Upstream HTTP clients have their own timeout. A bearer token file is read per request and rejected above 8192 bytes.
- PromQL queries are rejected when they exceed `limits.max_query_bytes`, when the range exceeds `limits.max_range`, or when the step would return more than `limits.max_prom_samples` points.
- Pod log reads are tailed, then capped by line count and by `limits.max_output_bytes`.
- Tool output and tool errors pass through redaction and then truncation. Truncation happens after redaction.
- stdio is the default transport and binds no port. HTTP on a non-loopback address requires a bearer token. `/healthz` is unauthenticated and returns a fixed string.
- Logs go to stderr. The MCP stdio transport uses stdout. Arguments are not logged at info level.

## What redaction looks for

The filter is in `internal/redact`. It replaces:

- PEM private keys
- `Bearer` tokens and JWTs
- GitHub tokens (`ghp_`, `gho_`, `ghu_`, `ghs_`, `ghr_`, `github_pat_`)
- Slack tokens (`xoxb-` and the other `xox` prefixes)
- AWS access key IDs (`AKIA` followed by 16 uppercase characters)
- URL userinfo (`scheme://user:password@host`)
- Assignments whose key is `password`, `passwd`, `secret`, `token`, `credential`, `api_key`, `access_key`, `private_key`, or `client_secret`, including `key="value"` and `"key": "value"` forms

Unquoted assignment values shorter than 6 characters are left alone, so `token=true` survives. A key glued to a word, such as `mytoken=...`, is left alone. Deployment summaries additionally refuse to print env values whose names contain those words, and they print secret references as `from secret/<name> key <key>` rather than the value.

This list will miss a raw base64 Secret, a cloud token with a new prefix, and a password that is not next to a key. Do not point the server at credentials you would not paste into the chat yourself.

## Supported versions

Security fixes go to the latest release on `main`. Until the first tagged release, that means `main`.
