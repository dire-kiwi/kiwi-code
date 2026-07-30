import { render, type RenderOptions } from '@testing-library/react'
import type { ReactElement, ReactNode } from 'react'
import { Provider } from 'react-redux'
import { createAppStore, type AppStore, type CreateAppStoreOptions } from './index'

// Isolated and non-persisting by default, so a test that only needs a Provider
// pays nothing and cannot touch real localStorage.
export function createTestStore(options: CreateAppStoreOptions = {}): AppStore {
  return createAppStore({ storage: null, persist: false, ...options })
}

export type RenderWithStoreOptions = Omit<RenderOptions, 'wrapper'> & {
  store?: AppStore
}

export function renderWithStore(
  ui: ReactElement,
  { store = createTestStore(), ...options }: RenderWithStoreOptions = {},
) {
  function Wrapper({ children }: { children: ReactNode }) {
    return <Provider store={store}>{children}</Provider>
  }
  return { store, ...render(ui, { wrapper: Wrapper, ...options }) }
}
