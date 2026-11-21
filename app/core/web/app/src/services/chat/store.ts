import { create } from 'zustand'
import { showToast } from '../../components/base/toastState.js'
import {
  ApiError,
  Q_PREFIX,
  bootstrap as apiBootstrap,
  disconnectServer as apiDisconnectServer,
  loginServer as apiLoginServer,
  officialLoginServer as apiOfficialLoginServer,
  type ApiServer,
  claimSession,
  createSession,
  deleteSession,
  editTurn,
  getSession,
  listSessions,
  openEvents,
  renameSession as apiRenameSession,
  retryTurn,
  selectTurn,
  sendTurn,
  stopTurn,
  toSession,
  toSummary,
  turnIdOf,
} from '../chatsvr.js'
import { useSettings } from '../settings.js'
import { useUploads } from '../attachments.js'
import { getVisibleMessages } from './branches.js'
import { createLatestReminder } from './latestReminder.js'
import { clearSentDraft, getDraft, prepareDraftMessage } from './drafts.js'
import type { AssistantMessage, ReplyBlock, Session, SessionSummary } from './types.js'

type ChatState = {
  ready: boolean
  initError: boolean
  importingHistory: boolean
  sessions: SessionSummary[]
  sessionCache: Record<string, Session>
  sessionLoadErrors: Record<string, boolean>
  sessionSaveErrors: Record<string, boolean>
  /** null 表示新对话（欢迎页） */
  activeId: string | null
  streaming: Record<string, AbortController>
  server: ApiServer
  /** 注册/找回密码页面的地址前缀；为空表示拿不到（未登录也应当有值） */
  officialBaseUrl: string
}

export const useChatStore = create<ChatState>(() => ({
  ready: false,
  initError: false,
  importingHistory: false,
  sessions: [],
  sessionCache: {},
  sessionLoadErrors: {},
  sessionSaveErrors: {},
  activeId: null,
  streaming: {},
  server: { paired: false, url: '', deviceId: '', kind: '' },
  officialBaseUrl: '',
}))

const { getState: get, setState: set } = useChatStore

const getSessionById = (id: string) => {
  const cache = get().sessionCache
  return Object.hasOwn(cache, id) ? cache[id] : undefined
}

const streamTurns = new Map<string, string>()
let bootstrapTask: Promise<void> | null = null
let events: EventSource | null = null
let sawHello = false
const timers = new Map<string, number>()

const later = (key: string, run: () => void, delay = 250) => {
  const prev = timers.get(key)
  if (prev) {window.clearTimeout(prev)}
  timers.set(
    key,
    window.setTimeout(() => {
      timers.delete(key)
      run()
    }, delay)
  )
}

const messageOf = (error: unknown) =>
  error instanceof ApiError ? error.message : error instanceof Error ? error.message : String(error)

const markStreaming = (sessionId: string, turnId: string) => {
  streamTurns.set(sessionId, turnId)
  set((state) => ({
    streaming: { ...state.streaming, [sessionId]: new AbortController() },
  }))
}

const clearStreaming = (sessionId: string) => {
  streamTurns.delete(sessionId)
  set((state) => {
    if (!Object.hasOwn(state.streaming, sessionId)) {return state}
    const streaming = { ...state.streaming }
    Reflect.deleteProperty(streaming, sessionId)
    return { streaming }
  })
}

export const refreshSessions = async () => {
  try {
    const res = await listSessions()
    const sessions = res.sessions.map(toSummary).sort((a, b) => b.updatedAt - a.updatedAt)
    set({ sessions })
  } catch (error) {
    showToast(messageOf(error))
  }
}

const loadSession = async (id: string, force = false) => {
  if (!force && getSessionById(id)) {return}
  try {
    const detail = await getSession(id)
    const session = toSession(detail)
    set((state) => ({
      sessionCache: { ...state.sessionCache, [id]: session },
      sessionLoadErrors: { ...state.sessionLoadErrors, [id]: false },
    }))
    // 本地或远端有未完成的回合时，让 UI 进入生成中状态（远端会话显示等待点）。
    const live = session.messages.find(
      (message) => message.role === 'assistant' && (message.status === 'pending' || message.status === 'streaming')
    )
    if (live) {markStreaming(id, live.id)}
    else {clearStreaming(id)}
  } catch (error) {
    set((state) => ({
      sessionLoadErrors: { ...state.sessionLoadErrors, [id]: true },
    }))
    if (!(error instanceof ApiError && error.status === 404)) {
      showToast(messageOf(error))
    }
  }
}

export const initChat = (): Promise<void> => {
  if (bootstrapTask) {return bootstrapTask}
  bootstrapTask = (async () => {
    try {
      const boot = await apiBootstrap()
      useSettings.setState({
        apiKey: boot.settings.hasKey ? 'managed-by-chatsvr' : null,
        thinking: boot.settings.thinking,
        search: boot.settings.search,
        safetyCheck: boot.settings.safetyCheck,
        autoStart: boot.settings.autoStart,
        autoStartSupported: boot.settings.autoStartSupported,
      })
      const sessions = boot.sessions.map(toSummary).sort((a, b) => b.updatedAt - a.updatedAt)
      set({ sessions, server: boot.server, officialBaseUrl: boot.officialBaseUrl ?? '', ready: true, initError: false })
      connectEvents()
    } catch (error) {
      set({ ready: false, initError: true })
      bootstrapTask = null
      showToast(messageOf(error))
    }
  })()
  return bootstrapTask
}

