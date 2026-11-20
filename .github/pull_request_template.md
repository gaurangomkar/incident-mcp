## Summary

## How to test

- [ ] `make test`
- [ ] `make lint`

## Safety

- [ ] This change does not add a write path to Kubernetes, Prometheus, or Alertmanager.
- [ ] New tool output goes through redaction and the output limit.
- [ ] A new Kubernetes call checks the namespace allowlist before it touches the API.
