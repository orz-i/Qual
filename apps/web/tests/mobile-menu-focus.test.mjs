import test from 'node:test'
import assert from 'node:assert/strict'
import { isInCollapsedMenuContent, nextMobileMenuFocusIndex } from '../src/app/mobile-menu-focus.ts'

test('open mobile navigation cycles forward only through visible controls', () => {
  assert.deepEqual([0,1,2,3,4].map(index => nextMobileMenuFocusIndex(index,5,false)),[1,2,3,4,0])
  assert.equal(nextMobileMenuFocusIndex(-1,5,false),0)
  assert.equal(nextMobileMenuFocusIndex(999,5,false),0)
})
test('Shift+Tab wraps the first control to the final visible control', () => {
  assert.deepEqual([0,1,2,3,4].map(index => nextMobileMenuFocusIndex(index,5,true)),[4,0,1,2,3])
  assert.equal(nextMobileMenuFocusIndex(-1,5,true),4)
  assert.equal(nextMobileMenuFocusIndex(999,5,true),4)
})
test('closed native details excludes hidden links but preserves visible summary', () => {
  const summary = { closest: () => details }
  const hiddenLink = { closest: () => details }
  const details = { querySelector: () => ({ contains: node => node === summary }) }
  const outside = { closest: () => null }
  assert.equal(isInCollapsedMenuContent(summary), false)
  assert.equal(isInCollapsedMenuContent(hiddenLink), true)
  assert.equal(isInCollapsedMenuContent(outside), false)
})
test('missing summary inside a collapsed details fails closed', () => {
  const details = { querySelector: () => null }
  assert.equal(isInCollapsedMenuContent({ closest: () => details }), true)
})
test('empty or one-item modal remains bounded', () => {
  assert.equal(nextMobileMenuFocusIndex(-1,0,false),-1)
  assert.equal(nextMobileMenuFocusIndex(0,1,false),0)
  assert.equal(nextMobileMenuFocusIndex(0,1,true),0)
})
