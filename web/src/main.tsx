import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { BrowserRouter } from 'react-router-dom'
import '@fontsource-variable/jetbrains-mono/wght.css'
import '@xterm/xterm/css/xterm.css'
import './index.css'
import App from './App'
import { ThemeProvider } from './theme'
import { StateSocketProvider } from './wire/react'

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <StateSocketProvider>
      <ThemeProvider>
        <BrowserRouter>
          <App />
        </BrowserRouter>
      </ThemeProvider>
    </StateSocketProvider>
  </StrictMode>,
)
