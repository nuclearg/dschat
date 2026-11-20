import { create } from 'zustand'
import { saveSettings } from './chatsvr.js'
import type { TtsVoice } from './api/speech.js'
import { normalizeLocation, type UserLocation } from './location.js'

export type Locale = 'zh-CN' | 'en-US'

/** 主题偏好；默认跟随系统 */
export type ThemePreference = 'light' | 'dark' | 'system'

/**
 * 功能设置（API Key / 深度思考 / 智能搜索）由 chatsvr 管理；
 * 界面偏好（语言、主题、位置、音色、侧边栏）仍留在浏览器。
 */
type Settings = {
  /** 'managed-by-chatsvr' 表示 chatsvr 已配置 key；null 表示未配置 */
  apiKey: string | null
  locale: Locale
  themePreference: ThemePreference
  location: UserLocation | null
  ttsVoice: TtsVoice
  thinking: boolean
  search: boolean
  /** 请求是否携带 safety_check；由 chatsvr 管理，默认开启 */
  safetyCheck: boolean
  /** 是否注册为登录启动；仅桌面端支持，由 chatsvr 管理 */
  autoStart: boolean
  autoStartSupported: boolean
  siderCollapsed: boolean
  /** 展开的侧边栏宽度（px） */
  siderWidth: number
}

const KEYS = {
  locale: 'chat-demo:locale',
  location: 'chat-demo:location',
  themePreference: 'chat-demo:theme-preference',
  ttsVoice: 'chat-demo:tts-voice',
  siderCollapsed: 'chat-demo:sider-collapsed',
  siderWidth: 'chat-demo:sider-width',
} as const

const read = (key: string) => {
  try {
    return localStorage.getItem(key)
  } catch {
    return null
  }
}

const write = (key: string, value: string | null) => {
  try {
    if (value === null) {
      localStorage.removeItem(key)
    } else {
      localStorage.setItem(key, value)
    }
  } catch {
    // 存储不可用时只在内存中生效
  }
}

export const isChineseBrowser = (): boolean =>
  (typeof navigator === 'undefined' ? 'zh' : navigator.language).toLowerCase().startsWith('zh')

const initialLocale = (): Locale => {
  const saved = read(KEYS.locale)
  if (saved === 'zh-CN' || saved === 'en-US') {
    return saved
  }
  return isChineseBrowser() ? 'zh-CN' : 'en-US'
}

const initialLocation = (): UserLocation | null => {
  try {
    const location = normalizeLocation(JSON.parse(read(KEYS.location) ?? 'null'))
    if (!location) {
      return null
    }
    return isChineseBrowser()
      ? location.region === 'CN' && location.province
        ? location
        : null
      : { region: location.region }
  } catch {
    return null
  }
}

const initialThemePreference = (): ThemePreference => {
  const saved = read(KEYS.themePreference)
  return saved === 'light' || saved === 'dark' ? saved : 'system'
}

const initialTtsVoice = (): TtsVoice => (read(KEYS.ttsVoice) === 'tide' ? 'tide' : 'stella')

const initialSiderWidth = (): number => {
  const width = Number(read(KEYS.siderWidth))
  return Number.isFinite(width) && width >= 200 && width <= 480 ? width : 261
}

export const useSettings = create<Settings>(() => ({
  apiKey: null,
  locale: initialLocale(),
  location: initialLocation(),
  themePreference: initialThemePreference(),
  ttsVoice: initialTtsVoice(),
  thinking: false,
  search: false,
  safetyCheck: true,
  autoStart: false,
  autoStartSupported: false,
  siderCollapsed: read(KEYS.siderCollapsed) === 'true',
  siderWidth: initialSiderWidth(),
}))

export const setLocale = (locale: Locale) => {
  write(KEYS.locale, locale)
  useSettings.setState({ locale })
}

export const setLocation = (value: UserLocation | null) => {
  const location = normalizeLocation(value)
  write(KEYS.location, location ? JSON.stringify(location) : null)
  useSettings.setState({ location })
}

export const setThemePreference = (themePreference: ThemePreference) => {
  write(KEYS.themePreference, themePreference)
  useSettings.setState({ themePreference })
}

export const setTtsVoice = (ttsVoice: TtsVoice) => {
  write(KEYS.ttsVoice, ttsVoice)
  useSettings.setState({ ttsVoice })
}

/** 保存新 key 到 chatsvr；浏览器只保留“已配置”标记，不保存明文。 */
export const setApiKey = (apiKey: string | null) => {
  useSettings.setState({ apiKey: apiKey ? 'managed-by-chatsvr' : null })
  void saveSettings(apiKey ? { apiKey } : { clearKey: true }).catch(() => {})
}

export const setThinking = (thinking: boolean) => {
  useSettings.setState({ thinking })
  void saveSettings({ thinking }).catch(() => {})
}

export const setSearch = (search: boolean) => {
  useSettings.setState({ search })
  void saveSettings({ search }).catch(() => {})
}

/** 保存到 chatsvr：下一次请求起生效，进行中的回答不受影响。 */
export const setSafetyCheck = (safetyCheck: boolean) => {
  useSettings.setState({ safetyCheck })
  void saveSettings({ safetyCheck }).catch(() => {})
}

/** 开机启动由操作系统管理；保存失败时恢复开关，避免界面和系统状态不一致。 */
export const setAutoStart = (autoStart: boolean) => {
  const previous = useSettings.getState().autoStart
  useSettings.setState({ autoStart })
  void saveSettings({ autoStart })
    .then((view) => {
      useSettings.setState({
        autoStart: view.autoStart,
        autoStartSupported: view.autoStartSupported,
      })
    })
    .catch(() => useSettings.setState({ autoStart: previous }))
}

export const setSiderCollapsed = (siderCollapsed: boolean) => {
  write(KEYS.siderCollapsed, String(siderCollapsed))
  useSettings.setState({ siderCollapsed })
}

export const setSiderWidth = (width: number) => {
  const clamped = Math.min(480, Math.max(200, Math.round(width)))
  write(KEYS.siderWidth, String(clamped))
  useSettings.setState({ siderWidth: clamped })
}
