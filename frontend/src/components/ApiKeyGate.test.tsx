import { describe, it, expect } from 'vitest'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { ApiKeyGate } from './ApiKeyGate'
import { setApiKey } from '../api'

describe('ApiKeyGate', () => {
  it('renders the form when no key is stored', () => {
    render(<ApiKeyGate><div>secret content</div></ApiKeyGate>)
    expect(screen.getByPlaceholderText('X-Api-Key')).toBeInTheDocument()
    expect(screen.queryByText('secret content')).not.toBeInTheDocument()
  })

  it('renders children immediately when a key is already stored', () => {
    setApiKey('preset')
    render(<ApiKeyGate><div>secret content</div></ApiKeyGate>)
    expect(screen.getByText('secret content')).toBeInTheDocument()
    expect(screen.queryByPlaceholderText('X-Api-Key')).not.toBeInTheDocument()
  })

  it('saves the key and reveals children on submit', async () => {
    const user = userEvent.setup()
    render(<ApiKeyGate><div>secret content</div></ApiKeyGate>)

    await user.type(screen.getByPlaceholderText('X-Api-Key'), 'typed-key')
    await user.click(screen.getByRole('button', { name: /continue/i }))

    expect(screen.getByText('secret content')).toBeInTheDocument()
    expect(localStorage.getItem('cadence.apiKey')).toBe('typed-key')
  })

  it('ignores whitespace-only submissions', async () => {
    const user = userEvent.setup()
    render(<ApiKeyGate><div>secret content</div></ApiKeyGate>)

    await user.type(screen.getByPlaceholderText('X-Api-Key'), '   ')
    await user.click(screen.getByRole('button', { name: /continue/i }))

    expect(screen.queryByText('secret content')).not.toBeInTheDocument()
    expect(localStorage.getItem('cadence.apiKey')).toBeNull()
  })

  it('trims whitespace around the submitted key', async () => {
    const user = userEvent.setup()
    render(<ApiKeyGate><div>secret content</div></ApiKeyGate>)

    await user.type(screen.getByPlaceholderText('X-Api-Key'), '  padded  ')
    await user.click(screen.getByRole('button', { name: /continue/i }))

    expect(localStorage.getItem('cadence.apiKey')).toBe('padded')
  })
})
