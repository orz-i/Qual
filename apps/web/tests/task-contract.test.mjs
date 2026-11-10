import test from 'node:test'
import assert from 'node:assert/strict'
import { taskGuideSections, cleanedAcceptanceCriteria, canReadTaskPrivate } from '../src/features/work/task-contract.ts'

test('Task description remains authoritative while Chinese and English sections provide navigation', () => {
  const input = ['目标：完成用户登录', '包含详细前提', '用户入口：http://127.0.0.1:5173/login',
    '操作：打开工作项', '点击领取任务', '验证：状态变化持久化', '证据：TestRun SHA',
    '恢复：403 时先停止', '### 失败与重试', '原请求键与原 payload 重试'].join('\n')
  const parts = taskGuideSections(input)
  assert.equal(parts.length, 7)
  assert.deepEqual(parts.map((part) => part.kind), ['goal','entry','drive','verify','evidence','recovery','recovery'])
  assert.match(parts[2].content, /点击领取任务/)
  assert.match(parts[6].content, /原 payload/)
  assert.equal(new Set(parts.map((part) => part.id)).size, parts.length)
})
test('untrusted Markdown code blocks are shown as plain text and do not become navigable headings', () => {
  const source = ['Goal: observe safe commands', '```bash', '### NOT A MARKDOWN TITLE',
    'echo "Verify: do not execute this"', '```', 'Verify: inspect actual HTTP 403'].join('\n')
  const parts = taskGuideSections(source)
  assert.equal(parts.length, 2)
  assert.match(parts[0].content, /### NOT A MARKDOWN TITLE/)
  assert.match(parts[0].content, /echo "Verify/)
  assert.equal(parts[1].title, 'Verify')
})
test('empty or legacy Task description degrades to a safe plain-text section', () => {
  assert.deepEqual(taskGuideSections(''), [])
  assert.deepEqual(taskGuideSections('  '), [])
  const parts = taskGuideSections('早期未结构化说明\n仍可原样阅读')
  assert.equal(parts.length, 1)
  assert.match(parts[0].content, /仍可原样阅读/)
})
test('Task private presentation never uses project-wide grants when TaskScoped relationship is absent or stale', () => {
  const base = {
    project_mode:'task_scoped',work_item_id:'11111111-1111-4111-8111-111111111111',work_version:7,
    allowed_actions:['task.read_test','task.read_evidence'],
  }
  assert.equal(canReadTaskPrivate(base,base.work_item_id,7),true)
  assert.equal(canReadTaskPrivate({...base,allowed_actions:[]},base.work_item_id,7,true),false)
  assert.equal(canReadTaskPrivate(base,'22222222-2222-4222-8222-222222222222',7,true),false)
  assert.equal(canReadTaskPrivate(base,base.work_item_id,8,true),false)
  assert.equal(canReadTaskPrivate(undefined,base.work_item_id,7,true),false)
  assert.equal(canReadTaskPrivate({...base,project_mode:'legacy',allowed_actions:[]},base.work_item_id,7,true),true)
})

test('Acceptance Criteria draft normalization preserves valid entries and rejects overflow', () => {
  assert.deepEqual(cleanedAcceptanceCriteria([' 一条 ', '', '失败重试']), ['一条','失败重试'])
  assert.throws(() => cleanedAcceptanceCriteria(Array(31).fill('x')), /30/)
  assert.throws(() => cleanedAcceptanceCriteria(['x'.repeat(2001)]), /2000/)
})
