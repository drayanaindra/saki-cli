import { describe, it, expect } from 'vitest'
import { parseArgs } from './args.js'
import { EXIT, CliError } from './exit.js'

const SPEC = { json: 'boolean', cwd: 'string', follow: 'boolean', create: 'boolean' } as const

describe('parseArgs', () => {
  it('collects positionals in order', () => {
    expect(parseArgs(['run', 'build', 'E12'], SPEC).positionals).toEqual(['run', 'build', 'E12'])
  })

  it('reads a boolean flag with no value', () => {
    const a = parseArgs(['runs', '--json'], SPEC)
    expect(a.flags.json).toBe(true)
    expect(a.positionals).toEqual(['runs'])
  })

  it('reads a string flag in --flag=value form', () => {
    expect(parseArgs(['runs', '--cwd=/repo/x'], SPEC).flags.cwd).toBe('/repo/x')
  })

  it('reads a string flag in --flag value form', () => {
    const a = parseArgs(['runs', '--cwd', '/repo/y'], SPEC)
    expect(a.flags.cwd).toBe('/repo/y')
    expect(a.positionals).toEqual(['runs'])
  })

  it('keeps an =-form value containing = intact', () => {
    expect(parseArgs(['x', '--cwd=/a=b'], SPEC).flags.cwd).toBe('/a=b')
  })

  it('does not swallow the next token for a boolean flag', () => {
    const a = parseArgs(['runs', '--json', 'extra'], SPEC)
    expect(a.flags.json).toBe(true)
    expect(a.positionals).toEqual(['runs', 'extra'])
  })

  it('last occurrence of a repeated flag wins', () => {
    expect(parseArgs(['x', '--cwd', '/a', '--cwd', '/b'], SPEC).flags.cwd).toBe('/b')
  })

  it('treats everything after -- as positional, even flag-shaped tokens', () => {
    const a = parseArgs(['add', '--', '--not-a-flag', 'text'], SPEC)
    expect(a.positionals).toEqual(['add', '--not-a-flag', 'text'])
    expect(a.flags['not-a-flag']).toBeUndefined()
  })

  it('sets help for -h and --help without requiring them in the spec', () => {
    expect(parseArgs(['-h'], SPEC).help).toBe(true)
    expect(parseArgs(['runs', '--help'], SPEC).help).toBe(true)
  })

  it('rejects an unknown flag with a USAGE CliError naming the flag', () => {
    try {
      parseArgs(['runs', '--bogus'], SPEC)
      throw new Error('expected parseArgs to throw')
    } catch (err) {
      expect(err).toBeInstanceOf(CliError)
      expect((err as CliError).code).toBe(EXIT.USAGE)
      expect((err as CliError).message).toContain('--bogus')
    }
  })

  // REGRESSION (review NIT): `spec[name]` walks Object.prototype, so inherited members were
  // accepted as flags — `--constructor boom` parsed as a string flag and `--toString` reported
  // "needs a value" instead of "unknown flag".
  it('rejects inherited Object.prototype keys as unknown flags', () => {
    for (const name of ['constructor', 'toString', 'hasOwnProperty', '__proto__']) {
      // Substring match — `__proto__` contains no regex metacharacters, but asserting on the
      // literal avoids a fake "escape" that does nothing.
      expect(() => parseArgs(['x', `--${name}`, 'boom'], SPEC)).toThrowError(`unknown flag: --${name}`)
    }
  })

  it('rejects a string flag used at the end with no value', () => {
    try {
      parseArgs(['runs', '--cwd'], SPEC)
      throw new Error('expected parseArgs to throw')
    } catch (err) {
      expect((err as CliError).code).toBe(EXIT.USAGE)
      expect((err as CliError).message).toContain('--cwd')
    }
  })

  it('rejects a string flag whose next token is another flag', () => {
    try {
      parseArgs(['runs', '--cwd', '--json'], SPEC)
      throw new Error('expected parseArgs to throw')
    } catch (err) {
      expect((err as CliError).code).toBe(EXIT.USAGE)
    }
  })
})
