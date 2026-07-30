import assert from 'node:assert/strict'
import test from 'node:test'

import { withE2EHarness } from './support/harness.mjs'

const personalProfileID = 'personal'
const activeProfileStorageKey = 'kiwi-code-active-profile'
const expectedRenderedPageCount = 23

function pause(milliseconds) {
  return new Promise((resolve) => setTimeout(resolve, milliseconds))
}

function route(harness, pathname) {
  return new URL(pathname, harness.baseURL).href
}

async function waitForPath(page, pathname) {
  await page.waitForFunction(
    (expected) => window.location.pathname === expected,
    {},
    pathname,
  )
}

async function waitForExactText(page, selector, text) {
  await page.waitForFunction(
    (candidateSelector, expected) => Array.from(document.querySelectorAll(candidateSelector))
      .some((element) => element.getClientRects().length > 0 && element.textContent?.trim() === expected),
    {},
    selector,
    text,
  )
}

async function waitForText(page, text) {
  await page.waitForFunction(
    (expected) => document.body?.innerText.includes(expected),
    {},
    text,
  )
}

async function waitForAriaLabel(page, role, label) {
  await page.waitForFunction(
    (expectedRole, expectedLabel) => Array.from(document.querySelectorAll(`[role="${expectedRole}"]`))
      .some((element) => element.getClientRects().length > 0 && element.getAttribute('aria-label') === expectedLabel),
    {},
    role,
    label,
  )
}

async function openPage(harness, pathname, { hard = true } = {}) {
  const target = new URL(route(harness, pathname))
  const current = new URL(harness.page.url())
  if (hard || current.origin !== target.origin) {
    const response = await harness.page.goto(target.href, {
      waitUntil: 'domcontentloaded',
    })
    assert.ok(response, `navigation to ${pathname} did not receive a document response`)
    assert.equal(response.status(), 200, `navigation to ${pathname}`)
  } else {
    await harness.page.evaluate((nextPath) => {
      window.history.pushState(null, '', nextPath)
      window.dispatchEvent(new PopStateEvent('popstate'))
    }, `${target.pathname}${target.search}${target.hash}`)
  }
  assert.equal(await harness.page.title(), 'Kiwi Code')
}

async function selectOption(page, triggerSelector, optionText, { contains = false } = {}) {
  await page.waitForFunction(
    (selector) => {
      const element = document.querySelector(selector)
      return element instanceof HTMLButtonElement && !element.disabled
    },
    {},
    triggerSelector,
  )
  await page.click(triggerSelector)
  await page.waitForFunction(
    (expected, matchSubstring) => Array.from(document.querySelectorAll('[role="option"]'))
      .some((element) => {
        const text = element.textContent?.trim() ?? ''
        return matchSubstring ? text.includes(expected) : text === expected
      }),
    {},
    optionText,
    contains,
  )
  const selected = await page.evaluate((expected, matchSubstring) => {
    const option = Array.from(document.querySelectorAll('[role="option"]'))
      .find((element) => {
        const text = element.textContent?.trim() ?? ''
        return matchSubstring ? text.includes(expected) : text === expected
      })
    if (!(option instanceof HTMLElement)) return false
    option.click()
    return true
  }, optionText, contains)
  assert.equal(selected, true, `select option ${optionText}`)
}

async function waitForProjectThread(harness, threadID, predicate, timeoutMilliseconds = 30_000) {
  const deadline = Date.now() + timeoutMilliseconds
  let latest
  while (Date.now() < deadline) {
    const projects = await harness.api('/api/projects')
    latest = projects
      .flatMap((project) => project.threads.map((thread) => ({ project, thread })))
      .find((candidate) => candidate.thread.id === threadID)
    if (latest && predicate(latest.thread)) return latest
    await pause(100)
  }
  throw new Error(`Thread ${threadID} did not reach the expected state. Latest: ${JSON.stringify(latest)}`)
}

async function waitForReports(harness, predicate, timeoutMilliseconds = 30_000) {
  const deadline = Date.now() + timeoutMilliseconds
  let entries = []
  while (Date.now() < deadline) {
    entries = await harness.readPiReports()
    if (predicate(entries)) return entries
    await pause(100)
  }
  throw new Error(`Pi fixture reports did not reach the expected state: ${JSON.stringify(entries, null, 2)}`)
}

