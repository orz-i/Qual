import test from 'node:test'
import assert from 'node:assert/strict'
import { createHash, webcrypto } from 'node:crypto'
import {
  isEvidencePacketFilename, isWorkEvidencePacket, verifyWorkEvidencePacket,
} from '../src/features/evidence/work-packet.ts'

globalThis.crypto ??= webcrypto
const project = 'e6560273-31f1-4c3f-b690-3b4586977700'
const work = '4bf4ea47-006b-4636-b8e1-e1334c1cfe5d'
const run = 'b4157870-1a20-44c9-a287-a74cda3f34aa'
const caseVersion = '45142191-bac5-412f-af56-d7901ea46be1'

const original = () => ({
  schema_version: 1,
  source: 'external-qual-cli',
  git_verification: 'local-git-cat-file-commit',
  project_id: project,
  work_item_id: work,
  git_commit: '95f83d4955fa02125e8c336f33ce2b14d3d3de32',
  test_run_id: run,
  case_versions: [{
    test_case_id: '659bcf96-9893-4b20-a7ed-87b924416e59',
    test_case_key: 'QUAL-TC-27',
    case_version_id: caseVersion,
    snapshot_sha256: '97d89f8ef72b1008fead54594f856ec0a0f759288b8625aea57a5b1d1bcda847',
    attempt_count: 1,
    latest_status: 'passed',
  }],
})
const serialize = data => {
  const buf = new TextEncoder().encode(JSON.stringify(data) + '\n')
  const expectedHash = createHash('sha256').update(buf).digest('hex')
  return { buf, expectedHash }
}
const expected = (buf, digest) => ({
  projectID: project, workItemID: work, sizeBytes: buf.length,
  artifactSHA256: digest, headerSHA256: digest,
})

test('only the canonical JSON packet filename and MIME are previewed', () => {
  assert.equal(isEvidencePacketFilename('qual-work-evidence-v1.json', 'application/json'), true)
  assert.equal(isEvidencePacketFilename('report.json', 'application/json'), false)
  assert.equal(isEvidencePacketFilename('qual-work-evidence-v1.json', 'text/plain'), false)
})

test('real QUAL-4 packet shape validates exact project, immutable case and result', async () => {
  const packet = original()
  const { buf, expectedHash } = serialize(packet)
  const verified = await verifyWorkEvidencePacket(buf.buffer, expected(buf, expectedHash))
  assert.deepEqual(verified.packet, packet)
  assert.equal(verified.sha256, expectedHash)
  assert.equal(verified.packet.case_versions[0].case_version_id, caseVersion)
  assert.equal(verified.packet.case_versions[0].attempt_count, 1)
})

test('hash, server header and downloaded size all must match', async () => {
  const { buf, expectedHash } = serialize(original())
  for (const envelope of [
    {...expected(buf, expectedHash), headerSHA256: null},
    {...expected(buf, expectedHash), artifactSHA256: '0'.repeat(64)},
    {...expected(buf, expectedHash), headerSHA256: 'f'.repeat(64)},
    {...expected(buf, expectedHash), sizeBytes: buf.length + 1},
    {...expected(buf, expectedHash), projectID: '00000000-0000-4000-8000-000000000000'},
    {...expected(buf, expectedHash), workItemID: '00000000-0000-4000-8000-000000000000'},
  ]) {
    await assert.rejects(verifyWorkEvidencePacket(buf.buffer, envelope))
  }
})

test('a new hash for forged/malformed content cannot bypass semantic validation', async () => {
  const cases = [
    { ...original(), source: 'external-runner-pretend' },
    { ...original(), schema_version: 2 },
    { ...original(), git_verification: 'remote-verified' },
    { ...original(), git_commit: 'deadbeef' },
    { ...original(), case_versions: [] },
    { ...original(), case_versions: [ { ...original().case_versions[0], attempt_count: 0 } ] },
    { ...original(), case_versions: [ { ...original().case_versions[0], latest_status: 'unknown' } ] },
    { ...original(), case_versions: [ { ...original().case_versions[0], snapshot_sha256: 'invalid' } ] },
    { ...original(), case_versions: [ ...original().case_versions, ...original().case_versions ] },
    null,
    ['a', 'b'],
  ]
  for (const packet of cases) {
    assert.equal(isWorkEvidencePacket(packet), false)
    const { buf, expectedHash } = serialize(packet)
    await assert.rejects(verifyWorkEvidencePacket(buf.buffer, expected(buf, expectedHash)))
  }
})

test('failed remains a valid observed result; verification does not turn it into Passed', async () => {
  const packet = original()
  packet.case_versions[0].latest_status = 'failed'
  packet.case_versions[0].attempt_count = 2
  const { buf, expectedHash } = serialize(packet)
  const { packet: actual } = await verifyWorkEvidencePacket(buf.buffer, expected(buf, expectedHash))
  assert.equal(actual.case_versions[0].latest_status, 'failed')
  assert.equal(actual.case_versions[0].attempt_count, 2)
})

test('invalid UTF-8 and invalid JSON never get displayed as verified', async () => {
  const invalids = [new Uint8Array([255, 254]), new TextEncoder().encode('{ broken')]
  for (const buf of invalids) {
    const expectedHash = createHash('sha256').update(buf).digest('hex')
    await assert.rejects(verifyWorkEvidencePacket(buf.buffer, expected(buf, expectedHash)))
  }
})
