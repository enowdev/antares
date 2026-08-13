/**
 * Cursor's attachment contract, checked in the composer before anything is
 * sent. The server validates the same rules authoritatively; this preflight
 * exists so a rejection happens before the draft is cleared and long before a
 * paid operation is offered for approval.
 */

export const CURSOR_MAX_IMAGES = 5
export const CURSOR_MAX_IMAGE_BYTES = 15 << 20
export const CURSOR_IMAGE_MIME_TYPES = [
  'image/png',
  'image/jpeg',
  'image/gif',
  'image/webp',
] as const

const CHAT_MAX_IMAGES = 4

/** How many images the composer accepts for the current execution target. */
export function composerImageLimit(kind: 'chat' | 'cursor'): number {
  return kind === 'cursor' ? CURSOR_MAX_IMAGES : CHAT_MAX_IMAGES
}

/** The declared MIME type of a base64 data URL, or "" when it is not one. */
export function dataUrlMimeType(dataUrl: string): string {
  const match = /^data:([^;,]+);base64,/.exec(dataUrl.trim())
  return match ? match[1] : ''
}

/** Decoded size of a base64 data URL, from its payload length alone. */
export function dataUrlByteLength(dataUrl: string): number {
  const payload = dataUrl.slice(dataUrl.indexOf(',') + 1)
  if (!payload) return 0
  const padding = payload.endsWith('==') ? 2 : payload.endsWith('=') ? 1 : 0
  return Math.max(0, Math.floor((payload.length * 3) / 4) - padding)
}

export interface CursorAttachmentIssue {
  code: 'documents' | 'imageCount' | 'imageType' | 'imageSize'
  values: Record<string, string | number>
}

/**
 * The first reason this turn cannot be sent to Cursor, or null. Local documents
 * are reported first: they are rejected outright rather than silently dropped,
 * because a Cursor cloud VM cannot read a path on this machine.
 */
export function validateCursorAttachments(input: {
  images: string[]
  docs: Array<{ name: string }>
}): CursorAttachmentIssue | null {
  const docs = input.docs ?? []
  if (docs.length > 0) {
    return {
      code: 'documents',
      values: { names: docs.map((doc) => doc.name).join(', ') },
    }
  }

  const images = input.images ?? []
  if (images.length > CURSOR_MAX_IMAGES) {
    return {
      code: 'imageCount',
      values: { max: CURSOR_MAX_IMAGES, n: images.length },
    }
  }

  for (let i = 0; i < images.length; i++) {
    const mimeType = dataUrlMimeType(images[i])
    if (!CURSOR_IMAGE_MIME_TYPES.includes(mimeType as (typeof CURSOR_IMAGE_MIME_TYPES)[number])) {
      return { code: 'imageType', values: { n: i + 1, type: mimeType } }
    }
    if (dataUrlByteLength(images[i]) > CURSOR_MAX_IMAGE_BYTES) {
      return {
        code: 'imageSize',
        values: { n: i + 1, max: CURSOR_MAX_IMAGE_BYTES >> 20 },
      }
    }
  }
  return null
}