test('every Kiwi Code page renders and real Pi follows the deterministic chat fixture', {
  timeout: 600_000,
}, async (t) => {
  await withE2EHarness(async (harness) => {
    const renderedPages = new Set()
    const { page, project, workspaceThread, fixture } = harness
    const projectPrefix = `/projects/${encodeURIComponent(project.id)}`
    const threadPrefix = `${projectPrefix}/threads/${encodeURIComponent(workspaceThread.id)}`

    async function renderedPage(name, callback) {
      await t.test(name, async () => {
        await callback()
        renderedPages.add(name)
      })
    }

    await renderedPage('empty profile landing page', async () => {
      await openPage(harness, '/settings/worktrees')
      await page.evaluate(
        (key, value) => window.localStorage.setItem(key, value),
        activeProfileStorageKey,
        harness.emptyProfile.id,
      )
      await openPage(harness, '/', { hard: true })
      await waitForExactText(page, 'h1', `Add a project to ${harness.emptyProfile.name}`)
      await page.evaluate(
        (key, value) => window.localStorage.setItem(key, value),
        activeProfileStorageKey,
        personalProfileID,
      )
      await openPage(harness, '/settings/worktrees', { hard: true })
    })

    const globalSettingsPages = [
      ['global worktree settings page', 'worktrees', 'Git worktrees'],
      ['global coding-agent settings page', 'profiles', 'Coding agents'],
      ['global cleanup settings page', 'cleanup', 'Automatic cleanup'],
      ['global agents and workflows settings page', 'agents', 'Sub-agent nesting'],
      ['global appearance settings page', 'appearance', 'Appearance'],
      ['global sandbox settings page', 'sandbox', 'Configuration file'],
      ['global skills settings page', 'skills', 'Agent skills'],
    ]
    for (const [name, section, heading] of globalSettingsPages) {
      await renderedPage(name, async () => {
        await openPage(harness, `/settings/${section}`)
        await waitForPath(page, `/settings/${section}`)
        await waitForExactText(page, 'h2', heading)
      })
    }

    const projectSettingsPages = [
      ['project profile settings page', 'profile', 'Profile'],
      ['project environment settings page', 'environment', 'Local environment'],
      ['project branch settings page', 'branches', 'Worktree branches'],
    ]
    for (const [name, section, heading] of projectSettingsPages) {
      await renderedPage(name, async () => {
        const pathname = `${projectPrefix}/settings/${section}`
        await openPage(harness, pathname)
        await waitForPath(page, pathname)
        await waitForExactText(page, 'h2', heading)
      })
    }

    await renderedPage('cleanup page', async () => {
      await openPage(harness, '/cleanup')
      await waitForExactText(page, 'h1', 'Scheduled deletion')
    })

    await renderedPage('closed-session log page', async () => {
      await openPage(harness, '/session-log')
      await waitForExactText(page, 'h1', 'Closed tmux sessions')
    })

    await renderedPage('new thread page', async () => {
      await openPage(harness, `${projectPrefix}/threads/new`)
      await waitForExactText(page, 'p', 'New thread')
      await page.waitForSelector('#thread-coding-agent')
      await page.waitForSelector('#thread-initial-prompt')
    })

    await renderedPage('thread sandbox page', async () => {
      await openPage(harness, `${threadPrefix}/sandbox`)
      await waitForExactText(page, 'h1', 'Thread sandbox')
      await waitForExactText(page, 'h2', 'Network and shell')
    })

    const terminalPages = [
      ['shell workspace page', 'shell', 'E2E Workspace shell session'],
      ['Neovim workspace page', 'nvim', 'E2E Workspace nvim session'],
      ['Lazygit workspace page', 'lazygit', 'E2E Workspace lazygit session'],
    ]
    for (const [name, tool, label] of terminalPages) {
      await renderedPage(name, async () => {
        await openPage(harness, `${threadPrefix}/${tool}`)
        await waitForAriaLabel(page, 'tabpanel', label)
      })
    }

    await renderedPage('process workspace page', async () => {
      await openPage(harness, `${threadPrefix}/process`)
      await waitForExactText(page, 'p', 'No process shells')
    })

    await renderedPage('browser workspace page', async () => {
      await openPage(harness, `${threadPrefix}/browser`)
      await waitForAriaLabel(page, 'tabpanel', 'E2E Workspace browser workspace')
      await waitForText(page, 'No browser session yet')
    })

    let chatThreadID
    await renderedPage('Pi Native workspace page with real fixture chat', async () => {
      await openPage(harness, `${projectPrefix}/threads/new`)
      await waitForExactText(page, 'p', 'New thread')
      await page.waitForSelector('#thread-initial-prompt')

      await selectOption(page, '#thread-coding-agent', 'Pi Native')
      await selectOption(page, '#thread-agent-model', fixture.model.name, { contains: true })
      await selectOption(page, '#thread-agent-thinking', 'Low')

      const firstPrompt = fixture.steps.find((step) => step.id === 'inspect-readme')?.when.lastUserText
      const firstReply = fixture.steps.find((step) => step.id === 'answer-project-name')
        ?.reply.content.find((block) => block.type === 'text')?.text
      const secondPrompt = fixture.steps.find((step) => step.id === 'answer-second-turn')?.when.lastUserText
      const secondReply = fixture.steps.find((step) => step.id === 'answer-second-turn')
        ?.reply.content.find((block) => block.type === 'text')?.text
      const expectedTitle = fixture.titleSteps[0]?.text
      assert.equal(typeof firstPrompt, 'string')
      assert.equal(typeof firstReply, 'string')
      assert.equal(typeof secondPrompt, 'string')
      assert.equal(typeof secondReply, 'string')
      assert.equal(typeof expectedTitle, 'string')

      await page.click('#thread-initial-prompt')
      await page.type('#thread-initial-prompt', firstPrompt)
      await page.waitForFunction(() => Array.from(document.querySelectorAll('button'))
        .some((button) => button.textContent?.trim() === 'Start Pi Native' && !button.disabled))
      const submitted = await page.evaluate(() => {
        const button = Array.from(document.querySelectorAll('button'))
          .find((candidate) => candidate.textContent?.trim() === 'Start Pi Native')
        if (!(button instanceof HTMLButtonElement) || button.disabled) return false
        button.click()
        return true
      })
      assert.equal(submitted, true)

      await page.waitForFunction(
        (prefix) => window.location.pathname.startsWith(prefix) && window.location.pathname.endsWith('/pi'),
        {},
        `${projectPrefix}/threads/`,
      )
      const pathSegments = new URL(page.url()).pathname.split('/').filter(Boolean)
      chatThreadID = pathSegments[pathSegments.indexOf('threads') + 1]
      assert.ok(chatThreadID)
      assert.notEqual(chatThreadID, workspaceThread.id)

      await page.waitForSelector('[data-testid="pi-native-conversation"]')
      await waitForText(page, firstReply)
      await page.waitForFunction(() => {
        const button = document.querySelector('[data-testid="pi-native-send"]')
        const composer = document.querySelector('[data-testid="pi-native-composer"]')
        return button instanceof HTMLButtonElement
          && composer instanceof HTMLTextAreaElement
          && !composer.disabled
          && button.getAttribute('aria-label') === 'Send message'
      })

      await page.type('[data-testid="pi-native-composer"]', secondPrompt)
      await page.waitForFunction(() => {
        const button = document.querySelector('[data-testid="pi-native-send"]')
        return button instanceof HTMLButtonElement && !button.disabled
      })
      await page.click('[data-testid="pi-native-send"]')
      await waitForText(page, secondReply)

      await waitForProjectThread(
        harness,
        chatThreadID,
        (thread) => thread.title === expectedTitle,
      )

      await page.click('[data-testid="pi-native-activity-toggle"]')
      await page.waitForFunction(() => {
        const usage = document.querySelector('[data-testid="pi-native-session-usage"]')
        return usage?.getAttribute('aria-label')?.startsWith('Pi session usage:')
      })
      const usageLabel = await page.$eval(
        '[data-testid="pi-native-session-usage"]',
        (element) => element.getAttribute('aria-label'),
      )
      assert.match(usageLabel, /input/)
      assert.match(usageLabel, /output/)
      assert.match(usageLabel, /\$/)

      const expectedStepIDs = new Set([
        'inspect-readme',
        'answer-project-name',
        'answer-second-turn',
        fixture.titleSteps[0].id,
      ])
      const reports = await waitForReports(harness, (entries) => {
        const matchedIDs = new Set(entries.filter((entry) => entry.matched).map((entry) => entry.stepId))
        return [...expectedStepIDs].every((id) => matchedIDs.has(id))
      })
      const threadReports = reports.filter((entry) => entry.threadId === chatThreadID)
      assert.ok(threadReports.length >= 4)
      assert.equal(threadReports.every((entry) => entry.matched === true), true)
      assert.deepEqual(
        new Set(threadReports.map((entry) => entry.modelId)),
        new Set([fixture.model.id, 'gpt-5.6-luna']),
      )
      const toolResultReport = threadReports.find((entry) => entry.stepId === 'answer-project-name')
      assert.equal(toolResultReport.context.lastToolResult.toolName, 'read')
      assert.equal(toolResultReport.context.lastToolResult.isError, false)
      assert.match(toolResultReport.context.lastToolResult.text, /Kiwi Code/)
    })

    await renderedPage('tmux sessions page', async () => {
      await openPage(harness, '/tmux')
      await waitForExactText(page, 'p', 'tmux sessions')
      await page.waitForFunction(() => {
        const button = document.querySelector('[aria-label="Refresh tmux sessions"]')
        return button instanceof HTMLButtonElement && !button.disabled
      })
    })

    await t.test('redirect routes resolve to their canonical rendered pages', async () => {
      await openPage(harness, '/settings')
      await waitForPath(page, '/settings/worktrees')
      await waitForExactText(page, 'h2', 'Git worktrees')

      await openPage(harness, `${projectPrefix}/settings`)
      await waitForPath(page, `${projectPrefix}/settings/profile`)
      await waitForExactText(page, 'h2', 'Profile')

      const chatPrefix = `${projectPrefix}/threads/${encodeURIComponent(chatThreadID)}`
      for (const legacyPath of [
        projectPrefix,
        chatPrefix,
        '/route-that-does-not-exist',
      ]) {
        await openPage(harness, legacyPath)
        await waitForPath(page, `${chatPrefix}/pi`)
        await page.waitForSelector('[data-testid="pi-native-conversation"]')
      }
    })

    assert.equal(
      renderedPages.size,
      expectedRenderedPageCount,
      `rendered page inventory: ${JSON.stringify([...renderedPages])}`,
    )
    harness.assertNoBrowserFailures()
  })
})
