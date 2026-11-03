/** A closed <details> can give its hidden anchors nonempty client rects.
 * Only the first summary (and its descendants) is actually exposed by the
 * browser's native details disclosure while the element is collapsed.
 */
export function isInCollapsedMenuContent(node: Element): boolean {
  const closed = node.closest('details:not([open])')
  return Boolean(closed && !closed.querySelector(':scope > summary')?.contains(node))
}

/** Keyboard focus policy for the mobile modal navigation.
 * -1 represents focus outside the menu (including an inert trigger).
 * The policy never produces an index outside the visible menu controls.
 */
export function nextMobileMenuFocusIndex(
  currentIndex: number,
  count: number,
  reverse: boolean,
): number {
  if (count <= 0) return -1
  if (reverse) return currentIndex <= 0 || currentIndex >= count ? count - 1 : currentIndex - 1
  return currentIndex < 0 || currentIndex >= count - 1 ? 0 : currentIndex + 1
}
