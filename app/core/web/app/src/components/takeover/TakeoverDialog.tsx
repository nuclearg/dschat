import { useEffect, useState } from 'react'
import { useTranslate } from '../../i18n/index.js'
import { importOfficialShare } from '../../services/chatsvr.js'
import { refreshSessions, selectSession } from '../../services/chat/store.js'
import { Button } from '../base/Button.js'
import { Modal } from '../base/Modal.js'
import { showToast } from '../base/toastState.js'
import { useTakeover } from './takeoverState.js'
import styles from './Takeover.module.css'

const extractShareUrl = (value: string) =>
  value.match(/https?:\/\/chat\.deepseek\.com\/share\/[a-z0-9]+/i)?.[0] ?? value.trim()

export const TakeoverDialog = () => {
  const t = useTranslate()
  const open = useTakeover((state) => state.open)
  const initialUrl = useTakeover((state) => state.initialUrl)
  const close = useTakeover((state) => state.closeTakeover)
  const [url, setUrl] = useState(initialUrl)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')

  useEffect(() => {
    if (open) {
      setUrl(extractShareUrl(initialUrl))
      setError('')
    }
  }, [open, initialUrl])

  if (!open) {
    return null
  }

  const importShare = async () => {
    const value = extractShareUrl(url)
    if (!value || busy) {
      return
    }
    setBusy(true)
    setError('')
    try {
      const result = await importOfficialShare(value)
      await refreshSessions()
      await selectSession(result.session.id)
      close()
      showToast({
        key: result.warning ? 'takeoverImportedWithWarning' : 'takeoverImported',
        params: result.warning ? { warning: result.warning } : undefined,
      })
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : String(cause))
    } finally {
      setBusy(false)
    }
  }

  return (
    <Modal
      title={t('takeoverTitle')}
      closable
      className={styles.dialog}
      onClose={() => {
        if (!busy) {
          close()
        }
      }}
      footer={
        <>
          <Button variant="outlined" disabled={busy} onClick={close}>
            {t('cancel')}
          </Button>
          <Button variant="primary" disabled={busy || !url.trim()} onClick={() => void importShare()}>
            {t('takeoverImport')}
          </Button>
        </>
      }
    >
      <div className={styles.body}>
        <p className={styles.hint}>{t('takeoverHint')}</p>
        <input
          className={styles.input}
          value={url}
          onChange={(event) => setUrl(event.target.value)}
          placeholder={t('takeoverPlaceholder')}
          autoFocus
          spellCheck={false}
          onKeyDown={(event) => {
            if (event.key === 'Enter') {
              event.preventDefault()
              void importShare()
            }
          }}
        />
        <div className={styles.actions}>
          <Button
            variant="outlined"
            size="s"
            disabled={busy}
            onClick={async () => {
              try {
                const text = await navigator.clipboard.readText()
                if (text.trim()) {
                  setUrl(text.trim())
                }
              } catch {
                // Clipboard permission is optional; paste into the field still works.
              }
            }}
          >
            {t('takeoverPaste')}
          </Button>
          {url.trim() ? (
            <button
              type="button"
              className={styles.clear}
              onClick={() => setUrl('')}
              disabled={busy}
            >
              {t('takeoverClear')}
            </button>
          ) : null}
        </div>
        {error ? <div className={styles.error}>{error}</div> : null}
        <p className={styles.privacy}>{t('takeoverPrivacy')}</p>
      </div>
    </Modal>
  )
}
