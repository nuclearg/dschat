import type {
  AssistantMessage,
  FileAttachment,
  Message,
  Session,
  SessionSummary,
  UserMessage,
} from './chat/types.js'

/** Everything below is same-origin: the web layer must only talk to chatsvr on 5217. */

export class ApiError extends Error {
  constructor(
    readonly code: string,
    message: string,
    readonly status: number
  ) {
    super(message)
    this.name = 'ApiError'
  }
}

const request = async <T>(path: string, init?: RequestInit): Promise<T> => {
  const headers = new Headers(init?.headers)
  headers.set(
    'X-Language',
    document.documentElement.lang || navigator.language || 'zh-CN'
  )
  const res = await fetch(path, { credentials: 'same-origin', ...init, headers })
  const text = await res.text()
  let data: unknown = null
  try {
    data = text ? JSON.parse(text) : null
  } catch {
    data = null
  }
  if (!res.ok) {
    const body = data as { error?: { code?: string; message?: string } } | null
    throw new ApiError(
      body?.error?.code ?? 'HTTP_ERROR',
      body?.error?.message ?? `HTTP ${res.status}`,
      res.status
    )
  }
  return data as T
}

const json = (method: string, body: unknown): RequestInit => ({
  method,
  headers: { 'Content-Type': 'application/json' },
  body: JSON.stringify(body),
})

export type ApiAttachment = {
  id: string
  name: string
  mime: string
  size: number
  isImage?: boolean
}

export type ApiQ = {
  text: string
  attachments?: ApiAttachment[]
  createdAt: number
}

export type ApiUsage = {
  input_tokens: number
  output_tokens: number
  cache_creation_input_tokens: number
  cache_read_input_tokens: number
  web_search_requests?: number
}

export type ApiAnswer = {
  status: 'done' | 'aborted' | 'streaming'
  stopReason?: string
  blocks: unknown[]
  usage?: ApiUsage
  model?: string
  thinking?: boolean
  search?: boolean
  title?: string
  durationMs?: number
  createdAt: number
}

export type ApiTurn = {
  id: string
  parentTurnId: string
  q: ApiQ
  createdAt: number
  answer?: ApiAnswer
}

export type TransferState = 'synced' | 'local' | 'pending' | 'uploading' | 'blocked' | 'fragment'

export type ApiSession = {
  id: string
  title: string
  ownerDeviceId: string
  createdAt: number
  updatedAt: number
  syncState?: string
  transferState?: TransferState
  hasFragment?: boolean
}

export type ApiSessionView = ApiSession & {
  readOnly?: boolean
  hasOpenTurn?: boolean
}

export type ApiDetail = {
  session: ApiSession
  turns: ApiTurn[]
  path: string[]
  readOnly: boolean
  hasOpenTurn: boolean
  localOutput?: Record<string, unknown[]>
}

export type ApiServer = { paired: boolean; url: string; deviceId: string }

export type ApiBootstrap = {
  device: { id: string; name: string }
  settings: {
    hasKey: boolean
    baseUrl: string
    model: string
    thinking: boolean
    search: boolean
    deviceName: string
  }
  server: ApiServer
  sessions: ApiSessionView[]
}

export type ApiSettingsView = ApiBootstrap['settings'] & {
  deviceId: string
  server: ApiServer
}

/** Model context window used for the ctx% ring. */
export const CONTEXT_WINDOW = 1_000_000

export const Q_PREFIX = 'q:'
export const turnIdOf = (messageId: string) =>
  messageId.startsWith(Q_PREFIX) ? messageId.slice(Q_PREFIX.length) : messageId

export const bootstrap = () => request<ApiBootstrap>('/api/bootstrap')
export const getSettings = () => request<ApiSettingsView>('/api/settings')
export const saveSettings = (patch: Record<string, unknown>) =>
  request<ApiSettingsView>('/api/settings', json('PUT', patch))
export const verifyKey = (apiKey: string) =>
  request<{ ok: boolean }>('/api/settings/verify', json('POST', { apiKey }))

export const getSession = (id: string) => request<ApiDetail>(`/api/sessions/${id}`)
export const listSessions = () => request<{ sessions: ApiSessionView[] }>('/api/sessions')
export const createSession = () => request<ApiSession>('/api/sessions', json('POST', {}))
export const renameSession = (id: string, title: string) =>
  request<{ ok: boolean }>(`/api/sessions/${id}`, json('PATCH', { title }))
export const deleteSession = (id: string) =>
  request<{ ok: boolean }>(`/api/sessions/${id}`, { method: 'DELETE' })

export type DeleteAllResult = { deleted: number; skipped: number; failed: number }

export const deleteAllSessions = () =>
  request<DeleteAllResult>('/api/sessions', { method: 'DELETE' })

export const sendTurn = (sessionId: string, text: string, attachmentIds: string[]) =>
  request<ApiTurn>(`/api/sessions/${sessionId}/turns`, json('POST', { text, attachmentIds }))
export const retryTurn = (sessionId: string, turnId: string) =>
  request<ApiTurn>(`/api/sessions/${sessionId}/turns`, json('POST', { retryOf: turnId }))
