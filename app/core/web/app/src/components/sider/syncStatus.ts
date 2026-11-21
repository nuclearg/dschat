import type { TranslationKey, TranslationParams } from '../../i18n/index.js'

type Translate = (key: TranslationKey, params?: TranslationParams) => string

/**
 * 同步图标和输入框上方共用同一套 blocked 文案。
 * 服务端只下发机器码（blockedDetail 形如 `StorageLimit: ...`），这里负责翻译成用户可读的原因。
 */
export const blockedStatusText = (
  t: Translate,
  reason: string | undefined,
  detail: string | undefined
) => {
  const reasonLabel = (() => {
    switch (reason) {
      case 'auth':
        return t('sessionCloudBlockedAuth')
      case 'limit':
        return t('sessionCloudBlockedLimit')
      case 'network':
        return t('sessionCloudBlockedNetwork')
      case 'server':
        return t('sessionCloudBlockedServer')
      case 'sync':
        return t('sessionCloudBlockedSync')
      default:
        return ''
    }
  })()

  const detailLabel = (() => {
    if (!detail) {
      return ''
    }
    const code = detail.split(':', 1)[0]
    switch (code) {
      case 'StorageLimit':
        return t('sessionCloudBlockedStorage')
      case 'MembershipExpired':
        return t('sessionCloudBlockedMembership')
      case 'DeviceLimit':
        return t('sessionCloudBlockedDeviceLimit')
      case 'SyncDisabled':
        return t('sessionCloudBlockedSyncDisabled')
      case 'RateLimited':
        return t('sessionCloudBlockedRateLimited')
      case 'Unauthorized':
      case 'TokenExpired':
        return t('sessionCloudBlockedUnauthorized')
      case 'HttpError':
      case 'Unknown':
        return t('sessionCloudBlockedServer')
      default:
        return detail
    }
  })()

  // 有具体错误码时只显示具体原因；否则显示分类，再附上原始 detail。
  // 不要把「同步已暂停」「认证失败」「登录失效」三层一起堆出来。
  const recognized = detailLabel !== '' && detailLabel !== detail
  const title = recognized ? detailLabel : (reasonLabel || t('sessionCloudBlocked'))
  const detailText = recognized ? '' : (detail ?? '')
  return {
    title,
    detail: detailText,
    ariaLabel: [title, detailText].filter(Boolean).join(' · '),
  }
}
