import { useState } from 'react'
import { Button } from '../base/Button.js'
import { useTranslate } from '../../i18n/index.js'
import { officialLoginServer, useChatStore } from '../../services/chat/store.js'
import styles from './Settings.module.css'

/**
 * 官方同步服务：地址内置在服务端，客户端这里只有**一个登录入口**（手机号 + 密码）。
 *
 * 注册与找回密码都在 cloudsvr 的网页上（`/register`、`/reset`）：它们是低频、带人机验证
 * 与短信的多步流程，做成网页后客户端不必维护表单和滑块 SDK，也不会出现"应用里注册完
 * 还要回登录页"的割裂。这里只负责把链接打开给用户。
 */

/** 与服务端一致：中国大陆 11 位手机号，提交时补 +86。 */
const CN_MOBILE = /^1[3-9]\d{9}$/

export const OfficialSync = () => {
  const t = useTranslate()
  const officialBaseUrl = useChatStore((state) => state.officialBaseUrl)
  const [phone, setPhone] = useState('')
  const [password, setPassword] = useState('')
  const [showPassword, setShowPassword] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')

  const message = (e: unknown) => (e instanceof Error ? e.message : String(e))
  const phoneOk = CN_MOBILE.test(phone.trim())

  const login = async () => {
    if (!phoneOk) {
      setError(t('syncPhoneInvalid'))
      return
    }
    if (!password) {
      setError(t('syncPasswordRequired'))
      return
    }
    setBusy(true)
    setError('')
    try {
      await officialLoginServer(phone.trim(), password)
      setPassword('')
    } catch (e) {
      setError(message(e))
    } finally {
      setBusy(false)
    }
  }

  const serverPageButton = (path: string, label: string) => {
    const base = officialBaseUrl.replace(/\/+$/, '')
    return (
      <a
        className={styles.serverLinkButton}
        href={`${base}${path}`}
        target="_blank"
        rel="noreferrer"
      >
        {label}
      </a>
    )
  }

  return (
    <div className={styles.serverForm}>
      <div className={styles.serverField}>
        <label className={styles.serverFieldLabel} htmlFor="official-phone">
          {t('syncPhoneLabel')}
        </label>
        <div className={styles.serverInput}>
          <input
            id="official-phone"
            value={phone}
            onChange={(event) => setPhone(event.target.value.replace(/[^\d]/g, '').slice(0, 11))}
            inputMode="numeric"
            aria-label={t('syncPhoneLabel')}
            placeholder={t('syncPhonePlaceholder')}
            autoComplete="tel"
            spellCheck={false}
          />
        </div>
      </div>

      <div className={styles.serverField}>
        <label className={styles.serverFieldLabel} htmlFor="official-password">
          {t('syncPasswordLabel')}
        </label>
        <div className={styles.serverInput}>
          <input
            id="official-password"
            value={password}
            onChange={(event) => setPassword(event.target.value)}
            type={showPassword ? 'text' : 'password'}
            aria-label={t('syncPasswordLabel')}
            autoComplete="current-password"
            onKeyDown={(event) => {
              if (event.key === 'Enter' && !event.nativeEvent.isComposing) {
                event.preventDefault()
                void login()
              }
            }}
          />
          <button
            type="button"
            className={styles.serverPasswordToggle}
            aria-label={t(showPassword ? 'syncPasswordHide' : 'syncPasswordShow')}
            onMouseDown={(event) => event.preventDefault()}
            onClick={() => setShowPassword((visible) => !visible)}
          >
            {t(showPassword ? 'syncPasswordHide' : 'syncPasswordShow')}
          </button>
        </div>
      </div>

      <div className={styles.serverActions}>
        <Button variant="primary" size="m" disabled={busy} onClick={() => void login()}>
          {busy ? t('syncLoggingIn') : t('syncLogin')}
        </Button>
        {/* 地址拿不到时（理论上不会）就不显示，避免给出一个死链接 */}
        {officialBaseUrl
          ? (
              <>
                {serverPageButton('/register', t('syncRegister'))}
                {serverPageButton('/reset', t('syncForgotPassword'))}
              </>
            )
          : null}
      </div>

      {error ? <div className={styles.error}>{error}</div> : null}
    </div>
  )
}
