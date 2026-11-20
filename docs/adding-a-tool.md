# Adding a tool

Tools are plain Go functions registered with the official MCP SDK. The server does not use a plugin system. A new tool is a package, a `Register` function, and one line in `internal/server/server.go`.

## 1. Decide the read

Write down the upstream call and confirm it cannot mutate anything. If the call needs a namespace, it has to go through `kube.Client` (or a new method on it) so the allowlist runs first. Do not take a URL, a kubeconfig, or a bearer token as a tool argument. Those belong in the config file.

## 2. Add the backend seam

Prefer a small interface the tool depends on, and a struct that talks to the real system. Tests use a fake that implements the interface. `prometheus.Querier` and `alertmanager.Client` are the pattern. For Kubernetes objects, use `kube.NewClient` with `k8s.io/client-go/kubernetes/fake`. The fake clientset does not serve pod logs; pass a `kube.LogReader`.

## 3. Register the tool

```go
func Register(s *mcp.Server, opt Options) {
    mcp.AddTool(s, &mcp.Tool{
        Name:        "k8s_nodes",
        Title:       "Kubernetes nodes",
        Description: "List node conditions and pressure. Read-only. No namespace argument; this is cluster scoped.",
        Annotations: mcptool.Annotations("Kubernetes nodes"),
    }, func(ctx context.Context, _ *mcp.CallToolRequest, in nodeInput) (*mcp.CallToolResult, any, error) {
        return opt.Runtime.Call(ctx, "k8s_nodes", func(ctx context.Context) (string, error) {
            return nodes(ctx, opt, in)
        })
    })
}
```

`mcptool.Runtime.Call` applies the timeout, redacts the result, and truncates it. Return a plain string. Return an error for bad input or an upstream failure; the SDK turns that into a tool error the model can see.

Input structs use `json` and `jsonschema` tags. A field without `omitempty` is required. Keep descriptions specific: say the unit, the default, and the cap.

Mark the tool read-only with `mcptool.Annotations`. The integration test in `internal/server` fails when a registered tool is missing that hint.

## 4. Wire it in

In `internal/server/server.go`, call `Register` next to the others and pass the shared `mcptool.Runtime`. If the tool needs a new limit, add it to `config.Limits`, give it a default, reject absurd values in `Validate`, and document it in [configuration.md](configuration.md).

## 5. Test the behavior, not the SDK

Call the unexported function with a fake. Cover the empty result, the cap, a denied namespace, and at least one string that the redactor must catch if the tool prints env-like values or logs. Add the tool name to the read-only list in `internal/server/server_test.go`.

## What not to do

- Do not log tool arguments at info. PromQL and log lines can carry customer identifiers. The shared runtime logs the tool name, duration, and byte count.
- Do not print Secret data, raw env values whose names look sensitive, or `kubectl.kubernetes.io/last-applied-configuration`.
- Do not add a write tool (delete a pod, create a silence, post a silence expiry) without a separate design. The RBAC example and the server instructions both say this process is read-only. A write tool would make those statements false.
