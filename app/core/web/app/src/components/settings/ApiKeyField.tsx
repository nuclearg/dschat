import { useState } from 'react'
import { IconEditOutline16, IconRightUpOutline14 } from '../base/icons.js'
import { useSettings } from '../../services/settings.js'
import { useIsMobile } from '../../app/viewport.js'
import { IconButton } from '../base/IconButton.js'
import { useApiKeyForm } from './useApiKeyForm.js'
import { useTranslate } from '../../i18n/index.js'
import styles from './Settings.module.css'

/** 开放平台的 API Keys 页面：引导里「去获取」直接跳到这里 */
export const API_KEY_PLATFORM_URL = 'https://platform.deepseek.com/api_keys'

/** API Key 的编辑状态：输入框与移动端底部的「确定」按钮共用 */
export const useApiKeyEditor = () => {
  const apiKey = useSettings((state) => state.apiKey)
  const [editing, setEditing] = useState(false)
  const form = useApiKeyForm(() => setEditing(false))
  return { apiKey, editing, setEditing, form, showInput: !apiKey || editing }
}

type ApiKeyEditor = ReturnType<typeof useApiKeyEditor>

/** 「去获取 API Key」链接：统一在新窗口打开开放平台 */
const GetApiKeyLink = ({ label }: { label: string }) => (
  <a
    className={styles.apiKeyGuideLink}
    href={API_KEY_PLATFORM_URL}
    target="_blank"
    rel="noreferrer"
    // 抢走输入框焦点会触发 onBlur 把输入区收起来，链接的 click 也因此丢失。
    onMouseDown={(event) => event.preventDefault()}
  >
    {label}
    <IconRightUpOutline14 />
  </a>
)

/** 首次配置时的分步引导：没接触过 API 也能照着走完 */
const ApiKeyGuide = () => {
  const t = useTranslate()
  return (
    <ol className={styles.apiKeyGuide}>
      <li>
        {t('apiKeyGuideStep1')}
        <GetApiKeyLink label={t('apiKeyGuideOpenPlatform')} />
      </li>
      <li>{t('apiKeyGuideStep2')}</li>
      <li>{t('apiKeyGuideStep3')}</li>
    </ol>
  )
}

/** API Key：保存后只显示掩码，点编辑按钮才能重新输入 */
export const ApiKeyField = ({ editor }: { editor: ApiKeyEditor }) => {
  const t = useTranslate()
  const isMobile = useIsMobile()
  const { apiKey, editing, setEditing, form, showInput } = editor

  return (
    <div className={styles.cell}>
      <div className={styles.apiKey}>
        <div className={styles.label}>{t('apiKeyStart')}</div>
        {showInput ? <ApiKeyGuide /> : null}
        {apiKey && !editing ? (
          <div className={styles.apiKeySaved}>
            <span className={styles.apiKeyMask}>{'*'.repeat(apiKey.length)}</span>
            <IconButton
              tone="primary"
              icon={<IconEditOutline16 />}
              tooltip={t('apiKeyEdit')}
              onClick={() => setEditing(true)}
            />
          </div>
        ) : (
          <div className={styles.apiKeyInput}>
            <input
              type="password"
              placeholder={t('apiKeyPlaceholder')}
              autoComplete="off"
              spellCheck={false}
              // 移动端打开设置时不弹键盘，点编辑后才聚焦
              autoFocus={!isMobile || editing}
              value={form.value}
              onChange={(event) => form.setValue(event.target.value)}
              onKeyDown={(event) => {
                if (event.key === 'Enter' && !event.nativeEvent.isComposing) {
                  void form.submit()
                }
              }}
              onBlur={() => {
                // 修改时没输入就离开，回到已保存的状态
                if (apiKey && !form.value.trim()) {
                  setEditing(false)
                }
              }}
            />
            {!isMobile && form.value.trim() ? (
              <>
                <span className={styles.divider} />
                <button
                  type="button"
                  className={styles.confirm}
                  disabled={form.verifying}
                  // 保持输入框焦点
                  onMouseDown={(event) => event.preventDefault()}
                  onClick={() => void form.submit()}
                >
                  {form.verifying ? t('apiKeyVerifying') : t('confirm')}
                </button>
              </>
            ) : null}
          </div>
        )}
        {showInput && form.error ? (
          <div className={styles.error}>
            {form.error}
            {/* 校验失败时补一个「重新获取」入口，免得用户卡在错误提示上 */}
            <GetApiKeyLink label={t('apiKeyGuideGet')} />
          </div>
        ) : null}
        {showInput ? <div className={styles.apiKeyHint}>{t('apiKeyHint')}</div> : null}
      </div>
    </div>
  )
}

/** 移动端：输入 API Key 时在选项下方显示整行「确定」 */
export const ApiKeyConfirm = ({ editor: { showInput, form } }: { editor: ApiKeyEditor }) => {
  const t = useTranslate()
  return showInput && form.value.trim() ? (
    <button
      type="button"
      className={styles.mobileConfirm}
      disabled={form.verifying}
      onMouseDown={(event) => event.preventDefault()}
      onClick={() => void form.submit()}
    >
      {form.verifying ? t('apiKeyVerifying') : t('confirm')}
    </button>
  ) : null
}