export const editTurn = (sessionId: string, turnId: string, text: string) =>
  request<ApiTurn>(`/api/sessions/${sessionId}/turns`, json('POST', { editOf: turnId, text }))
export const stopTurn = (sessionId: string, turnId: string) =>
  request<{ ok: boolean }>(`/api/sessions/${sessionId}/turns/${turnId}/stop`, { method: 'POST' })
export const selectTurn = (sessionId: string, turnId: string) =>
  request<{ ok: boolean }>(`/api/sessions/${sessionId}/select`, json('POST', { turnId }))
export const syncSession = (sessionId: string) =>
  request<{ ok: boolean }>(`/api/sessions/${sessionId}/sync`, { method: 'POST' })
export const claimSession = (sessionId: string, force = true) =>
  request<{ ok: boolean }>(`/api/sessions/${sessionId}/claim`, json('POST', { force }))

export type ImportFilePayload = {
  id: string
  name: string
  mime: string
  size: number
  isImage: boolean
}

export type ImportMessagePayload = {
  id: string
  role: 'user' | 'assistant'
  createdAt: number
  parentId: string | null
  selectedChildId: string | null
  content?: string
  files?: ImportFilePayload[]
  blocks?: unknown[]
  status?: string
  thinking?: boolean
  search?: boolean
}

export type ImportSessionPayload = {
  title: string
  createdAt: number
  updatedAt: number
  messages: ImportMessagePayload[]
}

export const importSession = (payload: ImportSessionPayload) =>
  request<{ session: ApiSession; synced: boolean; warning?: string }>(
    '/api/sessions/import',
    json('POST', payload)
  )

export const uploadAttachment = async (
  blob: Blob,
  name: string,
  mime?: string,
  signal?: AbortSignal
): Promise<ApiAttachment> => {
  const form = new FormData()
  form.append('file', mime ? new Blob([blob], { type: mime }) : blob, name)
  return request<ApiAttachment>('/api/attachments', { method: 'POST', body: form, signal })
}

export const loginServer = (url: string, password: string) =>
  request<{ paired: boolean; url: string; deviceId: string }>(
    '/api/server/login',
    json('POST', { url, password })
  )
export const disconnectServer = () =>
  request<{ paired: boolean }>('/api/server/disconnect', { method: 'POST' })

export const openEvents = () => new EventSource('/api/events')

const toFile = (a: ApiAttachment): FileAttachment => ({
  id: a.id,
  serverId: a.id,
  name: a.name,
  size: a.size,
  isImage: !!a.isImage,
  mimeType: a.mime,
})

export const toSummary = (s: ApiSession): SessionSummary => ({
  id: s.id,
  title: s.title,
  createdAt: s.createdAt,
  updatedAt: s.updatedAt,
  ownerDeviceId: s.ownerDeviceId,
  syncState: s.syncState,
  transferState: s.transferState,
  hasFragment: s.hasFragment,
})

/** Turn tree -> the demo's message tree. One turn becomes a Q/A pair. */
export const toSession = (detail: ApiDetail): Session => {
  const turns = [...detail.turns].sort((a, b) => a.createdAt - b.createdAt)
  const messages: Message[] = []
  const nextOnPath = new Map<string, string>()
  for (let i = 0; i + 1 < detail.path.length; i++) {
    nextOnPath.set(detail.path[i], detail.path[i + 1])
  }

  for (const turn of turns) {
    const qid = Q_PREFIX + turn.id
    const q: UserMessage = {
      id: qid,
      role: 'user',
      content: turn.q.text,
      files: (turn.q.attachments ?? []).map(toFile),
      createdAt: turn.q.createdAt,
      parentId: turn.parentTurnId === '0' ? null : turn.parentTurnId,
      selectedChildId: turn.id,
    }
    messages.push(q)

    const answer = turn.answer
    const reply: AssistantMessage = {
      id: turn.id,
      role: 'assistant',
      createdAt: answer?.createdAt ?? turn.createdAt,
      model: (answer?.model as AssistantMessage['model']) ?? null,
      thinking: answer?.thinking ?? false,
      search: answer?.search ?? false,
      prefix: false,
      status: answer
        ? answer.status === 'done'
          ? 'done'
          : answer.status === 'streaming'
            ? 'streaming'
            : 'stopped'
        : 'pending',
      blocks: (answer?.blocks ?? []) as AssistantMessage['blocks'],
      stopReason: (answer?.stopReason as AssistantMessage['stopReason']) ?? undefined,
      usage: answer?.usage as AssistantMessage['usage'],
      durationMs: answer?.durationMs,
      parentId: qid,
      selectedChildId: (() => {
        const next = nextOnPath.get(turn.id)
        return next ? Q_PREFIX + next : undefined
      })(),
    }
    messages.push(reply)
  }

  const root = detail.path.length > 0 ? Q_PREFIX + detail.path[0] : null
  return {
    id: detail.session.id,
    title: detail.session.title,
    createdAt: detail.session.createdAt,
    updatedAt: detail.session.updatedAt,
    messages,
    rootMessageId: root,
    readOnly: detail.readOnly,
  }
}
