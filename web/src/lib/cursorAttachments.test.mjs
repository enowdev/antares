import { describe, expect, test } from 'bun:test'
import {
  CURSOR_MAX_IMAGES,
  composerImageLimit,
  dataUrlByteLength,
  dataUrlMimeType,
  validateCursorAttachments,
} from './cursorAttachments.ts'

const png = (bytes = 3) => `data:image/png;base64,${'A'.repeat(Math.ceil(bytes / 3) * 4)}`

describe('Cursor attachment preflight', () => {
  test('local documents are rejected, never silently dropped', () => {
    const issue = validateCursorAttachments({
      images: [],
      docs: [
        { path: '/tmp/a.pdf', name: 'a.pdf' },
        { path: '/tmp/b.csv', name: 'b.csv' },
      ],
    })
    expect(issue).toEqual({ code: 'documents', values: { names: 'a.pdf, b.csv' } })
  })

  test('documents are reported before any image problem', () => {
    const issue = validateCursorAttachments({
      images: Array.from({ length: 9 }, () => png()),
      docs: [{ path: '/tmp/a.pdf', name: 'a.pdf' }],
    })
    expect(issue?.code).toBe('documents')
  })

  test('five images are accepted and a sixth is refused', () => {
    expect(CURSOR_MAX_IMAGES).toBe(5)
    expect(
      validateCursorAttachments({ images: Array.from({ length: 5 }, () => png()), docs: [] }),
    ).toBeNull()
    expect(
      validateCursorAttachments({ images: Array.from({ length: 6 }, () => png()), docs: [] }),
    ).toEqual({ code: 'imageCount', values: { max: 5, n: 6 } })
  })

  test('only the MIME types Cursor accepts pass', () => {
    for (const mime of ['image/png', 'image/jpeg', 'image/gif', 'image/webp']) {
      expect(
        validateCursorAttachments({ images: [`data:${mime};base64,AAAA`], docs: [] }),
      ).toBeNull()
    }
    expect(
      validateCursorAttachments({ images: ['data:image/svg+xml;base64,AAAA'], docs: [] }),
    ).toEqual({ code: 'imageType', values: { n: 1, type: 'image/svg+xml' } })
  })

  test('an entry that is not a base64 data URL is refused', () => {
    expect(
      validateCursorAttachments({ images: ['https://example.com/a.png'], docs: [] }),
    ).toEqual({ code: 'imageType', values: { n: 1, type: '' } })
  })

  test('an image over the 15 MiB decoded limit is refused before approval', () => {
    const oversized = png(15 * 1024 * 1024 + 3)
    expect(validateCursorAttachments({ images: [oversized], docs: [] })).toEqual({
      code: 'imageSize',
      values: { n: 1, max: 15 },
    })
  })

  test('composer image limits follow the execution target', () => {
    expect(composerImageLimit('cursor')).toBe(5)
    expect(composerImageLimit('chat')).toBe(4)
  })

  test('data URL helpers read the declared type and decoded size', () => {
    expect(dataUrlMimeType('data:image/webp;base64,AAAA')).toBe('image/webp')
    expect(dataUrlMimeType('nonsense')).toBe('')
    expect(dataUrlByteLength('data:image/png;base64,AAAA')).toBe(3)
    expect(dataUrlByteLength('data:image/png;base64,AAA=')).toBe(2)
    expect(dataUrlByteLength('data:image/png;base64,AA==')).toBe(1)
  })
})
