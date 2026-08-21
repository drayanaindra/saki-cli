# Final daemon lifecycle gate fixes

## Scope

Resolve the three verified review blockers without changing the PRD surface:

1. Make Node state cleanup conditional on the record captured from disk, so a replacement record survives a concurrent cleanup.
2. Make Go shutdown cleanup use the same conditional ownership behavior.
3. Prevent a second Go backend from unlinking an active Unix socket before state ownership is checked.

## Implementation

- Use an atomic rename-to-quarantine followed by PID verification; restore a non-owned record with a no-replace hard link.
- Reuse the existing state ownership checks before Unix binding, and detect an active Unix listener before stale-path removal.
- Add regression coverage for active socket preservation and keep existing replacement-state tests green.

## Verification

- `npm run typecheck`
- targeted Vitest daemon suites
- `npm test`
- `npm run backend:test`
- `go vet ./...`