export const loginServer = async (url: string, password: string) => {
  const result = await apiLoginServer(url, password)
  set({ server: { paired: true, url: result.url, deviceId: result.deviceId, kind: result.kind } })
  await refreshSessions()
  return result
}




/**
 * 官方模式登录：手机号 + 密码。成功后与自建一样切成已连接并拉一次会话。
 *
 * 注册与找回密码不在客户端：那是 cloudsvr 上的网页（/register、/reset），
 * 客户端只提供链接。
 */
export const officialLoginServer = async (phone: string, password: string) => {
  const result = await apiOfficialLoginServer(phone, password)
  set({ server: { paired: true, url: result.url, deviceId: result.deviceId, kind: result.kind } })
  await refreshSessions()
  return result
}

export const disconnectServer = async () => {
  await apiDisconnectServer()
  set({ server: { paired: false, url: '', deviceId: '', kind: '' } })
  await refreshSessions()
}

export const bindChatPersistence = () => () => {}

export const retrySaveSessions = () => {}

export const newChat = () => {
  set({ activeId: null })
}

export const selectSession = (id: string) => {
  set({ activeId: id })
  void loadSession(id, true)
}

export const renameSession = (id: string, title: string) => {
  set((state) => {
    const sessions = state.sessions.map((s) => (s.id === id ? { ...s, title } : s))
    const session = state.sessionCache[id]
    return {
      sessions,
      sessionCache: session
        ? { ...state.sessionCache, [id]: { ...session, title } }
        : state.sessionCache,
    }
  })
  void apiRenameSession(id, title).catch((error) => {
    showToast(messageOf(error))
    void refreshSessions()
  })
}

export const removeSession = async (id: string): Promise<boolean> => {
  try {
    await deleteSession(id)
  } catch (error) {
    showToast(messageOf(error))
    return false
  }
  set((state) => {
    const sessionCache = { ...state.sessionCache }
    Reflect.deleteProperty(sessionCache, id)
    return {
      sessions: state.sessions.filter((s) => s.id !== id),
      sessionCache,
      activeId: state.activeId === id ? null : state.activeId,
    }
  })
  return true
}

export const stopReply = (sessionId: string) => {
  const turnId = streamTurns.get(sessionId)
  if (!turnId) {return}
  void stopTurn(sessionId, turnId).catch((error) => {
    showToast(messageOf(error))
    clearStreaming(sessionId)
  })
}

/** 当前时刻的 latest_reminder：日期、星期、地点、平台、语言，与官方 demo 一致。 */
const currentReminder = () => {
  const { locale, location } = useSettings.getState()
  return createLatestReminder({ locale, location })
}

export const sendMessage = (targetSessionId: string | null): boolean => {
  if (get().importingHistory || !get().ready) {return false}
  const prepared = prepareDraftMessage(getDraft(targetSessionId), useUploads.getState().tasks)
  if (!prepared) {return false}
  void (async () => {
    try {
      let sessionId = targetSessionId
      if (sessionId === null) {
        const created = await createSession()
        sessionId = created.id
        set({ activeId: sessionId })
      }
      const turn = await sendTurn(
        sessionId,
        prepared.text,
        prepared.files.map((file) => file.serverId),
        currentReminder()
      )
      clearSentDraft(targetSessionId)
      markStreaming(sessionId, turn.id)
      await refreshSessions()
      await loadSession(sessionId, true)
    } catch (error) {
      showToast(messageOf(error))
    }
  })()
  return true
}

export const regenerateReply = (sessionId: string, messageId?: string) => {
  const session = getSessionById(sessionId)
  if (!session) {return}
  const path = getVisibleMessages(session)
  const id = messageId ?? path.at(-1)?.id
  if (!id || id.startsWith(Q_PREFIX)) {return}
  void (async () => {
    try {
      const turn = await retryTurn(sessionId, id, currentReminder())
      markStreaming(sessionId, turn.id)
      await refreshSessions()
      await loadSession(sessionId, true)
    } catch (error) {
      showToast(messageOf(error))
    }
  })()
}

export const editMessage = (sessionId: string, messageId: string, content: string): boolean => {
  const turnId = turnIdOf(messageId)
  if (!turnId) {return false}
  void (async () => {
    try {
      const turn = await editTurn(sessionId, turnId, content, currentReminder())
      markStreaming(sessionId, turn.id)
      await refreshSessions()
      await loadSession(sessionId, true)
    } catch (error) {
      showToast(messageOf(error))
    }
  })()
  return true
}

export const selectMessageVersion = (sessionId: string, messageId: string) => {
  const turnId = turnIdOf(messageId)
  void (async () => {
    try {
      await selectTurn(sessionId, turnId)
      await loadSession(sessionId, true)
    } catch (error) {
      showToast(messageOf(error))
    }
  })()
}

