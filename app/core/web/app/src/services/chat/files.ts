/** 附件原件由 chatsvr 保存，浏览器只按 id 取回用于预览。 */
export const loadFile = async (id: string): Promise<Blob | undefined> => {
  const response = await fetch(`/api/attachments/${encodeURIComponent(id)}`, {
    credentials: 'same-origin',
  })
  return response.ok ? response.blob() : undefined
}
