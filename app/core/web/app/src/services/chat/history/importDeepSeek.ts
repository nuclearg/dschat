import { importSession, uploadAttachment, type ImportSessionPayload } from '../../chatsvr.js'
import type { Session } from '../types.js'
import { readDeepSeekArchive } from './readArchive.js'

const IMAGE_MIME: Record<string, string> = {
  png: 'image/png',
  jpg: 'image/jpeg',
  jpeg: 'image/jpeg',
  gif: 'image/gif',
  webp: 'image/webp',
  bmp: 'image/bmp',
  avif: 'image/avif',
  tif: 'image/tiff',
  tiff: 'image/tiff',
}

const imageMime = (name: string) => {
  const ext = name.slice(name.lastIndexOf('.') + 1).toLowerCase()
  return IMAGE_MIME[ext] ?? ''
}

export type ImportSummary = {
  imported: number
  failed: number
  warnings: number
}

const payloadOf = (
  session: Session,
  uploaded: Map<string, { id: string; mime: string }>
): ImportSessionPayload => ({
  title: session.title,
  createdAt: session.createdAt,
  updatedAt: session.updatedAt,
  messages: session.messages.map((message) => {
    if (message.role === 'user') {
      return {
        id: message.id,
        role: 'user',
        content: message.content,
        createdAt: message.createdAt,
        parentId: message.parentId ?? null,
        selectedChildId: message.selectedChildId ?? null,
        files: message.files.map((file) => {
          const up = uploaded.get(file.id)
          return up
            ? { id: up.id, name: file.name, mime: up.mime, size: file.size, isImage: file.isImage }
            : {
                id: '',
                name: file.name,
                mime: file.mimeType,
                size: file.size,
                isImage: file.isImage,
              }
        }),
      }
    }
    return {
      id: message.id,
      role: 'assistant',
      createdAt: message.createdAt,
      parentId: message.parentId ?? null,
      selectedChildId: message.selectedChildId ?? null,
      blocks: message.blocks,
      status: message.status,
      thinking: message.thinking,
      search: message.search,
    }
  }),
})

/**
 * Import every conversation in a DeepSeek ZIP as a new local chatsvr session.
 * The server creates fresh IDs, so importing the same file twice creates two
 * copies. If a sessionsvr is paired, the server also uploads each new session.
 */
export const importDeepSeek = async (
  file: File,
  onProgress?: (done: number, total: number) => void
): Promise<ImportSummary> => {
  const { sessions, images } = await readDeepSeekArchive(file)
  const uploaded = new Map<string, { id: string; mime: string }>()
  const summary: ImportSummary = { imported: 0, failed: 0, warnings: 0 }

  const uploadFile = async (id: string, name: string, mime: string) => {
    if (uploaded.has(id)) return uploaded.get(id)!
    const blob = images.get(id)
    if (!blob) return null
    const attachment = await uploadAttachment(blob, name, mime || imageMime(name))
    const result = { id: attachment.id, mime: attachment.mime || mime || imageMime(name) }
    uploaded.set(id, result)
    return result
  }

  for (let index = 0; index < sessions.length; index++) {
    const session = sessions[index]
    try {
      for (const message of session.messages) {
        if (message.role !== 'user') continue
        for (const file of message.files) {
          if (!file.isImage) continue
          await uploadFile(file.id, file.name, file.mimeType)
        }
      }
      const result = await importSession(payloadOf(session, uploaded))
      summary.imported += 1
      if (result.warning) summary.warnings += 1
    } catch {
      summary.failed += 1
    }
    onProgress?.(index + 1, sessions.length)
  }
  return summary
}
