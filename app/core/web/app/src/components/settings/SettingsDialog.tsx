import clsx from 'clsx'
import { useRef, useState, type ReactNode } from 'react'
import {
  IconDataOutline16,
  IconCloudOutline16,
  IconKeyOutline16,
  IconRightUpOutline14,
  IconSettingsOutline16,
  IconUploadOutline14,
} from '../base/icons.js'
import {
  clearHistory,
  importHistory,
} from '../../services/chat/backup.js'
import { useIsMobile } from '../../app/viewport.js'
import { disconnectServer, loginServer, useChatStore } from '../../services/chat/store.js'
import { regionCodes, getRegionName, provinces } from '../../services/location.js'
import {
  isChineseBrowser,
  setAutoStart,
  setLocale,
  setLocation,
  setSafetyCheck,
  setThemePreference,
  setTtsVoice,
  useSettings,
  type ThemePreference,
} from '../../services/settings.js'
import { useTranslate, type TranslationKey, type TranslationMessage } from '../../i18n/index.js'
import { Button } from '../base/Button.js'
import { Modal } from '../base/Modal.js'
import { Select } from '../base/Select.js'
import { showToast } from '../base/toastState.js'
import { ApiKeyConfirm, ApiKeyField, useApiKeyEditor } from './ApiKeyField.js'
import { OfficialSync } from './OfficialSync.js'
import { OfficialAccountInfo } from './OfficialAccountInfo.js'
import { ServerDevices } from './ServerDevices.js'
import { closeSettings, useSettingsDialog, type SettingsSection } from './settingsDialogState.js'
import styles from './Settings.module.css'

/**
 * 设置弹窗：分「通用设置」（主题、语言、朗读音色、位置）与「数据管理」（API Key、导入、导出、删除）。
 * 桌面端为居中弹窗、左侧导航；移动端为底部弹层、导航横排在内容上方
 */
export const SettingsDialog = () => {
  const open = useSettingsDialog((state) => state.open)
  return open ? <SettingsContent /> : null
}

/** 自建数据同步服务的部署教程，放在仓库的 docs/self-host.md */
export const DEPLOY_GUIDE_URL = 'https://github.com/nuclearg/dschat/blob/main/docs/self-host.md'

const sections: { value: SettingsSection; label: TranslationKey; icon: ReactNode }[] = [
  {
    value: 'general',
    label: 'settingDialogAsideGeneralOptionLabel',
    icon: <IconSettingsOutline16 />,
  },
  {
    value: 'account',
    label: 'settingDialogAsideAccountOptionLabel',
    icon: <IconKeyOutline16 />,
  },
  { value: 'data', label: 'settingDialogAsideDataOptionLabel', icon: <IconDataOutline16 /> },
  {
    value: 'sync',
    label: 'settingDialogAsideSyncOptionLabel',
    icon: <IconCloudOutline16 />,
  },
]

const SettingsContent = () => {
  const t = useTranslate()
  const isMobile = useIsMobile()
  // 打开时按调用方指定的分组选中，之后在弹窗内切换
  const [section, setSection] = useState(() => useSettingsDialog.getState().section)
  // 切换分组不丢失正在输入的 API Key 与导入状态
  const apiKeyEditor = useApiKeyEditor()
  const historyImport = useHistoryImport()
  const historyDelete = useHistoryDelete()
  const replacingHistory = useChatStore((state) => state.importingHistory)
  const historyBusy = historyImport.busy || historyDelete.busy || replacingHistory
  return (
    <>
      <Modal
        title={t('settings')}
        closable
        className={styles.dialog}
        mainClassName={styles.dialogMain}
        onClose={() => {
          if (!historyDelete.busy) {
            closeSettings()
          }
        }}
      >
        <div role="tablist" className={styles.navigation}>
          {sections.map((item) => (
            <button
              key={item.value}
              type="button"
              role="tab"
              aria-selected={section === item.value}
              className={clsx(styles.navItem, section === item.value && styles.navItemActive)}
              onClick={() => setSection(item.value)}
            >
              <span className={styles.navIcon}>{item.icon}</span>
              {t(item.label)}
            </button>
          ))}
        </div>
        <div role="tabpanel" className={styles.content}>
          {section === 'general' ? (
            <>
              <ThemeSetting />
              <LocaleSetting />
              <TtsVoiceSetting />
              <LocationSetting />
              <SafetyCheckSetting />
              <AutoStartSetting />
            </>
          ) : section === 'account' ? (
            <>
              <ApiKeyField editor={apiKeyEditor} />
              {isMobile ? <ApiKeyConfirm editor={apiKeyEditor} /> : null}
            </>
          ) : section === 'sync' ? (
            <SessionsvrSetting />
          ) : (
            <>
              <ImportHistory historyImport={historyImport} disabled={historyBusy} />
              <SettingCell
                label={t('deleteAllSessions')}
                description={t('deleteAllSessionsDescription')}
                action={
                  <Button
                    variant="dangerOutlined"
                    size="m"
                    className={styles.cellButton}
                    disabled={historyBusy}
                    onClick={historyDelete.open}
                  >
                    {t('delete')}
                  </Button>
                }
              />
            </>
          )}
        </div>
      </Modal>
      {historyImport.file ? <ImportConfirm historyImport={historyImport} /> : null}
      {historyDelete.opened ? <DeleteAllConfirm historyDelete={historyDelete} /> : null}
    </>
  )
}

