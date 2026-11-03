import test from 'node:test'
import assert from 'node:assert/strict'
import {qualityCounts,filterRunItems,itemStatus,sortByLatestAttention} from '../src/features/tests/quality.ts'

const items=[
 {id:'first',latest_status:'passed'},
 {id:'second',latest_status:'failed'},
 {id:'third',latest_status:'blocked'},
 {id:'fourth',latest_status:'skipped'},
 {id:'fifth',latest_status:null},
]
test('latest reported categories and unreported are distinct',()=>{
 const n=qualityCounts(items)
 assert.deepEqual(n,{passed:1,failed:1,blocked:1,skipped:1,unreported:1,total:5,reported:4})
 assert.equal(itemStatus({latest_status:null}),'unreported')
 assert.equal(qualityCounts([]).reported,0)
})
test('quality filters do not mutate or invent completed statuses',()=>{
 assert.equal(filterRunItems(items,'all').length,5)
 assert.deepEqual(filterRunItems(items,'failed').map(x=>x.id),['second'])
 assert.deepEqual(filterRunItems(items,'unreported').map(x=>x.id),['fifth'])
 assert.equal(items[0].id,'first')
 assert.deepEqual(sortByLatestAttention(items).map(x=>x.id),['second','third','fifth','first','fourth'])
})
test('a later passed result changes only the latest summary, never past Attempt facts',()=>{
 const historical=[{attempt_no:1,status:'failed',bug_key:'QUAL-42'},{attempt_no:2,status:'passed'}]
 const runItem={latest_status:historical.at(-1).status}
 const summary=qualityCounts([runItem])
 assert.equal(summary.passed,1)
 assert.equal(summary.failed,0)
 assert.equal(historical[0].bug_key,'QUAL-42')
 assert.equal(historical[0].status,'failed')
})
