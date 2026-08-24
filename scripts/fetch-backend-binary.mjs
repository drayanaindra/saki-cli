#!/usr/bin/env node
// postinstall: fetch the platform-matching saki-backend binary from the GitHub Release tagged
// v<package.json version>, verify it against that release's SHA256SUMS.txt, then place it next to
// dist/index.js — the exact path src/daemon.ts:52-56 binaryPath() already looks for. Never fails
// `npm install`: every exit path below returns 0, even on total failure.

import { createHash } from 'node:crypto'
import { chmodSync, existsSync, mkdirSync, readFileSync, renameSync, unlinkSync, writeFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'

const PKG_ROOT = join(dirname(fileURLToPath(import.meta.url)), '..')
const DIST_DIR = join(PKG_ROOT, 'dist')
const BINARY_PATH = join(DIST_DIR, 'saki-backend')
const DOWNLOAD_PATH = `${BINARY_PATH}.download`
const REPO = 'drayanaindra/saki-cli'
const FETCH_TIMEOUT_MS = 30_000

export function assetNameFor(platform, arch) {
  return `saki-backend-${platform}-${arch}`
}

export function isSupportedTarget(platform, arch) {
  return (platform === 'darwin' || platform === 'linux') && (arch === 'x64' || arch === 'arm64')
}

function readPkgVersion() {
  const pkg = JSON.parse(readFileSync(join(PKG_ROOT, 'package.json'), 'utf8'))
  return pkg.version
}

function remediate(reason) {
  console.log(`saki-backend not installed (${reason}).`)
  console.log('Build it from source instead: npm run backend:build (requires Go >= 1.25)')
}

async function fetchWithTimeout(url) {
  return fetch(url, { signal: AbortSignal.timeout(FETCH_TIMEOUT_MS) })
}

async function fetchWithRetry(url) {
  try {
    return await fetchWithTimeout(url)
  } catch (err) {
    if (err?.name !== 'TimeoutError' && err?.name !== 'AbortError') throw err
    return fetchWithTimeout(url)
  }
}

function expectedDigestFor(sumsText, assetName) {
  for (const line of sumsText.split('\n')) {
    const [digest, name] = line.trim().split(/\s+/)
    if (name === assetName) return digest
  }
  return null
}

async function main() {
  if (existsSync(join(PKG_ROOT, 'backend', 'go.mod'))) {
    // Source checkout (backend/ is excluded from the published npm tarball) — the contributor
    // workflow builds this themselves via `npm run backend:build`.
    return
  }

  if (existsSync(BINARY_PATH)) {
    return
  }

  const platform = process.platform
  const arch = process.arch
  if (!isSupportedTarget(platform, arch)) {
    remediate(`${platform}/${arch} has no prebuilt binary — darwin/linux x64/arm64 only`)
    return
  }

  const version = readPkgVersion()
  const tag = `v${version}`
  const assetName = assetNameFor(platform, arch)
  const releaseBase = `https://github.com/${REPO}/releases/download/${tag}`

  try {
    const [binaryRes, sumsRes] = await Promise.all([
      fetchWithRetry(`${releaseBase}/${assetName}`),
      fetchWithRetry(`${releaseBase}/SHA256SUMS.txt`),
    ])
    if (!binaryRes.ok || !sumsRes.ok) {
      throw new Error(`release fetch failed (binary ${binaryRes.status}, checksums ${sumsRes.status})`)
    }

    const sumsText = await sumsRes.text()
    const expectedDigest = expectedDigestFor(sumsText, assetName)
    if (!expectedDigest) throw new Error(`no checksum entry for ${assetName} in SHA256SUMS.txt`)

    const bytes = new Uint8Array(await binaryRes.arrayBuffer())
    const actualDigest = createHash('sha256').update(bytes).digest('hex')
    if (actualDigest !== expectedDigest) {
      throw new Error(`checksum mismatch for ${assetName}: expected ${expectedDigest}, got ${actualDigest}`)
    }

    mkdirSync(DIST_DIR, { recursive: true })
    writeFileSync(DOWNLOAD_PATH, bytes)
    chmodSync(DOWNLOAD_PATH, 0o755)
    renameSync(DOWNLOAD_PATH, BINARY_PATH)

    console.log(`saki-backend ready (${tag}, ${assetName}).`)
    console.log('Next: run "saki doctor" to verify claude/codex/opencode provisioning.')
  } catch (err) {
    if (existsSync(DOWNLOAD_PATH)) unlinkSync(DOWNLOAD_PATH)
    remediate(err instanceof Error ? err.message : String(err))
  }
}

main()
