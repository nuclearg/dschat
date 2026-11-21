import { useEffect, useState } from 'react'
import { ApiError, getServerAccountInfo, type ApiAccountInfo } from '../../services/chatsvr.js'
import { useTranslate } from '../../i18n/index.js'
import styles from './Settings.module.css'

const errorMessage = (error: unknown) =>
  error instanceof ApiError ? error.message : error instanceof Error ? error.message : String(error)

/**
 * 官方 cloudsvr 账户页。
 *
 * 服务端下发 name/value/action，前端只按顺序渲染；不要在这里假设任何字段。
 * action 存在时渲染按钮，点击打开 action.url。
 */
export const OfficialAccountInfo = () => {
  const t = useTranslate()
  const [info, setInfo] = useState<ApiAccountInfo | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')

  const load = async () => {
    setLoading(true)
    setError('')
    try {
      setInfo(await getServerAccountInfo())
    } catch (e) {
      setError(t('syncAccountLoadFailed', { message: errorMessage(e) }))
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => {
    void load()
    // 设置页每次打开重新挂载，避免额外缓存失效逻辑。
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  const runAction = (url: string) => {
    if (!/^https?:\/\//i.test(url)) {
      return
    }
    window.open(url, '_blank', 'noopener,noreferrer')
  }

  return (
    <section className={styles.accountPanel} aria-label={t('syncAccountTitle')}>
      <div className={styles.accountHeader}>
        <div className={styles.accountTitle}>{t('syncAccountTitle')}</div>
        <button type="button" className={styles.refreshButton} onClick={() => void load()} disabled={loading}>
          {t('syncAccountRefresh')}
        </button>
      </div>

      {loading && !info ? <div className={styles.accountMuted}>{t('syncAccountLoading')}</div> : null}
      {error ? <div className={styles.error}>{error}</div> : null}

      {info ? (
        <div className={styles.accountGrid}>
          {info.items.map((item, index) => (
            <div className={styles.accountRow} key={`${item.name}-${index}`}>
              <span>{item.name}</span>
              <strong>{item.value}</strong>
              {item.action ? (
                <button
                  type="button"
                  className={styles.accountAction}
                  onClick={() => runAction(item.action!.url)}
                >
                  {item.action.text}
                </button>
              ) : null}
            </div>
          ))}
        </div>
      ) : null}
    </section>
  )
}
