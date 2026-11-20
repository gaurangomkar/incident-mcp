# Changelog

All notable changes to this project are documented here. The format follows Keep a Changelog, and versions follow Semantic Versioning once they are tagged.

## [Unreleased]

### Added

- MCP server over stdio and streamable HTTP, built on `github.com/modelcontextprotocol/go-sdk`.
- `prometheus_query` and `prometheus_query_range`, with series, sample, and range caps.
- `alertmanager_alerts` and `alertmanager_silences`.
- `k8s_pods`, `k8s_events`, `k8s_deployment`, and `k8s_pod_logs`.
- `triage_service`, which summarizes one deployment from those signals and continues when a section fails.
- Namespace allowlist, per-call timeouts, output truncation, and best-effort secret redaction.
- YAML config with environment overrides, plus an example file.
- Dockerfile, GoReleaser config, and a release workflow for binaries and a GHCR image.
- Unit tests against fakes, including the client-go fake clientset.
- CI workflow for build, vet, lint, and test.

[Unreleased]: https://github.com/gaurangomkar/incident-mcp/compare/HEAD
