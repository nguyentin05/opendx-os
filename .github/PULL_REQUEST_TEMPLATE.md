## Summary

<!-- What does this change and why? -->

## Changes

<!-- Bullet the key changes. -->

## Checklist

- [ ] `make test` passes locally
- [ ] `cd workspace-ui && npm test && npm run build` passes (if dashboard changed)
- [ ] Tests added/updated for new behavior
- [ ] Preserves architectural invariants (zero domain leakage, composite FKs, version-checked transitions, clean file quarantine, audit hash-chain) – see `CONTRIBUTING.md`

### Documentation

Drift guards run in `make test`, so these usually fail loudly rather than silently. Confirm anyway:

- [ ] New or changed environment variables are in `.env.example`
- [ ] New or changed CLI commands or flags are in `docs/architecture/`
- [ ] New HTTP routes/capabilities match H-P-D-I contract specifications
- [ ] Claims match shipped behavior: no unbuilt artifact or unimplemented setting described as verified
- [ ] Screenshots retaken if documented UI changed, with no tokens, keys, or real credentials captured
