import { useRef, type KeyboardEvent as ReactKeyboardEvent, type PointerEvent as ReactPointerEvent, type RefObject } from 'react'
import styles from './SiderSplitter.module.css'

const MIN_WIDTH = 200
const MAX_WIDTH = 480
const DEFAULT_WIDTH = 261

/** 侧边栏与主体之间的拖拽分隔条；拖动时直接改 CSS 变量，松手才写回设置。 */
export const SiderSplitter = ({
  width,
  frameRef,
  onCommit,
}: {
  width: number
  frameRef: RefObject<HTMLDivElement>
  onCommit: (width: number) => void
}) => {
  const dragging = useRef(false)
  const current = useRef(width)

  const apply = (next: number) => {
    const clamped = Math.min(MAX_WIDTH, Math.max(MIN_WIDTH, Math.round(next)))
    current.current = clamped
    frameRef.current?.style.setProperty('--sider-width', `${clamped}px`)
    return clamped
  }

  const onPointerDown = (event: ReactPointerEvent<HTMLDivElement>) => {
    if (event.button !== 0) {return}
    event.preventDefault()
    const el = event.currentTarget
    const startX = event.clientX
    const startWidth = current.current
    dragging.current = true
    el.dataset.dragging = 'true'
    el.setPointerCapture?.(event.pointerId)
    document.body.style.userSelect = 'none'
    document.body.style.cursor = 'col-resize'

    const move = (moveEvent: PointerEvent) => {
      if (!dragging.current) {return}
      apply(startWidth + moveEvent.clientX - startX)
    }
    const finish = (finishEvent: PointerEvent) => {
      if (!dragging.current) {return}
      dragging.current = false
      delete el.dataset.dragging
      el.releasePointerCapture?.(finishEvent.pointerId)
      document.body.style.userSelect = ''
      document.body.style.cursor = ''
      window.removeEventListener('pointermove', move)
      window.removeEventListener('pointerup', finish)
      window.removeEventListener('pointercancel', finish)
      onCommit(current.current)
    }
    window.addEventListener('pointermove', move)
    window.addEventListener('pointerup', finish)
    window.addEventListener('pointercancel', finish)
  }

  const onKeyDown = (event: ReactKeyboardEvent<HTMLDivElement>) => {
    if (event.key !== 'ArrowLeft' && event.key !== 'ArrowRight') {return}
    event.preventDefault()
    const next = apply(current.current + (event.key === 'ArrowRight' ? 16 : -16))
    onCommit(next)
  }

  return (
    <div
      className={styles.splitter}
      role="separator"
      aria-orientation="vertical"
      aria-label="调整侧边栏宽度"
      aria-valuemin={MIN_WIDTH}
      aria-valuemax={MAX_WIDTH}
      aria-valuenow={Math.round(width)}
      tabIndex={0}
      onPointerDown={onPointerDown}
      onKeyDown={onKeyDown}
      onDoubleClick={() => {
        apply(DEFAULT_WIDTH)
        onCommit(DEFAULT_WIDTH)
      }}
    />
  )
}
