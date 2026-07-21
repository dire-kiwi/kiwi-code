import { createHash } from 'node:crypto'

export const defaultVitePort = 5173
export const defaultGoPort = 8080
export const reservedProductionPort = 4000
export const productionTmuxSocket = 'kiwi-code'

export function defaultDevelopmentTmuxSocket(rootDirectory) {
  const checkoutID = createHash('sha256').update(rootDirectory).digest('hex').slice(0, 12)
  return `kcdev-${checkoutID}`
}

export function assertDevelopmentPort(port, option) {
  if (port === reservedProductionPort) {
    throw new Error(`${option} may not use reserved production port ${reservedProductionPort}`)
  }
}

export function assertDevelopmentApiTarget(configuredPort, configuredUrl) {
  const port = String(configuredPort ?? '').trim()
  if (/^\d+$/.test(port)) {
    assertDevelopmentPort(Number(port), 'Vite API target')
  }

  const value = String(configuredUrl ?? '').trim()
  if (!value) return
  const url = new URL(value, 'http://kiwi-code.invalid')
  if (url.port) {
    assertDevelopmentPort(Number(url.port), 'Vite API target')
  }
}

export function usage() {
  return `Usage:
  npm run dev:servers -- [options]
  npm run dev:desktop -- [options]

Options:
  --vite-port <port>     Vite listen port (default: ${defaultVitePort})
  --go-port <port>       Go server listen port (default: ${defaultGoPort})
  --tmux-socket <name>   tmux socket name (default: isolated per checkout)
  --add-current-directory
                         Add the checkout root as a project at server startup
  --loopback             Bind both servers to 127.0.0.1
  --help                 Show this help

Development safety:
  Port ${reservedProductionPort} and tmux socket ${productionTmuxSocket} are reserved for production.
`
}

function parsePort(value, option) {
  if (!/^\d+$/.test(value ?? '')) throw new Error(`${option} requires a numeric port`)
  const port = Number(value)
  if (!Number.isSafeInteger(port) || port < 1 || port > 65535) {
    throw new Error(`${option} must be between 1 and 65535`)
  }
  return port
}

export function parseArgs(args) {
  const options = {
    desktop: false,
    loopback: false,
    addCurrentDirectory: false,
    help: false,
    vitePort: defaultVitePort,
    goPort: defaultGoPort,
    tmuxSocket: '',
  }

  for (let index = 0; index < args.length; index += 1) {
    const argument = args[index]
    if (argument === '--desktop') {
      options.desktop = true
      continue
    }
    if (argument === '--loopback') {
      options.loopback = true
      continue
    }
    if (argument === '--add-current-directory') {
      options.addCurrentDirectory = true
      continue
    }
    if (argument === '--help' || argument === '-h') {
      options.help = true
      continue
    }

    const separator = argument.indexOf('=')
    const name = separator === -1 ? argument : argument.slice(0, separator)
    const inlineValue = separator === -1 ? undefined : argument.slice(separator + 1)
    if (name !== '--vite-port' && name !== '--go-port' && name !== '--tmux-socket') {
      throw new Error(`unknown option: ${argument}`)
    }

    const value = inlineValue ?? args[++index]
    if (name === '--tmux-socket') {
      if (!/^[A-Za-z0-9._-]{1,64}$/.test(value ?? '') || value === '.' || value === '..') {
        throw new Error('--tmux-socket must be 1-64 letters, numbers, dots, hyphens, or underscores')
      }
      if (value === productionTmuxSocket) {
        throw new Error(`--tmux-socket may not use the production tmux server ${value}`)
      }
      options.tmuxSocket = value
      continue
    }

    const port = parsePort(value, name)
    assertDevelopmentPort(port, name)
    if (name === '--vite-port') options.vitePort = port
    if (name === '--go-port') options.goPort = port
  }

  if (options.vitePort === options.goPort) {
    throw new Error('--vite-port and --go-port must be different')
  }
  return options
}
