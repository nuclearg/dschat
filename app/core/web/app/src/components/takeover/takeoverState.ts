import { create } from 'zustand'

type TakeoverState = {
  open: boolean
  initialUrl: string
  openTakeover: (url?: string) => void
  closeTakeover: () => void
}

export const useTakeover = create<TakeoverState>((set) => ({
  open: false,
  initialUrl: '',
  openTakeover: (initialUrl = '') => set({ open: true, initialUrl }),
  closeTakeover: () => set({ open: false, initialUrl: '' }),
}))
