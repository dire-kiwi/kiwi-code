import assert from 'node:assert/strict'
import test from 'node:test'
import {
  collapsePromptPaste,
  expandPromptPastes,
  prunePromptPastes,
} from '../src/prompt-pastes.mjs'

test('large multiline pastes collapse to a Pi-style marker and expand for submission', () => {
  const content = Array.from({ length: 19 }, (_, index) => `line ${index + 1}`).join('\n')
  const result = collapsePromptPaste({
    value: 'before after',
    selectionStart: 7,
    selectionEnd: 7,
    pastedText: content,
    pastes: [],
  })

  assert.ok(result)
  assert.equal(result.value, 'before [paste #1 +19 lines]after')
  assert.equal(expandPromptPastes(result.value, result.pastes), `before ${content}after`)
})

test('large single-line pastes use a character-count marker', () => {
  const content = 'x'.repeat(1_001)
  const result = collapsePromptPaste({
    value: '',
    selectionStart: 0,
    selectionEnd: 0,
    pastedText: content,
    pastes: [],
  })

  assert.ok(result)
  assert.equal(result.value, '[paste #1 1001 chars]')
  assert.equal(expandPromptPastes(result.value, result.pastes), content)
})

test('small pastes remain native textarea pastes', () => {
  assert.equal(collapsePromptPaste({
    value: '',
    selectionStart: 0,
    selectionEnd: 0,
    pastedText: 'one\ntwo',
    pastes: [],
  }), null)
})

test('removed markers discard their hidden pasted content', () => {
  const pastes = [
    { id: 1, content: 'first' },
    { id: 2, content: 'second' },
  ]
  assert.deepEqual(prunePromptPastes('[paste #2 1001 chars]', pastes), [pastes[1]])
})

test('initial prompt limits apply to expanded paste content', () => {
  const content = Array.from({ length: 20 }, () => 'abcdefghij').join('\n')
  const result = collapsePromptPaste({
    value: 'prefix',
    selectionStart: 6,
    selectionEnd: 6,
    pastedText: content,
    pastes: [],
    maxExpandedLength: 100,
  })

  assert.ok(result)
  assert.equal(expandPromptPastes(result.value, result.pastes).length, 100)
})
