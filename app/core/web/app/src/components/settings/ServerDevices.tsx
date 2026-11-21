import { useEffect, useState } from 'react'
import { ApiError, listServerDevices, revokeServerDevice, type ApiDevice } from '../../services/chatsvr.js'
import { useTranslate } from '../../i18n/index.js'
import { formatDate } from '../../utils/format.js'
import { Button } from '../base/Button.js'
import { Modal } from '../base/Modal.js'
import styles from './Settings.module.css'

const errorMessage = (error: unknown) =>
  error instanceof ApiError ? error.message : error instanceof Error ? error.message : String(error)

/** 双端共用：官方 cloudsvr 和自建 sessionsvr 都实现 devices.list / devices.revoke。 */
export const ServerDevices = () => {
  const t = useTranslate()
  const [devices, setDevices] = useState<ApiDevice[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [revoking, setRevoking] = useState(false)
  const [confirm, setConfirm] = useState<ApiDevice | null>(null)

  const load = async () => {
    setLoading(true)
    setError('')
    try {
      const result = await listServerDevices()
      setDevices(result.devices ?? [])
    } catch (e) {
      setError(t('syncDevicesLoadFailed', { message: errorMessage(e) }))
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => {
    void load()
    // 只在组件挂载时拉一次；设置页重新打开会重新挂载。
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  const revoke = async () => {
    if (!confirm) {return}
    setRevoking(true)
    setError('')
    try {
      await revokeServerDevice(confirm.deviceId)
      setConfirm(null)
      await load()
    } catch (e) {
      setError(t('syncDeviceRemoveFailed', { message: errorMessage(e) }))
    } finally {
      setRevoking(false)
    }
  }

  const lastSeen = (device: ApiDevice) =>
    device.lastSeen > 0 ? (formatDate(device.lastSeen, '/') ?? '') : ''

  return (
    <section className={styles.accountPanel} aria-label={t('syncDevicesTitle')}>
      <div className={styles.accountHeader}>
        <div>
          <div className={styles.accountTitle}>{t('syncDevicesTitle')}</div>
        </div>
        <button type="button" className={styles.refreshButton} onClick={() => void load()} disabled={loading}>
          {t('syncDevicesRefresh')}
        </button>
      </div>

      {loading ? <div className={styles.accountMuted}>{t('syncDevicesLoading')}</div> : null}
      {!loading && devices.length === 0 ? (
        <div className={styles.accountMuted}>{t('syncDevicesEmpty')}</div>
      ) : null}

      {devices.map((device) => (
        <div className={styles.deviceRow} key={device.deviceId}>
          <div className={styles.deviceName}>
            <span className={styles.deviceLabel}>
              {device.name || device.deviceId}
              {device.platform ? <span className={styles.devicePlatform}>（{device.platform}）</span> : null}
            </span>
            {device.current ? <span className={styles.currentBadge}>{t('syncDeviceCurrent')}</span> : null}
          </div>
          <div className={styles.deviceRight}>
            <span className={styles.deviceLastSeen}>
              {lastSeen(device) ? `${t('syncDeviceLastUsed')}${lastSeen(device)}` : '—'}
            </span>
            {!device.current ? (
              <button
                type="button"
                className={styles.removeDeviceButton}
                onClick={() => setConfirm(device)}
                disabled={revoking}
              >
                {t('syncDeviceRemove')}
              </button>
            ) : null}
          </div>
        </div>
      ))}

      {error ? <div className={styles.error}>{error}</div> : null}

      {confirm ? (
        <Modal
          title={t('syncDeviceRemoveTitle')}
          className={styles.importConfirm}
          onClose={() => setConfirm(null)}
          footer={
            <>
              <Button variant="outlined" autoFocus onClick={() => setConfirm(null)} disabled={revoking}>
                {t('cancel')}
              </Button>
              <Button variant="dangerOutlined" onClick={() => void revoke()} disabled={revoking}>
                {revoking ? t('syncDeviceRemoving') : t('syncDeviceRemoveConfirm')}
              </Button>
            </>
          }
        >
          <div className={styles.accountConfirmText}>
            {t('syncDeviceRemoveDescription', { name: confirm.name || confirm.deviceId })}
          </div>
        </Modal>
      ) : null}
    </section>
  )
}
