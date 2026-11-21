import { useEffect, useState } from 'react'
import { ApiError, getServerAccountInfo, type ApiAccountInfo } from '../../services/chatsvr.js'
import { useTranslate } from '../../i18n/index.js'
import { formatDate } from '../../utils/format.js'
import { formatSize } from '../../services/attachments.js'
import styles from './Settings.module.css'

const errorMessage = (error: unknown) =>
  error instanceof ApiError ? error.message : error instanceof Error ? error.message : String(error)

/** 官方 cloudsvr 专属：账户、会员、用量。自建 sessionsvr 不渲染此组件。 */
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

  const used = info?.usage.storageUsedBytes ?? 0
  const limit = info?.usage.storageLimitBytes ?? 0
  const percent = limit > 0 ? Math.min(100, Math.max(0, (used / limit) * 100)) : 0
  const expiry =
    info && info.membership.planId !== 'free' && info.membership.periodEnd > 0
      ? formatDate(info.membership.periodEnd, '/')
      : ''

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
          <div className={styles.accountRow}>
            <span>{t('syncAccountPhone')}</span>
            <strong>{info.account.phone || '—'}</strong>
          </div>
          <div className={styles.accountRow}>
            <span>{t('syncAccountPlan')}</span>
            <strong>{info.membership.planName || info.membership.planId}</strong>
          </div>
          <div className={styles.accountRow}>
            <span>{t('syncAccountStorage')}</span>
            <strong>
              {t('syncAccountStorageUsage', { used: formatSize(used), limit: formatSize(limit) })}
            </strong>
          </div>
          <div className={styles.storageBar} role="progressbar" aria-valuenow={Math.round(percent)} aria-valuemin={0} aria-valuemax={100}>
            <div className={styles.storageFill} style={{ width: `${percent}%` }} />
          </div>
          {expiry ? (
            <div className={styles.accountRow}>
              <span>{t('syncAccountExpires')}</span>
              <strong>{expiry}</strong>
            </div>
          ) : null}
          <div className={styles.accountRow}>
            <span>{t('syncAccountOpenTurns')}</span>
            <strong>
              {info.usage.openTurns} / {info.usage.maxOpenTurns}
            </strong>
          </div>
        </div>
      ) : null}
    </section>
  )
}
