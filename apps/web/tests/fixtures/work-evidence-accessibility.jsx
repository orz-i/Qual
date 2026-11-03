// Synthetic local-only QA fixture. Does not authenticate a Human, call Qual's
// production API, execute Git/tests or manufacture a passing TestRun.
import React from 'react'
import { createRoot } from 'react-dom/client'
import { MemoryRouter } from 'react-router'
import '@/index.css'
import { WorkEvidencePacketPreview } from '@/features/evidence/work-packet-preview'

// Deliberately not any real QUAL Project, WorkItem, Artifact or TestRun ID.
const projectID = '11111111-1111-4111-8111-111111111111'
const workItemID = '22222222-2222-4222-8222-222222222222'
const artifactID = '33333333-3333-4333-8333-333333333333'
const packet = {
  schema_version: 1,
  source: 'external-qual-cli',
  git_verification: 'local-git-cat-file-commit',
  project_id: projectID,
  work_item_id: workItemID,
  git_commit: 'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa',
  test_run_id: '44444444-4444-4444-8444-444444444444',
  case_versions: [{
    test_case_id: '55555555-5555-4555-8555-555555555555',
    test_case_key: 'QA-SYNTHETIC-TC',
    case_version_id: '66666666-6666-4666-8666-666666666666',
    snapshot_sha256: 'bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb',
    attempt_count: 2,
    latest_status: 'failed',
  }],
}
if (!['localhost', '127.0.0.1'].includes(location.hostname) ||
    location.pathname !== '/tests/fixtures/work-evidence-accessibility.html') {
  throw new Error('Synthetic QA fixture is only available on loopback at its exact path')
}
const bytes = new TextEncoder().encode(JSON.stringify(packet, null, 2) + '\n')
const digest = Array.from(new Uint8Array(await crypto.subtle.digest('SHA-256', bytes)),
  value => value.toString(16).padStart(2, '0')).join('')
const requestedPath = `/api/v1/projects/${projectID}/artifacts/${artifactID}/content`
// Explicit synthetic failure variants exercise the real UI's safe error path.
const variant = new URLSearchParams(location.search).get('variant')
if (![null, 'sha-mismatch', 'forbidden'].includes(variant)) throw new Error('Unknown QA variant')
const actualFetch = window.fetch.bind(window)
window.fetch = async (input, init) => {
  const url = typeof input === 'string' ? input : input instanceof URL ? input.pathname : input.url
  if (url === requestedPath) {
    if (variant === 'forbidden') return new Response(null, { status: 403 })
    return new Response(bytes, {
      status: 200,
      headers: {
        'Content-Type': 'application/json',
        'Content-Length': String(bytes.length),
        'X-Content-SHA256': variant === 'sha-mismatch' ? '0'.repeat(64) : digest,
      },
    })
  }
  // Never proxy unexpected API requests from a synthetic QA fixture.
  if (url.startsWith('/api/v1/')) return new Response(null, { status: 403 })
  return actualFetch(input, init)
}
const artifact = {
  id: artifactID, project_id: projectID, target_kind: 'work_item',
  target_id: workItemID, filename: 'qual-work-evidence-v1.json',
  media_type: 'application/json', sha256: digest, size_bytes: bytes.length,
  state: 'ready',
}
createRoot(document.getElementById('root')).render(
  <MemoryRouter>
    <main className="min-h-screen space-y-6 bg-background p-4 text-foreground">
      <h1 className="text-lg font-semibold">合成 QA：工作项证据包无障碍</h1>
      <p className="text-sm">本页仅用于独立的键盘、模态焦点与窄视口回归；不是 Human 登录、VoiceOver 朗读或真实 Qual 测试执行。</p>
      <button type="button" id="outside-focus" className="rounded border p-2">对话框外部焦点目标</button>
      <WorkEvidencePacketPreview
        projectID={projectID} workItemID={workItemID} artifact={artifact}
      />
    </main>
  </MemoryRouter>
)
window.__qualSyntheticQAFixtureReady = true
