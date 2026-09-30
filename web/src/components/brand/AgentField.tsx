import { useEffect, useRef } from 'react'

// A field of nodes behind empty and sign-in screens, drawn on one 2D canvas.
// Taken from the enowx site's hero.
//
// It borrows the product's own picture: agents as nodes, work handed between
// them. Left alone, nodes drift, neighbours stay faintly linked, and pulses
// hop from node to node (a task being delegated, sometimes passed on again).
// The cursor acts as the router: nearby nodes wake up, lean toward it without
// clumping, and it sends work out to them. A click or tap on empty space sends
// a burst, which is how touch screens get to interact at all.
//
// Kept cheap: node count scales with area and is capped, ambient links are
// batched into a few paths by opacity, the pixel ratio is capped, the loop
// draws at most 60 frames a second (a 120Hz screen would otherwise draw twice
// as often, and move the field twice as fast), and it stops while the tab is
// hidden or the canvas is scrolled out of view. With prefers-reduced-motion it
// draws still frames only.

type Node = {
  x: number
  y: number
  vx: number
  vy: number
  bx: number // resting drift the node eases back to
  by: number
  r: number
  hub: boolean
}
type Point = { x: number; y: number }
type Pulse = { from: Point; to: Node; start: number; hops: number }

// The ink is --field-ink (the theme's text colour as "r, g, b"), read again
// when the theme changes; it is used with alpha only.
let INK = '244, 244, 245'
function readInk() {
  const v = getComputedStyle(document.documentElement).getPropertyValue('--field-ink').trim()
  if (v) INK = v
}
const REACH = 230 // px around the cursor where nodes wake up
const LINK = 115 // max distance for a link between two nodes
const PULSE_EVERY = 650 // ms between ambient handoffs
const CURSOR_SENDS_EVERY = 900 // ms between handoffs from the cursor
const PULSE_FOR = 950 // ms a pulse takes to cross one link
const MAX_PULSES = 10
const BUCKETS = 4 // opacity levels ambient links are batched into

