import test from 'node:test'
import assert from 'node:assert/strict'
import { externalRunPresentation, externalReportPresentation } from '../src/features/work/run-observability-utils.ts'

test('server-computed run observability never means WorkItem Done, TestAttempt Passed or task failure', () => {
  const stale = externalRunPresentation({ source_status: 'running', observability: 'stale' })
  assert.match(stale.observation, /Stale/)
  assert.match(stale.note, /不等于执行失败/)
  const unknown = externalRunPresentation({ source_status: 'blocked', observability: 'unknown' })
  assert.match(unknown.note, /不能判断/)
  const terminal = externalRunPresentation({ source_status: 'succeeded', observability: 'terminal' })
  assert.match(terminal.note, /不代表 WorkItem Done.*TestAttempt Passed/)
  const current = externalRunPresentation({ source_status: 'running', observability: 'current' })
  assert.match(current.note, /外部 Host/)
})

test('historical out-of-order external status is retained, not presented as current', () => {
  assert.match(externalReportPresentation({ kind: 'status', status: 'failed', applied: false }), /仅留存历史/)
  assert.match(externalReportPresentation({ kind: 'status', status: 'running', applied: true }), /已纳入运行投影/)
  assert.match(externalReportPresentation({ kind: 'heartbeat', applied: false }), /心跳/)
})
