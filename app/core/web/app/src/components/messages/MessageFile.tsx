import clsx from 'clsx'
import { useEffect, useState } from 'react'
import { useTranslate } from '../../i18n/index.js'
import { useUploads } from '../../services/attachments.js'
import { loadFile } from '../../services/chat/files.js'
import type { FileAttachment } from '../../services/chat/types.js'
import { IconPhotoErrorFill20 } from '../base/icons.js'
import { FileCard } from '../files/FileCard.js'
import styles from './MessageFile.module.css'

/** 原件从 chatsvr 读出并生成本地地址；undefined 表示加载中，null 表示原件缺失。 */
const useFileUrl = (id: string) => {
  const [fileUrl, setFileUrl] = useState<{ id: string; url: string | null } | null>(null)
  useEffect(() => {
    let objectUrl: string | null = null
    let cancelled = false
    loadFile(id).then((blob) => {
      if (!cancelled) {
        objectUrl = blob ? URL.createObjectURL(blob) : null
        setFileUrl({ id, url: objectUrl })
      }
    })
    return () => {
      cancelled = true
      if (objectUrl) {
        URL.revokeObjectURL(objectUrl)
      }
    }
  }, [id])
  return fileUrl?.id === id ? fileUrl.url : undefined
}

/** 历史或失效附件的补传状态：只在还没有 serverId 时显示 */
const useUploadState = (file: FileAttachment) =>
  useUploads((state) => {
    if (file.serverId) {
      return null
    }
    const task = state.tasks[file.id]
    return task?.status === 'uploading' || task?.status === 'error' ? task : null
  })

/**
 * 消息里的附件：
 * - 图片：纯图片显示 120×120 缩略图，与文件混排时为 64×64；上传中显示 loading 蒙层，
 *   上传失败显示品牌色描边 + 中心重试图标（可重试）或 photo-error 图标（不可重试）。
 * - 文件：显示文件卡片。
 */
export const MessageFile = ({
  file,
}: {
  file: FileAttachment
}) => {
  const t = useTranslate()
  const url = useFileUrl(file.id)
  const [failedUrl, setFailedUrl] = useState<string | null>(null)
  const upload = useUploadState(file)
  const uploading = upload?.status === 'uploading'
  const failed = upload?.status === 'error'
  const errorText = failed ? t(upload.error.key, upload.error.params) : undefined

  if (file.isImage && url !== null) {
    const preview = url ? (
      failedUrl === url ? (
        <span className={styles.failedIcon} role="img" aria-label={file.name}>
          <IconPhotoErrorFill20 />
        </span>
      ) : (
        <img src={url} alt={file.name} onError={() => setFailedUrl(url)} />
      )
    ) : null

    // 上传中：原件已由 chatsvr 保存，先垫在蒙层下显示，避免只有一块灰色占位
    if (uploading) {
      return (
        <div className={clsx(styles.image, styles.imageThumb)} title={file.name}>
          {preview}
          <div className={styles.overlay}>
            <span className={styles.spinner} />
          </div>
        </div>
      )
    }

    // 「上传失败」只显示状态，其余原因写在图片底部的渐变上
    const reason =
      errorText && errorText !== t('uploadFileStatusFailed') ? (
        <span className={styles.reason}>{errorText}</span>
      ) : null

    // 上传失败不可重试：保留缩略图，底部渐变显示原因
    if (failed) {
      return (
        <div
          className={clsx(styles.image, styles.imageThumb)}
          title={errorText}
          aria-label={errorText}
        >
          {preview ?? (
            <span className={styles.failedIcon}>
              <IconPhotoErrorFill20 />
            </span>
          )}
          {reason}
        </div>
      )
    }

    // 正常态：链接到原件
    return url ? (
      <a
        className={clsx(styles.image, styles.imageThumb)}
        href={url}
        target="_blank"
        rel="noreferrer"
        title={file.name}
      >
        {preview}
      </a>
    ) : (
      // 原件还没加载出来（极短暂）
      <div className={styles.image} />
    )
  }

  return (
    <FileCard
      name={file.name}
      size={file.size}
      status={uploading ? 'uploading' : failed ? 'error' : 'done'}
      error={errorText}
      onOpen={url && !upload ? () => window.open(url, '_blank', 'noreferrer') : undefined}
    />
  )
}