/** 删除确认保留在设置之上；失败可重试，成功只关闭确认弹窗。 */
const DeleteAllConfirm = ({
  historyDelete,
}: {
  historyDelete: ReturnType<typeof useHistoryDelete>
}) => {
  const t = useTranslate()
  return (
    <Modal
      title={t('deleteAllSessionsModalTitle')}
      className={styles.deleteConfirm}
      onClose={historyDelete.cancel}
      footer={
        <div className={styles.deleteActions} aria-busy={historyDelete.busy}>
          <Button
            variant="outlined"
            autoFocus
            disabled={historyDelete.busy}
            onClick={historyDelete.cancel}
          >
            {t('cancel')}
          </Button>
          <Button
            variant="danger"
            disabled={historyDelete.busy}
            onClick={() => void historyDelete.confirm()}
          >
            {t(historyDelete.busy ? 'historyDeleting' : 'deleteAllSessions')}
          </Button>
        </div>
      }
    >
      {t('deleteAllSessionsModalDescription')}
    </Modal>
  )
}

const useHistoryDelete = () => {
  const [opened, setOpened] = useState(false)
  const [busy, setBusy] = useState(false)
  const deleting = useRef(false)

  const confirm = async () => {
    if (deleting.current) {
      return
    }
    deleting.current = true
    setBusy(true)
    try {
      const result = await clearHistory()
      setOpened(false)
      if (result.failed > 0) {
        showToast({
          key: 'historyDeletePartial',
          params: result,
          type: 'error',
        })
      } else if (result.deleted === 0) {
        showToast({ key: 'historyDeleteNone' })
      } else {
        showToast({ key: 'historyDeleted', params: result })
      }
    } catch {
      showToast({ key: 'historyDeleteFailed', type: 'error' })
    } finally {
      deleting.current = false
      setBusy(false)
    }
  }

  return {
    opened,
    busy,
    open: () => setOpened(true),
    cancel: () => {
      if (!deleting.current) {
        setOpened(false)
      }
    },
    confirm,
  }
}

const LocationSetting = () => {
  const t = useTranslate()
  const locale = useSettings((state) => state.locale)
  const location = useSettings((state) => state.location)
  const province =
    location?.region === 'CN'
      ? provinces.find((item) => item.code === location.province)
      : undefined

  return (
    <SettingCell
      label={t('settingsLocation')}
      status={null}
      className={styles.locationCell}
      action={
        <div className={styles.locationSelectors}>
          {isChineseBrowser() ? (
            <>
              <Select
                ariaLabel={t('settingsLocationProvince')}
                className={styles.provinceSelect}
                value={province?.code ?? ''}
                placeholder={t('settingsLocationProvince')}
                options={provinces.map((item) => ({ value: item.code, label: item.name }))}
                onChange={(value) => {
                  if (value !== province?.code) {
                    setLocation({ region: 'CN', province: value })
                  }
                }}
              />
              <Select
                ariaLabel={t('settingsLocationCity')}
                className={styles.citySelect}
                value={province ? (location?.city ?? '') : ''}
                disabled={!province?.cities.length}
                placeholder={t('settingsLocationCity')}
                options={
                  province?.cities.map((item) => ({ value: item.code, label: item.name })) ?? []
                }
                onChange={(value) => {
                  if (province) {
                    setLocation({ region: 'CN', province: province.code, city: value })
                  }
                }}
              />
            </>
          ) : (
            <Select
              ariaLabel={t('settingsLocationRegion')}
              className={styles.regionSelect}
              value={location?.region ?? ''}
              placeholder={t('settingsLocationRegion')}
              options={regionCodes
                .map((code) => ({ value: code, label: getRegionName(code, locale) }))
                .sort((a, b) => a.label.localeCompare(b.label, locale))}
              onChange={(value) => setLocation({ region: value })}
            />
          )}
        </div>
      }
    />
  )
}