export function AgentField({ className }: { className?: string }) {
  const canvasRef = useRef<HTMLCanvasElement>(null)

  useEffect(() => {
    const canvas = canvasRef.current
    const ctx = canvas?.getContext('2d')
    if (!canvas || !ctx) return

    const still = window.matchMedia('(prefers-reduced-motion: reduce)').matches
    let width = 0
    let height = 0
    let nodes: Node[] = []
    const pulses: Pulse[] = []
    let lastPulse = 0
    let lastSend = 0
    // Pointer in canvas space; `at` eases toward `target` so the glow trails
    // the cursor instead of snapping to it.
    const target = { x: -1e4, y: -1e4, on: false }
    const at = { x: -1e4, y: -1e4 }

    function seed() {
      const dpr = Math.min(window.devicePixelRatio || 1, 1.5)
      const rect = canvas!.getBoundingClientRect()
      width = rect.width
      height = rect.height
      canvas!.width = Math.round(width * dpr)
      canvas!.height = Math.round(height * dpr)
      ctx!.setTransform(dpr, 0, 0, dpr, 0, 0)
      const count = Math.max(36, Math.min(130, Math.round((width * height) / 11000)))
      nodes = Array.from({ length: count }, () => {
        const vx = (Math.random() - 0.5) * 0.22
        const vy = (Math.random() - 0.5) * 0.22
        const hub = Math.random() < 0.08
        return {
          x: Math.random() * width,
          y: Math.random() * height,
          vx,
          vy,
          bx: vx,
          by: vy,
          r: hub ? 2 : 0.8 + Math.random() * 0.8,
          hub,
        }
      })
      pulses.length = 0
    }

    function neighbour(of: Point, exclude?: Node): Node | null {
      // A random node within reach of `of`, not too close to read as a hop.
      let pick: Node | null = null
      let seen = 0
      for (const n of nodes) {
        if (n === exclude) continue
        const d = Math.hypot(n.x - of.x, n.y - of.y)
        if (d > 45 && d < 190 && Math.random() < 1 / ++seen) pick = n
      }
      return pick
    }

    function send(from: Point, to: Node | null, now: number, hops: number) {
      if (!to || pulses.length >= MAX_PULSES) return
      pulses.push({ from: { x: from.x, y: from.y }, to, start: now, hops })
    }

    function burst(x: number, y: number) {
      const now = performance.now()
      const nearest = [...nodes]
        .map((n) => ({ n, d: Math.hypot(n.x - x, n.y - y) }))
        .filter((e) => e.d > 20 && e.d < 260)
        .sort((a, b) => a.d - b.d)
        .slice(0, 5)
      for (const { n } of nearest) send({ x, y }, n, now, 1)
    }

    function draw(now: number) {
      ctx!.clearRect(0, 0, width, height)
      at.x += (target.x - at.x) * 0.12
      at.y += (target.y - at.y) * 0.12

      const weight = new Float32Array(nodes.length)
      for (let i = 0; i < nodes.length; i++) {
        const n = nodes[i]
        const dx = at.x - n.x
        const dy = at.y - n.y
        const d = target.on ? Math.hypot(dx, dy) : Infinity
        const w = d < REACH ? 1 - d / REACH : 0 // 0 far, 1 at the cursor
        weight[i] = w
        if (!still) {
          if (w > 0 && d > 1) {
            // Lean in from afar, keep a little room close up.
            const pull = d > 70 ? 0.006 : -0.02
            n.vx += (dx / d) * pull * w
            n.vy += (dy / d) * pull * w
          }
          n.vx += (n.bx - n.vx) * 0.02
          n.vy += (n.by - n.vy) * 0.02
          n.x += n.vx
          n.y += n.vy
          if (n.x < -10) n.x = width + 10
          else if (n.x > width + 10) n.x = -10
          if (n.y < -10) n.y = height + 10
          else if (n.y > height + 10) n.y = -10
        }
      }

      // Ambient links, batched by opacity: a handful of strokes per frame.
      const paths = Array.from({ length: BUCKETS }, () => new Path2D())
      const used = new Array<boolean>(BUCKETS).fill(false)
      for (let i = 0; i < nodes.length; i++) {
        const a = nodes[i]
        for (let j = i + 1; j < nodes.length; j++) {
          const b = nodes[j]
          const dx = a.x - b.x
          if (dx > LINK || dx < -LINK) continue
          const dy = a.y - b.y
          if (dy > LINK || dy < -LINK) continue
          const d = Math.hypot(dx, dy)
          if (d > LINK) continue
          const strength = (1 - d / LINK) * (1 + 3 * Math.max(weight[i], weight[j]))
          const k = Math.min(BUCKETS - 1, Math.floor(strength * 1.2))
          paths[k].moveTo(a.x, a.y)
          paths[k].lineTo(b.x, b.y)
          used[k] = true
        }
      }
      ctx!.lineWidth = 1
      for (let k = 0; k < BUCKETS; k++) {
        if (!used[k]) continue
        ctx!.strokeStyle = `rgba(${INK}, ${0.045 + k * 0.05})`
        ctx!.stroke(paths[k])
      }

      // Cursor links to woken nodes.
      if (target.on) {
        for (let i = 0; i < nodes.length; i++) {
          const w = weight[i]
          if (w <= 0.15) continue
          ctx!.strokeStyle = `rgba(${INK}, ${w * 0.2})`
          ctx!.beginPath()
          ctx!.moveTo(nodes[i].x, nodes[i].y)
          ctx!.lineTo(at.x, at.y)
          ctx!.stroke()
        }
      }

      for (let i = 0; i < nodes.length; i++) {
        const n = nodes[i]
        const w = weight[i]
        ctx!.fillStyle = `rgba(${INK}, ${(n.hub ? 0.38 : 0.22) + w * 0.55})`
        ctx!.beginPath()
        ctx!.arc(n.x, n.y, n.r + w * 1, 0, Math.PI * 2)
        ctx!.fill()
        if (n.hub) {
          // Hubs carry a faint ring: the agents others hand work to.
          ctx!.strokeStyle = `rgba(${INK}, ${0.1 + w * 0.3})`
          ctx!.beginPath()
          ctx!.arc(n.x, n.y, n.r + 4 + w * 2, 0, Math.PI * 2)
          ctx!.stroke()
        }
      }

      if (still) return

      if (now - lastPulse > PULSE_EVERY && nodes.length) {
        lastPulse = now
        const from = nodes[(Math.random() * nodes.length) | 0]
        send(from, neighbour(from, from), now, 1 + ((Math.random() * 3) | 0))
      }
      if (target.on && now - lastSend > CURSOR_SENDS_EVERY) {
        lastSend = now
        send(at, neighbour(at), now, 1)
      }

      for (let i = pulses.length - 1; i >= 0; i--) {
        const p = pulses[i]
        const t = (now - p.start) / PULSE_FOR
        if (t >= 1) {
          pulses.splice(i, 1)
          // Passed on: the node that received the work delegates again.
          if (p.hops > 1) send(p.to, neighbour(p.to, p.to), now, p.hops - 1)
          continue
        }
        const fade = Math.sin(Math.PI * t)
        ctx!.strokeStyle = `rgba(${INK}, ${0.16 * fade})`
        ctx!.beginPath()
        ctx!.moveTo(p.from.x, p.from.y)
        ctx!.lineTo(p.to.x, p.to.y)
        ctx!.stroke()
        const e = t < 0.5 ? 2 * t * t : 1 - (-2 * t + 2) ** 2 / 2
        ctx!.fillStyle = `rgba(${INK}, ${0.85 * fade})`
        ctx!.beginPath()
        ctx!.arc(p.from.x + (p.to.x - p.from.x) * e, p.from.y + (p.to.y - p.from.y) * e, 1.7, 0, Math.PI * 2)
        ctx!.fill()
      }
    }

    let frame = 0
    let running = false
    let visible = true
    let drawn = 0
    // Schedule first, then draw: a throw inside draw() then shows up in the
    // console every frame instead of silently freezing the field.
    const loop = (now: number) => {
      frame = requestAnimationFrame(loop)
      if (now - drawn < 12) return
      drawn = now
      draw(now)
    }
    function sync() {
      const want = !still && visible && document.visibilityState === 'visible'
      if (want && !running) {
        running = true
        frame = requestAnimationFrame(loop)
      } else if (!want && running) {
        running = false
        cancelAnimationFrame(frame)
      }
    }

    function local(event: PointerEvent): Point {
      const rect = canvas!.getBoundingClientRect()
      return { x: event.clientX - rect.left, y: event.clientY - rect.top }
    }
    function onMove(event: PointerEvent) {
      if (event.pointerType !== 'mouse') return // touch has no hover to follow
      const p = local(event)
      target.x = p.x
      target.y = p.y
      if (!target.on) {
        at.x = p.x
        at.y = p.y
      }
      target.on = true
      if (still) draw(performance.now())
    }
    function onLeave() {
      target.on = false
      if (still) draw(performance.now())
    }
    function onDown(event: PointerEvent) {
      // Only on empty space: clicking the form or a link is not a gesture.
      if (still || !(event.target instanceof Element)) return
      if (event.target.closest('a, button, input, label, textarea, select, [role=dialog]')) return
      const p = local(event)
      if (p.y < 0 || p.y > height) return
      burst(p.x, p.y)
    }

    readInk()
    seed()
    draw(performance.now())
    const theme = new MutationObserver(() => {
      readInk()
      draw(performance.now())
    })
    theme.observe(document.documentElement, { attributes: true, attributeFilter: ['class'] })

    const resize = new ResizeObserver(() => {
      seed()
      draw(performance.now())
    })
    resize.observe(canvas)
    const seen = new IntersectionObserver(([entry]) => {
      visible = entry.isIntersecting
      sync()
    })
    seen.observe(canvas)
    document.addEventListener('visibilitychange', sync)
    window.addEventListener('pointermove', onMove, { passive: true })
    window.addEventListener('pointerdown', onDown, { passive: true })
    document.documentElement.addEventListener('pointerleave', onLeave)
    sync()

    return () => {
      cancelAnimationFrame(frame)
      theme.disconnect()
      resize.disconnect()
      seen.disconnect()
      document.removeEventListener('visibilitychange', sync)
      window.removeEventListener('pointermove', onMove)
      window.removeEventListener('pointerdown', onDown)
      document.documentElement.removeEventListener('pointerleave', onLeave)
    }
  }, [])

  return (
    <canvas
      ref={canvasRef}
      className={className ? `agent-field ${className}` : 'agent-field'}
      aria-hidden="true"
    />
  )
}
