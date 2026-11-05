import { useLayoutEffect, type RefObject } from 'react'

// Motion helpers; motion.css says what each part does.
//
// Blocks marked `data-reveal` play their entrance once, the first time they
// come into view: one IntersectionObserver serves the whole root and nothing
// listens to scroll events. Pages mount blocks long after the root does (data
// arrives, a tab switches), so a MutationObserver hands new blocks to it. The
// root is marked `data-motion` only when this can work and motion is welcome;
// otherwise every block is simply drawn in its final state.

export function prefersReducedMotion(): boolean {
  return typeof window !== 'undefined' && window.matchMedia('(prefers-reduced-motion: reduce)').matches
}

export function useReveal(root: RefObject<HTMLElement | null>) {
  // A layout effect, so blocks already on screen are hidden before the first
  // paint instead of flashing in their final state and then starting over.
  useLayoutEffect(() => {
    const el = root.current
    if (
      !el ||
      prefersReducedMotion() ||
      typeof IntersectionObserver === 'undefined' ||
      typeof MutationObserver === 'undefined'
    )
      return
    el.dataset.motion = 'on'

    const seen = new WeakSet<Element>()
    const io = new IntersectionObserver(
      (entries) => {
        // Siblings arriving together are staggered in reading order (--rv),
        // whatever the grid looks like at this width.
        const arrived = new Map<Element | null, number>()
        for (const entry of entries) {
          if (!entry.isIntersecting) continue
          const block = entry.target as HTMLElement
          const order = arrived.get(block.parentElement) ?? 0
          arrived.set(block.parentElement, order + 1)
          block.style.setProperty('--rv', String(order))
          block.dataset.shown = ''
          io.unobserve(block)
        }
      },
      // Start once a block is a little way above the bottom of the screen.
      { rootMargin: '0px 0px -6% 0px' },
    )

    const watch = (node: ParentNode) => {
      const blocks: Element[] = []
      if (node instanceof Element && node.matches('[data-reveal]')) blocks.push(node)
      node.querySelectorAll?.('[data-reveal]').forEach((b) => blocks.push(b))
      for (const b of blocks) {
        if (seen.has(b) || (b as HTMLElement).dataset.shown !== undefined) continue
        seen.add(b)
        io.observe(b)
      }
    }
    watch(el)

    const mo = new MutationObserver((records) => {
      for (const r of records) r.addedNodes.forEach((n) => n instanceof Element && watch(n))
    })
    mo.observe(el, { childList: true, subtree: true })

    return () => {
      io.disconnect()
      mo.disconnect()
      delete el.dataset.motion
    }
  }, [root])
}
