import type { Session } from '../types.js'
import { BackupError, fail } from './errors.js'
import { INDEX_PATH, MANIFEST_PATH, USER_PATH, parseSessionPath, sessionFilesDir } from './paths.js'
import { checkManifest, readSessionFile, readSessionIndex } from './schema.js'
import { toLocalSession } from './toLocal.js'
import {
  ArchiveError,
  isZipFile,
  readArchive,
  readEntry,
  skipEntry,
  type ArchiveEntry,
} from './zipReader.js'

export type DeepSeekArchive = {
  sessions: Session[]
  /** DeepSeek file UUID → original image bytes. Non-image files have no blob. */
  images: Map<string, Blob>
}

const readJson = async (entry: ArchiveEntry): Promise<unknown> => {
  const text = await (await readEntry(entry)).text()
  try {
    return JSON.parse(text)
  } catch {
    return fail('backupInvalidJson')
  }
}

/**
 * Parse a DeepSeek `deepseek_data-*.zip` (export_format_version 3) into the
 * local Session shape. Unlike the old IndexedDB importer this keeps everything
 * in memory and lets the caller upload images and create chatsvr sessions.
 */
export const readDeepSeekArchive = async (file: File): Promise<DeepSeekArchive> => {
  if (!(await isZipFile(file))) {
    fail('backupUnrecognized')
  }
  const sessions: Session[] = []
  const imported = new Set<string>()
  const images = new Map<string, Blob>()
  const imagePaths = new Map<string, string>()
  let manifest = false
  let index: ReturnType<typeof readSessionIndex> | undefined
  let currentSession: string | undefined

  try {
    for await (const entry of readArchive(file)) {
      const sessionId = parseSessionPath(entry.path)
      if (!manifest) {
        if (entry.path !== MANIFEST_PATH) {
          fail('backupUnrecognized')
        }
        checkManifest(await readJson(entry))
        manifest = true
      } else if (entry.path === USER_PATH && !index && imported.size === 0) {
        await skipEntry(entry)
      } else if (sessionId !== null && !index) {
        const local = toLocalSession(readSessionFile(await readJson(entry)))
        if (local.session.id !== sessionId || imported.has(sessionId)) {
          fail('backupIncomplete')
        }
        imported.add(sessionId)
        currentSession = sessionId
        sessions.push(local.session)
        for (const [imagePath, id] of local.images) {
          imagePaths.set(imagePath, id)
        }
      } else if (entry.path === INDEX_PATH && !index) {
        index = readSessionIndex(await readJson(entry))
      } else if (
        index
          ? imported.has(/^chat_sessions\/([^/]+)_files\//.exec(entry.path)?.[1] ?? '')
          : currentSession && entry.path.startsWith(sessionFilesDir(currentSession))
      ) {
        const id = imagePaths.get(entry.path)
        if (id === undefined) {
          await skipEntry(entry)
        } else if (!images.has(id)) {
          images.set(id, await readEntry(entry))
        } else {
          await skipEntry(entry)
        }
      } else {
        fail('backupIncomplete')
      }
    }
  } catch (error) {
    throw error instanceof ArchiveError ? new BackupError('backupIncomplete') : error
  }

  if (!manifest) {
    fail('backupUnrecognized')
  }
  const listed = new Set(index?.map((entry) => entry.id))
  if (
    !index ||
    listed.size !== index.length ||
    listed.size !== imported.size ||
    [...imported].some((id) => !listed.has(id))
  ) {
    fail('backupIncomplete')
  }
  return { sessions, images }
}
