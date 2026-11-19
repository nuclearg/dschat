import { useTranslate } from '../../i18n/index.js'
import type { SessionSummary } from '../../services/chat/types.js'
import styles from './SessionCloud.module.css'

type Props = {
  session: SessionSummary
  paired: boolean
  localDeviceId: string
}

const CloudPath = 'M4.6 12.2h6.7a2.9 2.9 0 0 0 .2-5.8 4 4 0 0 0-7.6-.3 2.9 2.9 0 0 0 .7 6.1Z'

/** 云本体只表示 ownership；角标表示传输状态。 */
export const SessionCloud = ({ session, paired, localDeviceId }: Props) => {
  const t = useTranslate()
  if (!paired) {
    return null
  }
  const owned = Boolean(localDeviceId) && session.ownerDeviceId === localDeviceId
  const state =
    session.hasFragment || session.transferState === 'fragment'
      ? 'fragment'
      : session.transferState === 'blocked'
        ? 'blocked'
        : session.transferState === 'uploading'
          ? 'uploading'
          : session.transferState === 'pending'
            ? 'pending'
            : session.transferState === 'local' || session.syncState === 'local'
              ? 'local'
              : 'synced'

  const label =
    state === 'fragment'
      ? t(owned ? 'sessionCloudFragment' : 'sessionCloudFragmentRemote')
      : state === 'blocked'
        ? t('sessionCloudBlocked')
        : state === 'uploading'
          ? t(owned ? 'sessionCloudUploading' : 'sessionCloudDownloading')
          : state === 'pending'
            ? t('sessionCloudPending')
            : state === 'local'
              ? t('sessionCloudLocal')
              : t(owned ? 'sessionCloudOwnedSynced' : 'sessionCloudRemote')

  return (
    <span className={styles.status} role="img" aria-label={label} title={label}>
      <svg width={16} height={16} viewBox="0 0 16 16" fill="none" aria-hidden="true">
        <path
          d={CloudPath}
          fill={owned ? 'currentColor' : 'none'}
          stroke="currentColor"
          strokeWidth={owned ? 0 : 1.3}
          strokeLinejoin="round"
        />
      </svg>
      {state === 'fragment' ? (
        <span className={styles.fragment} aria-hidden="true">
          <svg width={8} height={8} viewBox="0 0 8 8" fill="none">
            <path d="M1.5 1.5 6.5 6.5M6.5 1.5 1.5 6.5" stroke="currentColor" strokeWidth="1.3" strokeLinecap="round" />
          </svg>
        </span>
      ) : state === 'blocked' ? (
        <span className={styles.blocked} aria-hidden="true">
          <svg width={8} height={8} viewBox="0 0 8 8" fill="none">
            <circle cx="4" cy="4" r="3.1" stroke="currentColor" strokeWidth="1" />
            <path d="M4 2.1v2.4" stroke="currentColor" strokeWidth="1" strokeLinecap="round" />
            <circle cx="4" cy="5.9" r="0.6" fill="currentColor" />
          </svg>
        </span>
      ) : state === 'uploading' ? (
        <span className={styles.spinner} aria-hidden="true" />
      ) : state === 'local' || state === 'pending' ? (
        <span className={styles.clock} aria-hidden="true">
          <svg width={8} height={8} viewBox="0 0 8 8" fill="none">
            <circle cx="4" cy="4" r="3.1" stroke="currentColor" strokeWidth="1" />
            <path d="M4 2.2v2.1l1.2.7" stroke="currentColor" strokeWidth="1" strokeLinecap="round" />
          </svg>
        </span>
      ) : null}
    </span>
  )
}
