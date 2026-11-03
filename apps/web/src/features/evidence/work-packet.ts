// WorkItem evidence packet v1, as produced by the external Qual CLI.
// This is a private, source-reported record. SHA-256 verifies transport and
// storage integrity, not independent Git authenticity or test execution.
export type WorkEvidencePacketV1 = {
  schema_version: 1
  source: 'external-qual-cli'
  git_verification: 'local-git-cat-file-commit'
  project_id: string
  work_item_id: string
  git_commit: string
  test_run_id: string
  case_versions: Array<{
    test_case_id: string
    test_case_key: string
    case_version_id: string
    snapshot_sha256: string
    attempt_count: number
    latest_status: 'passed' | 'failed' | 'blocked' | 'skipped'
  }>
}

export type PacketEnvelope = {
  projectID: string
  workItemID: string
  artifactSHA256: string
  headerSHA256: string | null
  sizeBytes: number
}

const sha256 = /^[a-f0-9]{64}$/
const commit = /^(?:[a-f0-9]{40}|[a-f0-9]{64})$/
const uuid = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/
const outcomes = new Set(['passed', 'failed', 'blocked', 'skipped'])
const maxPacketBytes = 10485760

export function isEvidencePacketFilename(filename: string, mediaType: string): boolean {
  return filename === 'qual-work-evidence-v1.json' && mediaType === 'application/json'
}

export function isWorkEvidencePacket(value: unknown): value is WorkEvidencePacketV1 {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return false
  const record = value as Record<string, unknown>
  if (record.schema_version !== 1 || record.source !== 'external-qual-cli' ||
    record.git_verification !== 'local-git-cat-file-commit' ||
    typeof record.project_id !== 'string' || !uuid.test(record.project_id) ||
    typeof record.work_item_id !== 'string' || !uuid.test(record.work_item_id) ||
    typeof record.test_run_id !== 'string' || !uuid.test(record.test_run_id) ||
    typeof record.git_commit !== 'string' || !commit.test(record.git_commit) ||
    !Array.isArray(record.case_versions) || record.case_versions.length < 1 || record.case_versions.length > 100) {
    return false
  }
  const seen = new Set<string>()
  for (const raw of record.case_versions) {
    if (!raw || typeof raw !== 'object' || Array.isArray(raw)) return false
    const item = raw as Record<string, unknown>
    if (typeof item.test_case_id !== 'string' || !uuid.test(item.test_case_id) ||
      typeof item.case_version_id !== 'string' || !uuid.test(item.case_version_id) ||
      typeof item.test_case_key !== 'string' || item.test_case_key.length < 1 || item.test_case_key.length > 80 ||
      typeof item.snapshot_sha256 !== 'string' || !sha256.test(item.snapshot_sha256) ||
      !Number.isSafeInteger(item.attempt_count) || (item.attempt_count as number) < 1 ||
      typeof item.latest_status !== 'string' || !outcomes.has(item.latest_status) ||
      seen.has(item.case_version_id)) return false
    seen.add(item.case_version_id)
  }
  return true
}

export async function verifyWorkEvidencePacket(
  bytes: ArrayBuffer,
  envelope: PacketEnvelope,
): Promise<{ packet: WorkEvidencePacketV1; sha256: string }> {
  if (bytes.byteLength < 1 || bytes.byteLength > maxPacketBytes ||
    bytes.byteLength !== envelope.sizeBytes) {
    throw new Error('附件长度与 Qual 记录不一致，已停止展示。')
  }
  const hashBuffer = await crypto.subtle.digest('SHA-256', bytes)
  const digest = Array.from(new Uint8Array(hashBuffer), n => n.toString(16).padStart(2, '0')).join('')
  if (!sha256.test(envelope.artifactSHA256) ||
    envelope.headerSHA256 !== digest || envelope.artifactSHA256 !== digest) {
    throw new Error('附件 SHA-256 与 Qual 元数据或下载回执不一致，已停止展示。')
  }

  let document: unknown
  try {
    document = JSON.parse(new TextDecoder('utf-8', { fatal: true }).decode(bytes))
  } catch {
    throw new Error('附件不是有效的 UTF-8 JSON 证据包。')
  }
  if (!isWorkEvidencePacket(document)) {
    throw new Error('附件格式或 CaseVersion/Attempt 来源字段不符合证据包 v1 规范。')
  }
  if (document.project_id !== envelope.projectID || document.work_item_id !== envelope.workItemID) {
    throw new Error('证据包的项目或工作项与当前页面不一致。')
  }
  return { packet: document, sha256: digest }
}
