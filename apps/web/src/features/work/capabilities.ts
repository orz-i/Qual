import type { components } from '@qual/sdk-ts'

export type TaskCapabilities = components['schemas']['TaskCapabilities']
export type TaskAction = components['schemas']['TaskAction']
export type Status = components['schemas']['Status']

// The server grants an ephemeral action snapshot. Never infer permissions
// from a local project role or reuse an action after the WorkItem version
// changes. A write must still perform full server-side validation.
export function allowsTaskAction(
  cap: TaskCapabilities | undefined,
  workVersion: number,
  action: TaskAction,
): boolean {
  return cap?.work_version === workVersion && cap.allowed_actions.includes(action)
}

export function taskTransitionAction(status: Status, next: Status): TaskAction | null {
  if ((status === 'backlog' && next === 'todo') ||
      (status === 'todo' && next === 'in_progress')) return 'task.start'
  if (status === 'in_progress' && next === 'in_review') return 'task.submit_review'
  if (status === 'in_progress' && next === 'cancelled') return 'task.cancel'
  if (status === 'in_review' && next === 'in_progress') return 'task.return_for_work'
  return null
}

const capacityNames: Record<TaskCapabilities['my_capacity'][number], string> = {
  candidate: '可参与任务',
  executor: '当前执行者',
  reviewer: '当前审核者',
  owner: '任务责任人',
}

export function displayTaskCapacities(cap: TaskCapabilities | undefined): string {
  return cap?.my_capacity.map(value => capacityNames[value]).join(' · ') || '当前无任务职责'
}

const deniedNames: Record<string, string> = {
  participation_required: '需要有效项目参与资格',
  project_explicit_deny: '项目明确禁止此操作',
  not_independent: '曾参与此任务的执行或测试，不能独立审核',
  status_or_relation: '当前任务状态或职责不允许',
  project_permission_required: '当前项目权限不足',
  not_current_task_capacity: '当前职责不允许',
}
export function displayTaskDenial(reason: string | undefined): string {
  return reason ? deniedNames[reason] ?? '当前不可操作，请刷新权限状态' : ''
}
