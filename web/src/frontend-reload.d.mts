type FrontendReloadWindow = {
  kiwiCodeDesktopApp?: { reloadFrontend(): void | Promise<unknown> }
  direMuxDesktopApp?: { reloadFrontend(): void | Promise<unknown> }
  location: { reload(): void }
}

export function reloadFrontend(windowValue?: FrontendReloadWindow): Promise<'desktop' | 'browser'>
