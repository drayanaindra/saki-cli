import { isAbsolute, relative, resolve as resolvePath, sep } from 'node:path'

// A `.md`-shaped MCP argument that resolves outside `cwd`. Mirrors backend/domain/lock.go:108-115's
// containment check for the identical shape. Unlike the CLI (a human deliberately types the path), an
// MCP tool's argument can be steered by the calling agent from content already in its context — so this
// check must run at the MCP boundary, BEFORE the wrapped `cmd*` function (which stays unchanged) ever
// runs. Shared by every tool that accepts a caller-supplied path-shaped string (saki_prd_show,
// saki_run_start) — duplicating it per file is exactly how it drifted once already (see commit
// 8a7431c, the slice 2 reviewer-caught whitespace bypass).
//
// `path.relative` (not a `startsWith(cwd + sep)` prefix test) so a resolved path that merely SHARES
// cwd's prefix as a substring — or a directory literally named ".." inside cwd — can't produce a false
// verdict either way; relative() itself does the path-boundary-aware comparison.
//
// Lexical only — does not resolve symlinks. A `.md` symlink already planted inside the repo, pointing
// outside it, would pass this check; closing that requires the caller to already have local write
// access to the repo, a materially stronger precondition than the "LLM-steered argument" threat model
// this check targets (confirmed by a dedicated security audit, slice 3) — tracked, not fixed here.
export function pathEscapesCwd(target: string, cwd: string): boolean {
  const abs = isAbsolute(target) ? target : resolvePath(cwd, target)
  const rel = relative(cwd, abs)
  return rel === '..' || rel.startsWith(`..${sep}`) || isAbsolute(rel)
}
