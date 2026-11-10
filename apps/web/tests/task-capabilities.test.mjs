import test from 'node:test'
import assert from 'node:assert/strict'
import { allowsTaskAction, taskTransitionAction, displayTaskCapacities, displayTaskDenial } from '../src/features/work/capabilities.ts'

const base = {
  schema_version: 1,
  project_mode: 'task_scoped',
  work_item_id: '11111111-1111-4111-8111-111111111111',
  work_version: 7,
  policy_version: 2,
  policy_source: 'project',
  review_mode: 'independent_review',
  my_capacity: ['candidate', 'executor'],
  allowed_actions: ['task.update', 'task.comment', 'task.report_test', 'task.submit_review'],
  verification_required: true,
}

test('server snapshot enables only exactly granted task action and matching WorkItem version', () => {
  assert.equal(allowsTaskAction(base, 7, 'task.update'), true)
  assert.equal(allowsTaskAction(base, 7, 'task.claim'), false)
  assert.equal(allowsTaskAction(base, 8, 'task.update'), false)
  assert.equal(allowsTaskAction(undefined, 7, 'task.update'), false)
  const revoked = { ...base, allowed_actions: [], work_version: 8 }
  assert.equal(allowsTaskAction(revoked, 8, 'task.update'), false)
  // A user may be executor for X, not Y, despite an old project work:write.
  const otherTask = { ...base, work_item_id: '22222222-2222-4222-8222-222222222222', allowed_actions: ['task.claim'] }
  assert.equal(allowsTaskAction(otherTask, 7, 'task.update'), false)
  assert.equal(allowsTaskAction(otherTask, 7, 'task.claim'), true)
})

test('status buttons map to the same exact server action names as Domain', () => {
  assert.equal(taskTransitionAction('backlog', 'todo'), 'task.start')
  assert.equal(taskTransitionAction('todo', 'in_progress'), 'task.start')
  assert.equal(taskTransitionAction('in_progress', 'in_review'), 'task.submit_review')
  assert.equal(taskTransitionAction('in_progress', 'cancelled'), 'task.cancel')
  assert.equal(taskTransitionAction('in_review', 'in_progress'), 'task.return_for_work')
  for (const [from, to] of [['todo', 'cancelled'], ['done', 'todo'], ['in_review', 'done'], ['backlog', 'done']]) {
    assert.equal(taskTransitionAction(from, to), null)
  }
})

test('capacity names and coarse denial remain safe and readable', () => {
  assert.match(displayTaskCapacities(base), /当前执行者/)
  assert.match(displayTaskCapacities(base), /可参与任务/)
  assert.equal(displayTaskCapacities(undefined), '当前无任务职责')
  assert.match(displayTaskDenial('project_explicit_deny'), /项目明确禁止/)
  assert.match(displayTaskDenial('not_independent'), /不能独立审核/)
  assert.match(displayTaskDenial('untrusted_secret_dump'), /当前不可操作/)
})
