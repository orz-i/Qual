import test from 'node:test'
import assert from 'node:assert/strict'
import { applyThemePreference, parseThemePreference, readThemePreference, resolveTheme, saveThemePreference } from '../src/app/theme.ts'

test('only known explicit preferences are trusted', () => {
  assert.equal(parseThemePreference('light'), 'light')
  assert.equal(parseThemePreference('dark'), 'dark')
  assert.equal(parseThemePreference('system'), 'system')
  assert.equal(parseThemePreference(null), 'system')
  assert.equal(parseThemePreference('evil'), 'system')
})

test('system theme follows OS while explicit preference overrides it', () => {
  assert.equal(resolveTheme('system', true), 'dark')
  assert.equal(resolveTheme('system', false), 'light')
  assert.equal(resolveTheme('light', true), 'light')
  assert.equal(resolveTheme('dark', false), 'dark')
})

test('local persistence works without storing user identity or requiring storage access', () => {
  const previous = globalThis.window
  const store = new Map()
  globalThis.window = { localStorage: {
    getItem: key => store.get(key) ?? null,
    setItem: (key, value) => store.set(key, value),
  } }
  try {
    assert.equal(readThemePreference(), 'system')
    saveThemePreference('dark')
    assert.equal(readThemePreference(), 'dark')
    globalThis.window.localStorage = {
      getItem: () => { throw new Error('access denied') },
      setItem: () => { throw new Error('access denied') },
    }
    assert.equal(readThemePreference(), 'system')
    assert.doesNotThrow(() => saveThemePreference('light'))
  } finally {
    if (previous === undefined) delete globalThis.window
    else globalThis.window = previous
  }
})

test('applying dark mode updates root class and native control color scheme', () => {
  const previous = globalThis.document
  const root = { classList: { toggle: (name, active) => { assert.equal(name, 'dark'); root.dark = active } }, style: {} }
  globalThis.document = { documentElement: root }
  try {
    applyThemePreference('dark', false)
    assert.equal(root.dark, true)
    assert.equal(root.style.colorScheme, 'dark')
    applyThemePreference('system', false)
    assert.equal(root.dark, false)
    assert.equal(root.style.colorScheme, 'light')
  } finally {
    if (previous === undefined) delete globalThis.document
    else globalThis.document = previous
  }
})