const LocaleSetting = () => {
  const t = useTranslate()
  const locale = useSettings((state) => state.locale)
  return (
    <SettingCell
      label={t('settingsLocalePreference')}
      status={null}
      action={
        <Select
          ariaLabel={t('settingsLocalePreference')}
          value={locale}
          options={[
            { value: 'zh-CN', label: t('settingsLocaleZhCN'), lang: 'zh-CN' },
            { value: 'en-US', label: t('settingsLocaleEnUS'), lang: 'en' },
          ]}
          onChange={setLocale}
        />
      }
    />
  )
}

/** 朗读音色：stella（海星，女声）/ tide（暗潮，男声） */
const TtsVoiceSetting = () => {
  const t = useTranslate()
  const ttsVoice = useSettings((state) => state.ttsVoice)
  return (
    <SettingCell
      label={t('settingsTtsVoice')}
      status={null}
      action={
        <Select
          ariaLabel={t('settingsTtsVoice')}
          value={ttsVoice}
          options={[
            { value: 'stella', label: t('settingsTtsVoiceStella') },
            { value: 'tide', label: t('settingsTtsVoiceTide') },
          ]}
          onChange={setTtsVoice}
        />
      }
    />
  )
}

/**
 * 内容安全审核开关：关闭后请求体不再携带 safety_check，
 * 交给服务端按其默认策略处理；下一次请求起生效。
 */
const SafetyCheckSetting = () => {
  const t = useTranslate()
  const safetyCheck = useSettings((state) => state.safetyCheck)
  return (
    <SettingCell
      label={t('settingsSafetyCheck')}
      description={t('settingsSafetyCheckDescription')}
      action={
        <button
          type="button"
          role="switch"
          aria-checked={safetyCheck}
          aria-label={t('settingsSafetyCheck')}
          className={clsx(styles.switch, safetyCheck && styles.switchOn)}
          onClick={() => setSafetyCheck(!safetyCheck)}
        >
          <span className={styles.switchThumb} />
        </button>
      }
    />
  )
}

/** 登录时后台启动；仅 macOS/Windows 显示。 */
const AutoStartSetting = () => {
  const t = useTranslate()
  const autoStart = useSettings((state) => state.autoStart)
  const supported = useSettings((state) => state.autoStartSupported)
  if (!supported) {
    return null
  }
  return (
    <SettingCell
      label={t('settingsAutoStart')}
      description={t('settingsAutoStartDescription')}
      action={
        <button
          type="button"
          role="switch"
          aria-checked={autoStart}
          aria-label={t('settingsAutoStart')}
          className={clsx(styles.switch, autoStart && styles.switchOn)}
          onClick={() => setAutoStart(!autoStart)}
        >
          <span className={styles.switchThumb} />
        </button>
      }
    />
  )
}

/** 覆盖导入的二次确认，叠在设置弹窗上 */
const ImportConfirm = ({
  historyImport,
}: {
  historyImport: ReturnType<typeof useHistoryImport>
}) => {
  const t = useTranslate()
  return (
    <Modal
      title={t('historyImportConfirmTitle')}
      className={styles.importConfirm}
      onClose={historyImport.cancel}
      footer={
        <>
          <Button variant="outlined" size="m" autoFocus onClick={historyImport.cancel}>
            {t('cancel')}
          </Button>
          <Button variant="dangerOutlined" size="m" onClick={() => void historyImport.confirm()}>
            {t('historyImportConfirm')}
          </Button>
        </>
      }
    >
      <div className={styles.importConfirmText}>{t('historyImportConfirmDescription')}</div>
    </Modal>
  )
}

const themeOptions: { value: ThemePreference; label: TranslationKey }[] = [
  { value: 'light', label: 'avatarMenuSettingsDialogThemeSelectLightOption' },
  { value: 'dark', label: 'avatarMenuSettingsDialogThemeSelectDarkOption' },
  { value: 'system', label: 'avatarMenuSettingsDialogThemeSelectSystemOption' },
]

/**
 * 主题项：朴素的系统下拉框，和语言/音色等设置项保持同一行布局。
 */
