import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { Provider } from 'react-redux'
import { BrowserRouter } from 'react-router-dom'
import '@fontsource-variable/jetbrains-mono/wght.css'
import '@xterm/xterm/css/xterm.css'
import '@/index.css'
import App from './App'
import { store } from '@/store'
import { ServerStateBridge } from '@/store/ServerStateBridge'
import { ThemeProvider } from '@/theme'
import { StateSocketProvider } from '@/wire/react'

// The store outranks the socket provider: it has no dependency on the socket,
// while socket-driven middleware would need the store to already exist.
// ServerStateBridge sits inside StateSocketProvider and above ThemeProvider: it
// is what copies server topics into the store, and ThemeProvider is the topmost
// reader of one of them.
createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <Provider store={store}>
      <StateSocketProvider>
        <ServerStateBridge />
        <ThemeProvider>
          <BrowserRouter>
            <App />
          </BrowserRouter>
        </ThemeProvider>
      </StateSocketProvider>
    </Provider>
  </StrictMode>,
)
