# Configuration

incident-mcp reads one YAML file and then applies environment variables over it. Flags override the file and the environment for the transport and the HTTP address.

## Where the file is found

1. `-config PATH`
2. `INCIDENT_MCP_CONFIG`
3. `./incident-mcp.yaml`
4. `./incident-mcp.yml`
5. `$XDG_CONFIG_HOME/incident-mcp/config.yaml`, or `~/.config/incident-mcp/config.yaml` when `XDG_CONFIG_HOME` is unset

A missing file in steps 3 to 5 is not an error. The server starts from defaults plus the environment and logs that it did so. An explicit path that does not exist is an error. Unknown YAML fields are an error.

A copy-ready file is `examples/config.yaml`.

## File

```yaml
server:
  transport: stdio          # stdio or http
  http_addr: 127.0.0.1:8080
  bearer_token: ""          # required when http_addr is not loopback
  bearer_token_file: ""     # read on each request; wins over bearer_token

log_level: info             # debug, info, warn, error
timeout: 20s                # per tool call, 1s to 2m
redact: true

limits:
  max_output_bytes: 32768   # 256 to 2MiB
  max_prom_series: 50       # 1 to 2000
  max_prom_samples: 200     # 1 to 10000, also the cap on range resolution
  max_range: 24h            # 1m to 168h
  max_log_lines: 200        # 1 to 5000
  max_events: 50            # 1 to 1000
  max_pods: 200             # 1 to 2000
  max_alerts: 100           # 1 to 2000
  max_query_bytes: 8192     # 64 to 1MiB

prometheus:
  url: http://127.0.0.1:9090
  timeout: 20s
  bearer_token: ""
  bearer_token_file: ""
  username: ""
  password: ""
  insecure_skip_verify: false

alertmanager:
  url: http://127.0.0.1:9093
  timeout: 20s
  bearer_token: ""
  bearer_token_file: ""
  username: ""
  password: ""
  insecure_skip_verify: false

kubernetes:
  kubeconfig: ""            # empty: KUBECONFIG, then ~/.kube/config, then in-cluster
  context: ""
  timeout: 20s              # also bounds log streams
  allowed_namespaces: []    # empty means every namespace

triage:
  log_lines: 80             # must be <= limits.max_log_lines
  event_limit: 15           # must be <= limits.max_events
  queries: []               # replaces the built-in queries when set
```

Durations are strings (`20s`, `1h`). A bare number is rejected.

Leaving `prometheus.url` or `alertmanager.url` empty disables that backend. The tools stay registered and return a clear "not configured" error. Kubernetes with no credentials does the same, unless you set `kubeconfig` or `context` to something that cannot be loaded, which fails startup.

`insecure_skip_verify` turns off TLS verification for that upstream. Leave it false outside a lab.

URLs must be `http` or `https` and must not embed `user:password@`. Use the username and password fields or a bearer token.

### Triage queries

Each query has a `name` and an `expr`. The expression is a Go template. The data fields are `Namespace` and `Service`. The function `re` runs `regexp.QuoteMeta`, which you want inside a PromQL regex.

Setting `triage.queries` replaces the defaults. The defaults are:

```text
up{namespace="{{.Namespace}}",pod=~"{{.Service | re}}-.+"}
sum by (pod) (increase(kube_pod_container_status_restarts_total{namespace="{{.Namespace}}",pod=~"{{.Service | re}}-.+"}[30m]))
```

A query that fails at triage time is reported in that section. It does not fail the whole summary. An empty result is normal when the metric is absent.

### HTTP transport

`server.transport: http` listens on `server.http_addr` and serves MCP at `/mcp`. `GET /healthz` returns `ok` and does not require a token, so a probe can use it. Binding to anything that is not a loopback address (`127.0.0.1`, `::1`, `localhost`) requires `bearer_token` or `bearer_token_file`. The check exists so a public listen address cannot ship without a credential.

Clients send `Authorization: Bearer <token>`. Comparison is constant time. A token file is read per request and rejected when it is larger than 8192 bytes.

Stdio is the right transport for a desktop client. HTTP is for a client on another host. Do not publish the port without the token.

## Environment

Set variables override the file. Empty values are ignored, so unsetting a variable is how you keep the file's value.

| Variable | Overrides |
| --- | --- |
| `INCIDENT_MCP_CONFIG` | config file path |
| `INCIDENT_MCP_TRANSPORT` | `server.transport` |
| `INCIDENT_MCP_HTTP_ADDR` | `server.http_addr` |
| `INCIDENT_MCP_BEARER_TOKEN` | `server.bearer_token` |
| `INCIDENT_MCP_BEARER_TOKEN_FILE` | `server.bearer_token_file` |
| `INCIDENT_MCP_LOG_LEVEL` | `log_level` |
| `INCIDENT_MCP_TIMEOUT` | `timeout` |
| `INCIDENT_MCP_REDACT` | `redact` (`true` or `false`) |
| `INCIDENT_MCP_MAX_OUTPUT_BYTES` | `limits.max_output_bytes` |
| `INCIDENT_MCP_ALLOWED_NAMESPACES` | `kubernetes.allowed_namespaces`, comma-separated |
| `INCIDENT_MCP_KUBECONFIG` | `kubernetes.kubeconfig` |
| `INCIDENT_MCP_KUBE_CONTEXT` | `kubernetes.context` |
| `PROMETHEUS_URL` | `prometheus.url` |
| `PROMETHEUS_BEARER_TOKEN` | `prometheus.bearer_token` |
| `PROMETHEUS_BEARER_TOKEN_FILE` | `prometheus.bearer_token_file` |
| `PROMETHEUS_USERNAME` | `prometheus.username` |
| `PROMETHEUS_PASSWORD` | `prometheus.password` |
| `ALERTMANAGER_URL` | `alertmanager.url` |
| `ALERTMANAGER_BEARER_TOKEN` | `alertmanager.bearer_token` |
| `ALERTMANAGER_BEARER_TOKEN_FILE` | `alertmanager.bearer_token_file` |
| `ALERTMANAGER_USERNAME` | `alertmanager.username` |
| `ALERTMANAGER_PASSWORD` | `alertmanager.password` |

`KUBECONFIG` is not copied into the config struct. When `kubernetes.kubeconfig` is empty, client-go reads `KUBECONFIG` the same way kubectl does, including a list of files separated by the platform path delimiter. Set `INCIDENT_MCP_KUBECONFIG` when you want a single explicit path that overrides that list.

Other limits are file-only. They change rarely, and a typo in an environment variable should not silently raise a cap.

## Flags

| Flag | Effect |
| --- | --- |
| `-config` | File path. Overrides `INCIDENT_MCP_CONFIG`. |
| `-transport` | `stdio` or `http`. Overrides the file and the environment. |
| `-http-addr` | Listen address for `http`. |
| `-version` | Print the version to stderr and exit. |

## Kubernetes credentials

Resolution order when `kubernetes.kubeconfig` is empty:

1. `KUBECONFIG`
2. `~/.kube/config`
3. In-cluster config, when the process runs in a pod
4. Otherwise the Kubernetes tools return "not configured" and the process still starts

An explicit kubeconfig path or context that cannot be loaded fails startup. An invalid namespace in the allowlist fails startup. Namespace names must be DNS-1123 labels.

Give the process the RBAC in `examples/rbac.yaml` and nothing else. The code never requests Secret objects. The RBAC should not grant them either. Redaction is not a substitute for that.