const ThemeSetting = () => {
  const t = useTranslate()
  const themePreference = useSettings((state) => state.themePreference)
  const label = t('avatarMenuSettingsDialogThemeItemLabel')
  return (
    <SettingCell
      label={label}
      status={null}
      action={
        <Select
          ariaLabel={label}
          value={themePreference}
          options={themeOptions.map((option) => ({
            value: option.value,
            label: t(option.label),
          }))}
          onChange={setThemePreference}
        />
      }
    />
  )
}

/** `reason` 为失败原因的文案 key，展示时按当前语言翻译后填入 `message` 参数。 */
type Status = (TranslationMessage & { error?: boolean; reason?: TranslationKey }) | null

const SettingCell = ({
  label,
  description,
  status,
  action,
  className,
}: {
  label: string
  description?: string
  status?: Status
  action: ReactNode
  className?: string
}) => {
  const t = useTranslate()
  return (
    <div className={clsx(styles.cell, styles.row, className)}>
      <div className={styles.text}>
        <div className={styles.label}>{label}</div>
        {status || description ? (
          <div className={clsx(styles.description, status?.error && styles.error)}>
            {status
              ? t(status.key, status.reason ? { message: t(status.reason) } : status.params)
              : description}
          </div>
        ) : null}
      </div>
      {action}
    </div>
  )
}

/** 选文件后先提示覆盖；确认后才检查文件并执行导入，任何错误都提示导入失败。 */
const useHistoryImport = () => {
  const [file, setFile] = useState<File | null>(null)
  const [busy, setBusy] = useState(false)
  const [status, setStatus] = useState<Status>(null)
  const importing = useRef(false)

  const confirm = async () => {
    if (!file || importing.current) {
      return
    }
    importing.current = true
    setFile(null)
    setBusy(true)
    setStatus(null)
    try {
      const summary = await importHistory(file)
      if (summary.imported === 0) {
        setStatus({ key: 'historyImportFailed', error: true })
      } else if (summary.failed > 0 || summary.warnings > 0) {
        setStatus({
          key: 'historyImportPartial',
          params: { imported: summary.imported, failed: summary.failed, warnings: summary.warnings },
          error: summary.failed > 0,
        })
      } else {
        setStatus({
          key: summary.imported === 1 ? 'historyImportedOne' : 'historyImported',
          params: { count: summary.imported },
        })
      }
    } catch {
      setStatus({ key: 'historyImportFailed', error: true })
    } finally {
      importing.current = false
      setBusy(false)
    }
  }

  return { file, busy, status, select: setFile, cancel: () => setFile(null), confirm }
}

