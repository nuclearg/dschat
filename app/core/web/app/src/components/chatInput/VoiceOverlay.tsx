import clsx from 'clsx'
import { IconMicFill16 } from '../base/icons.js'
import { useTranslate } from '../../i18n/index.js'
import styles from './VoiceOverlay.module.css'

/** 按住录音时覆盖页面的提示层；松手发送，上滑取消。 */
export const VoiceOverlay = ({ cancel }: { cancel: boolean }) => {
  const t = useTranslate()
  return (
    <div className={clsx(styles.overlay, cancel && styles.cancel)} role="status" aria-live="assertive">
      <div className={styles.card}>
        <div className={styles.mic}>
          <IconMicFill16 />
        </div>
        <div className={styles.hint}>
          {cancel ? t('voiceOverlayCancel') : t('voiceOverlayRelease')}
        </div>
      </div>
    </div>
  )
}
