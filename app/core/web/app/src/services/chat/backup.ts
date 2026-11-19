import { deleteAllSessions, type DeleteAllResult } from '../chatsvr.js'
import { refreshSessions, useChatStore } from './store.js'

export { BackupError } from './history/errors.js'

// 导入与全部删除串行处理，避免读取归档时数据被清空。
let backupQueue: Promise<unknown> = Promise.resolve()

const enqueueBackup = <T>(run: () => Promise<T>): Promise<T> => {
  const task = backupQueue.then(run)
  backupQueue = task.catch(() => {})
  return task
}

/** 选择文件后导入 DeepSeek ZIP；同一个文件导入两次会产生两份副本。 */
export const importHistory = (file: File, onProgress?: (done: number, total: number) => void) =>
  enqueueBackup(async () => {
    useChatStore.setState({ importingHistory: true })
    try {
      const { importDeepSeek } = await import('./history/importDeepSeek.js')
      return await importDeepSeek(file, onProgress)
    } finally {
      useChatStore.setState({ importingHistory: false })
    }
  })

/** 确认删除后调用：只删除当前设备拥有的本地会话；其他设备只读会话保留。 */
export const clearHistory = (): Promise<DeleteAllResult> =>
  enqueueBackup(async () => {
    const result = await deleteAllSessions()
    await refreshSessions()
    return result
  })
