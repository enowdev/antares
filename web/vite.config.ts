import { defineConfig, type Plugin } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'
import { gzipSync } from 'node:zlib'
import { readFileSync, writeFileSync } from 'node:fs'
import { relative, resolve } from 'node:path'

/**
 * Records the static-import closure of every emitted entry chunk with the
 * actual on-disk raw + gzip byte counts and the per-module rendered length
 * inside that closure. scripts/bundle-report.mjs consumes it to show top
 * contributors and initial download totals.
 *
 * Split across two hooks by necessity:
 *
 *   generateBundle  — snapshot the graph (imports, modules, isEntry). This
 *                     runs BEFORE Vite substitutes final placeholders into
 *                     chunk.code, so we deliberately do not measure bytes
 *                     here — the earlier `raw532KB` under-count was exactly
 *                     that mistake (asset URL / import-path rewrites happen
 *                     after generateBundle).
 *   writeBundle    — Vite has flushed the final bytes to disk in outDir.
 *                     Read the files back for authoritative raw/gzip totals,
 *                     then write bundle-report.json alongside them. Not
 *                     emitFile: writeBundle runs after Vite's asset writer,
 *                     so anything we emit at that point would be dropped.
 *
 * Module ids are stored as workspace-relative paths so the report does not
 * leak absolute filesystem paths (developer usernames, CI runner layout).
 */
function bundleReport(): Plugin {
  interface ChunkMeta {
    imports: string[]
    modules: Record<string, { renderedLength: number }>
    isEntry: boolean
    name: string
  }

  const graph = new Map<string, ChunkMeta>()
  let outDir = ''
  const workspaceRoot = resolve(__dirname, '..')
  const scrub = (id: string): string => {
    // Vite virtual / rollup helper prefixes stay as-is; only real filesystem
    // paths get made relative.
    if (id.startsWith('\0') || id.startsWith('virtual:') || !id.startsWith('/')) return id
    const rel = relative(workspaceRoot, id)
    return rel.startsWith('..') ? id.split('/node_modules/').pop() ?? id : rel
  }

  return {
    name: 'antares-bundle-report',
    apply: 'build',

    generateBundle(_options, bundle) {
      graph.clear()
      for (const [fileName, asset] of Object.entries(bundle)) {
        if (asset.type !== 'chunk') continue
        graph.set(fileName, {
          imports: asset.imports ?? [],
          modules: Object.fromEntries(
            Object.entries(asset.modules ?? {}).map(([k, v]) => [
              scrub(k),
              { renderedLength: v.renderedLength ?? 0 },
            ]),
          ),
          isEntry: asset.isEntry,
          name: asset.name,
        })
      }
    },

    writeBundle(options) {
      outDir = options.dir ?? resolve(__dirname, 'dist')

      // Closure walk: entry -> all statically-imported descendants.
      const closure = (root: string): string[] => {
        const seen = new Set<string>()
        const stack = [root]
        while (stack.length) {
          const cur = stack.pop()!
          if (seen.has(cur)) continue
          seen.add(cur)
          const c = graph.get(cur)
          if (!c) continue
          for (const imp of c.imports) if (!seen.has(imp)) stack.push(imp)
        }
        return [...seen]
      }

      // Measure the actual on-disk bytes: this is post-substitution, so import
      // paths and asset URLs are final. Cache per-file so shared chunks in
      // multiple entries don't get re-hashed.
      const sizeCache = new Map<string, { rawBytes: number; gzipBytes: number }>()
      const sizeOf = (fileName: string) => {
        const cached = sizeCache.get(fileName)
        if (cached) return cached
        const buf = readFileSync(resolve(outDir, fileName))
        const size = { rawBytes: buf.length, gzipBytes: gzipSync(buf).length }
        sizeCache.set(fileName, size)
        return size
      }

      const entries: Array<{
        entry: string
        name: string
        chunkCount: number
        rawBytes: number
        gzipBytes: number
        chunks: Array<{ file: string; rawBytes: number; gzipBytes: number }>
        modules: Array<{ id: string; chunk: string; renderedLength: number }>
      }> = []

      for (const [fileName, chunk] of graph) {
        if (!chunk.isEntry) continue
        const closureFiles = closure(fileName)
        const perChunk = closureFiles.map((f) => ({ file: f, ...sizeOf(f) }))
        const rawBytes = perChunk.reduce((s, c) => s + c.rawBytes, 0)
        const gzipBytes = perChunk.reduce((s, c) => s + c.gzipBytes, 0)
        const modules: Array<{ id: string; chunk: string; renderedLength: number }> = []
        for (const f of closureFiles) {
          const c = graph.get(f)!
          for (const [id, m] of Object.entries(c.modules)) {
            modules.push({ id, chunk: f, renderedLength: m.renderedLength })
          }
        }
        modules.sort((a, b) => b.renderedLength - a.renderedLength)
        entries.push({
          entry: fileName,
          name: chunk.name,
          chunkCount: closureFiles.length,
          rawBytes,
          gzipBytes,
          chunks: perChunk.sort((a, b) => b.rawBytes - a.rawBytes),
          modules,
        })
      }

      const report = {
        generatedAt: new Date().toISOString(),
        entries: entries.sort((a, b) => b.rawBytes - a.rawBytes),
      }
      // writeBundle runs after Vite's own asset flush, so emitFile is a no-op
      // here. Write directly into the resolved outDir Vite handed us.
      writeFileSync(resolve(outDir, 'bundle-report.json'), JSON.stringify(report, null, 2))
    },
  }
}

// Dev server binds to loopback by default; expose it explicitly by setting
// HOST=0.0.0.0 when a Tailscale/LAN peer needs to reach the dashboard. The
// previous `host: '0.0.0.0'` + `allowedHosts: true` combination made the
// dashboard reachable from any origin the client cared to spoof, which is
// unsafe as a default.
export default defineConfig({
  plugins: [react(), tailwindcss(), bundleReport()],
  resolve: {
    alias: { '@': new URL('./src', import.meta.url).pathname },
  },
  server: {
    host: process.env.HOST ?? '127.0.0.1',
    port: 5173,
    strictPort: true,
    proxy: {
      '/api': {
        // ANTARES_API points the dev dashboard at another server, e.g. an
        // isolated smoke instance, instead of the default local one.
        target: process.env.ANTARES_API ?? 'http://127.0.0.1:8787',
        changeOrigin: true,
        ws: true,
      },
    },
  },
  build: {
    outDir: 'dist',
    emptyOutDir: true,
  },
})