export const claim = (sessionId: string, force = true) =>
  claimSession(sessionId, force).then(() => loadSession(sessionId, true))


const applyToReply = (
  sessionId: string,
  turnId: string,
  update: (reply: AssistantMessage) => AssistantMessage
) => {
  set((state) => {
    const session = state.sessionCache[sessionId]
    if (!session) {return state}
    let found = false
    const messages = session.messages.map((message) => {
      if (message.role !== 'assistant' || message.id !== turnId) {return message}
      found = true
      return update(message)
    })
    if (!found) {return state}
    return {
      sessionCache: { ...state.sessionCache, [sessionId]: { ...session, messages } },
    }
  })
}

const cloneBlock = (block: unknown): ReplyBlock =>
  JSON.parse(JSON.stringify(block)) as ReplyBlock

const ensureLiveBlock = (reply: AssistantMessage, index: number, kind: 'text' | 'thinking') => {
  const blocks = [...reply.blocks]
  while (blocks.length <= index) {blocks.push({ type: 'text', text: '' })}
  if (!blocks[index]) {blocks[index] = { type: kind, [kind]: '' } as unknown as ReplyBlock}
  return blocks
}

const applyEvent = (event: Record<string, unknown>) => {
  const type = event.type as string
  const sessionId = event.sessionId as string | undefined
  const turnId = event.turnId as string | undefined
  if (type === 'hello') {
    if (sawHello) {
      void refreshSessions()
      const active = get().activeId
      if (active) {void loadSession(active, true)}
    }
    sawHello = true
    return
  }
  if (type === 'turn_finished') {
    if (sessionId) {
      clearStreaming(sessionId)
      later(`session:${sessionId}`, () => void loadSession(sessionId, true))
      later('sessions', () => void refreshSessions())
    }
    return
  }
  if (type === 'turn_preempted') {
    if (sessionId) {clearStreaming(sessionId)}
    showToast('已被其他设备接管，本轮输出已作废')
    if (sessionId) {later(`session:${sessionId}`, () => void loadSession(sessionId, true))}
    return
  }
  if (type === 'error') {
    showToast(String(event.message ?? '生成失败'))
    return
  }
  if (type === 'session_updated' || type === 'remote_changed' || type === 'sessions_changed') {
    later('sessions', () => void refreshSessions())
    const active = get().activeId
    if (active && (!sessionId || sessionId === active)) {
      later(`session:${active}`, () => void loadSession(active, true))
    }
    return
  }
  if (type === 'session_deleted') {
    later('sessions', () => void refreshSessions())
    if (sessionId === get().activeId) {set({ activeId: null })}
    return
  }
  if (type === 'session_created') {
    later('sessions', () => void refreshSessions())
    return
  }
  if (!sessionId || !turnId) {return}

  if (type === 'turn_started') {
    if (!Object.hasOwn(get().streaming, sessionId)) {markStreaming(sessionId, turnId)}
    return
  }
  if (type === 'block_start') {
    const index = Number(event.index)
    const block = event.block
    applyToReply(sessionId, turnId, (reply) => {
      const blocks = [...reply.blocks]
      blocks[index] = cloneBlock(block)
      return { ...reply, status: 'streaming', blocks }
    })
    return
  }
  if (type === 'delta') {
    const index = Number(event.index)
    const kind = event.kind === 'thinking' ? 'thinking' : 'text'
    const text = String(event.text ?? '')
    applyToReply(sessionId, turnId, (reply) => {
      const blocks = ensureLiveBlock(reply, index, kind)
      const current = blocks[index] as unknown as Record<string, unknown>
      blocks[index] = {
        ...current,
        type: kind,
        [kind]: String(current[kind] ?? '') + text,
      } as unknown as ReplyBlock
      return { ...reply, status: 'streaming', blocks }
    })
    return
  }
  if (type === 'signature') {
    const index = Number(event.index)
    applyToReply(sessionId, turnId, (reply) => {
      const blocks = ensureLiveBlock(reply, index, 'thinking')
      const current = blocks[index] as unknown as Record<string, unknown>
      blocks[index] = {
        ...current,
        type: 'thinking',
        signature: event.signature,
      } as unknown as ReplyBlock
      return { ...reply, blocks }
    })
    return
  }
  if (type === 'citation') {
    const index = Number(event.index)
    applyToReply(sessionId, turnId, (reply) => {
      const blocks = ensureLiveBlock(reply, index, 'text')
      const current = blocks[index] as unknown as Record<string, unknown>
      const citations = Array.isArray(current.citations) ? current.citations : []
      blocks[index] = {
        ...current,
        type: 'text',
        citations: [...citations, event.citation],
      } as unknown as ReplyBlock
      return { ...reply, blocks }
    })
  }
}

const connectEvents = () => {
  if (events) {return}
  events = openEvents()
  events.onmessage = (message) => {
    try {
      applyEvent(JSON.parse(message.data) as Record<string, unknown>)
    } catch {
      // ignore malformed frames
    }
  }
}
