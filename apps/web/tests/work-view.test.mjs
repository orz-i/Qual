import test from 'node:test'
import assert from 'node:assert/strict'
import { safeWorkFilters, workDetailHref, workReturnHref, filterLoadedWorkItems } from '../src/features/work/navigation.ts'

test('return path preserves only project board view/filter/search state', () => {
 const current = new URLSearchParams('q=QUAL-13&view=board&kind=task&status=in_review&unsafe=https://example.org')
 const href = workDetailHref('p1', 'i2', current)
 assert.match(href, /^\/projects\/p1\/items\/i2\?from=/)
 assert.equal(workReturnHref('p1', href.slice(href.indexOf('?'))), '/projects/p1?view=board&q=QUAL-13&kind=task&status=in_review')
 assert.equal(workReturnHref('p1', '?from=http%3A%2F%2Fevil.example%2F'), '/projects/p1')
 assert.equal(workReturnHref('p1', ''), '/projects/p1')
})

test('invalid or oversized filters never flow into detail-to-board return route', () => {
 const safe = safeWorkFilters(new URLSearchParams('status=forged&kind=bot&view=%2F%2Fevil.example&q=' + 'x'.repeat(200)))
 assert.equal(safe.get('q')?.length, 120)
 assert.equal(safe.has('status'), false)
 assert.equal(safe.has('kind'), false)
 assert.equal(safe.has('view'), false)
 assert.equal(workReturnHref('p', '?from=' + encodeURIComponent('q=' + 'x'.repeat(1200))), '/projects/p')
})

test('local search respects pagination expansion and does not invent unloaded matches', () => {
 const page1 = [{key:'QUAL-13',title:'界面整改'}]
 const page2 = [{key:'QUAL-14',title:'权限整改'}]
 assert.deepEqual(filterLoadedWorkItems(page1,'QUAL-14'), [])
 assert.deepEqual(filterLoadedWorkItems([...page1,...page2],'QUAL-14').map(x=>x.key), ['QUAL-14'])
})

test('loaded title/key search is case-insensitive and never implies server-wide search', () => {
 const rows = [{ key:'QUAL-13', title:'列表整改'},{key:'QUAL-14', title:'审查整改'},{key:'QUAL-15', title:'测试跟踪'}]
 assert.deepEqual(filterLoadedWorkItems(rows, ' qual-13 ').map(x => x.key), ['QUAL-13'])
 assert.deepEqual(filterLoadedWorkItems(rows, '整 改'), [])
 assert.deepEqual(filterLoadedWorkItems(rows, '整改').map(x => x.key), ['QUAL-13','QUAL-14'])
 assert.equal(filterLoadedWorkItems(rows,'').length, rows.length)
})
