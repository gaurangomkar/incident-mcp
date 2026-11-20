# Contributing

Thanks for looking. The useful changes are the ones an on-call engineer would trust with a production kubeconfig.

## Setup

Go 1.27 or newer. The version in `go.mod` is what CI uses.

```sh
git clone https://github.com/gaurangomkar/incident-mcp.git
cd incident-mcp
make test
make lint
```

`make lint` needs [golangci-lint](https://golangci-lint.run) v2.15 or newer. CI runs v2.15.0. The config is `.golangci.yml`.

`make build` writes `bin/incident-mcp`. `make ci` runs vet, lint, test, and build.

## What to change

- A new read-only tool or a tighter guard belongs here. Start with [docs/adding-a-tool.md](docs/adding-a-tool.md).
- A write against Kubernetes, Prometheus, or Alertmanager does not belong here unless the design is discussed first. The server instructions, the RBAC example, and the read-only tool hints would all become false.
- Keep packages small. `internal/server.New` is the registration list. Do not hide registration in `init`.

## Tests

Tests use fakes. Prometheus and Alertmanager have interfaces. Kubernetes tests use `k8s.io/client-go/kubernetes/fake` for objects, and an injected log reader because the fake clientset does not serve the log subresource.

Cover the cap, the empty result, and the failure you care about. A namespace allowlist test should show that the denied namespace never needs the object to exist. If you add a tool, add its name to the read-only check in `internal/server/server_test.go`.

## Commits and pull requests

Use a conventional commit subject (`feat(kube): ...`, `fix(prometheus): ...`, `docs: ...`). Keep the subject specific to the change.

Fill in the pull request template. In particular, say whether any new call is still read-only and whether its output goes through redaction and the byte limit.

Do not commit a kubeconfig, a token, or a filled-in `incident-mcp.yaml`. The gitignore ignores the usual local names. `examples/config.yaml` is the one that stays, and it points at localhost with no credentials.

## Reporting issues

Use the bug or feature template. For a vulnerability, follow [SECURITY.md](SECURITY.md) and do not open a public issue.
