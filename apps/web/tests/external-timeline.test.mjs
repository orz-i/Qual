import test from 'node:test'
import assert from 'node:assert/strict'
import { safeThreadURL, externalEventTimeHint } from '../src/features/work/external-timeline-utils.ts'

test('only safe HTTPS external thread links may open in another tab', () => {
  assert.equal(safeThreadURL('https://github.com/orz-i/Qual/pull/2'), 'https://github.com/orz-i/Qual/pull/2')
  assert.equal(safeThreadURL('https://example.org/path'), 'https://example.org/path')
  for (const unsafe of [
    '', 'javascript:alert(1)', '//github.com/pull/2', 'http://example.org/issue/1',
    'https://user:pass@example.org/', 'https://localhost/', 'https://127.0.0.1/',
    'https://[::1]/', 'https://forge.internal/', 'https://example.org/?redirect=bad',
    'https://example.org/#script', 'https://example.org:8443/', 'https://example.org.evil.local/path',
    'https://bad..example.org/', 'https://test.invalid/', 'http://127.0.0.1/',
  ]) assert.equal(safeThreadURL(unsafe), null, unsafe)
})

test('source clock hints are informative and never imply task status', () => {
  assert.equal(externalEventTimeHint({
    occurred_at: '2026-11-03T08:01:00Z', received_at: '2026-11-03T08:02:00Z',
  }), null)
  assert.match(externalEventTimeHint({
    occurred_at: '2026-11-03T08:12:00Z', received_at: '2026-11-03T08:02:00Z',
  }), /时钟偏差/)
  assert.match(externalEventTimeHint({
    occurred_at: '2026-11-01T08:00:00Z', received_at: '2026-11-03T08:02:00Z',
  }), /延迟或乱序/)
  assert.match(externalEventTimeHint({
    occurred_at: 'bad', received_at: '2026-11-03T08:02:00Z',
  }), /不可验证/)
})