/** 数据同步服务：官方服务器敬请期待，自建服务器填地址 + 密码。 */
const SessionsvrSetting = () => {
  const t = useTranslate()
  const server = useChatStore((state) => state.server)
  const [url, setUrl] = useState(server.url)
  const [password, setPassword] = useState('')
  const [showPassword, setShowPassword] = useState(false)
  // 空密码不在表单里勾选，改成点连接时弹窗确认风险
  const [confirmEmpty, setConfirmEmpty] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  // 官方是产品主路径（地址内置、只要手机号密码）；自建是给愿意自己部署的人
  const [mode, setMode] = useState<'official' | 'selfHosted'>('official')

  const message = (e: unknown) => (e instanceof Error ? e.message : String(e))

  const connect = async () => {
    setBusy(true)
    setError('')
    try {
      await loginServer(url.trim(), password)
      setPassword('')
    } catch (e) {
      setError(message(e))
    } finally {
      setBusy(false)
    }
  }

  const submit = () => {
    if (!url.trim()) {
      setError(t('syncMissingUrl'))
      return
    }
    if (!password.trim()) {
      setConfirmEmpty(true)
      return
    }
    void connect()
  }

  const disconnectNow = async () => {
    setBusy(true)
    setError('')
    try {
      await disconnectServer()
    } catch (e) {
      setError(message(e))
    } finally {
      setBusy(false)
    }
  }

  return (
    <>
      <div className={styles.cell}>
        <div className={styles.label}>{t('syncTitle')}</div>
        <div className={styles.description}>{t('syncDescription')}</div>
        <div className={styles.radioGroup}>
          <label className={styles.radio}>
            <input
              type="radio"
              name="sessionsvr-mode"
              checked={mode === 'official'}
              onChange={() => setMode('official')}
              disabled={server.paired}
            />
            <span>{t('syncOfficial')}</span>
          </label>
          <div className={styles.radioRow}>
            <label className={styles.radio}>
              <input
                type="radio"
                name="sessionsvr-mode"
                checked={mode === 'selfHosted'}
                onChange={() => setMode('selfHosted')}
                disabled={server.paired}
              />
              <span>{t('syncSelfHosted')}</span>
            </label>
            {/* 放在 label 之外：点链接不应该顺带切换单选 */}
            <a
              className={styles.deployGuide}
              href={DEPLOY_GUIDE_URL}
              target="_blank"
              rel="noreferrer"
              onMouseDown={(event) => event.preventDefault()}
            >
              {t('syncDeployGuide')}
              <IconRightUpOutline14 />
            </a>
          </div>
        </div>
        {server.paired ? (
          <>
            <div className={styles.serverStatus}>
              <strong>{t('syncConnected')}</strong>
              <span>{server.url}</span>
              <Button variant="outlined" size="s" onClick={() => void disconnectNow()} disabled={busy}>
                {t('syncDisconnect')}
              </Button>
            </div>
            <ServerDevices />
            {server.kind === 'official' ? <OfficialAccountInfo /> : null}
          </>
        ) : mode === 'official' ? (
          <OfficialSync />
        ) : (
          <>
            <div className={styles.serverForm}>
              <div className={styles.serverField}>
                <label className={styles.serverFieldLabel} htmlFor="sessionsvr-url">
                  {t('syncAddressLabel')}
                </label>
                <div className={styles.serverInput}>
                  <input
                    id="sessionsvr-url"
                    value={url}
                    onChange={(event) => setUrl(event.target.value)}
                    aria-label={t('syncAddressLabel')}
                    spellCheck={false}
                    autoComplete="off"
                  />
                </div>
              </div>
              <div className={styles.serverField}>
                <label className={styles.serverFieldLabel} htmlFor="sessionsvr-password">
                  {t('syncPasswordLabel')}
                </label>
                <div className={styles.serverInput}>
                  <input
                    id="sessionsvr-password"
                    value={password}
                    onChange={(event) => setPassword(event.target.value)}
                    type={showPassword ? 'text' : 'password'}
                    aria-label={t('syncPasswordLabel')}
                    autoComplete="current-password"
                    onKeyDown={(event) => {
                      if (event.key === 'Enter' && !event.nativeEvent.isComposing) {
                        event.preventDefault()
                        submit()
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
                <Button variant="primary" size="m" onClick={submit} disabled={busy}>
                  {busy ? t('syncConnecting') : t('syncConnect')}
                </Button>
              </div>
            </div>
          </>
        )}
        {error ? <div className={styles.error}>{error}</div> : null}
      </div>
      {confirmEmpty ? (
        <Modal
          title={t('syncTitle')}
          className={styles.importConfirm}
          onClose={() => setConfirmEmpty(false)}
          footer={
            <div className={styles.deleteActions}>
              <Button variant="outlined" autoFocus onClick={() => setConfirmEmpty(false)}>
                {t('cancel')}
              </Button>
              <Button
                variant="primary"
                disabled={busy}
                onClick={() => {
                  setConfirmEmpty(false)
                  void connect()
                }}
              >
                {t('syncEmptyPasswordContinue')}
              </Button>
            </div>
          }
        >
          <div className={styles.importConfirmText}>{t('syncEmptyPasswordConfirm')}</div>
        </Modal>
      ) : null}
    </>
  )
}

/** 导入 = 覆盖：用选中文件替换本地全部会话与附件。 */
const ImportHistory = ({
  historyImport,
  disabled,
}: {
  historyImport: ReturnType<typeof useHistoryImport>
  disabled: boolean
}) => {
  const t = useTranslate()
  const inputRef = useRef<HTMLInputElement>(null)

  return (
    <SettingCell
      label={t('historyImport')}
      description={t('historyImportDescription')}
      status={historyImport.status}
      action={
        <>
          <Button
            variant="outlined"
            size="m"
            className={clsx(styles.cellButton, styles.uploadButton)}
            disabled={disabled}
            onClick={() => inputRef.current?.click()}
          >
            <IconUploadOutline14 />
            {t('historyImportAction')}
          </Button>
          <input
            ref={inputRef}
            type="file"
            accept=".zip,application/zip,application/x-zip-compressed"
            hidden
            onChange={(event) => {
              const file = event.target.files?.[0]
              event.target.value = ''
              if (file) {
                historyImport.select(file)
              }
            }}
          />
        </>
      }
    />
  )
}
