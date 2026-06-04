import { describe, it, expect } from 'vitest'
import { render, screen } from '@testing-library/react'
import { StatusBadge } from './StatusBadge'
import type { CheckStatus } from '../api'

const cases: Array<{ status: CheckStatus; label: string; bg: string }> = [
  { status: 'new', label: 'New', bg: 'bg-slate-200' },
  { status: 'up', label: 'Up', bg: 'bg-emerald-100' },
  { status: 'grace', label: 'Late', bg: 'bg-amber-100' },
  { status: 'down', label: 'Down', bg: 'bg-rose-100' },
  { status: 'paused', label: 'Paused', bg: 'bg-slate-100' },
]

describe('StatusBadge', () => {
  it.each(cases)('renders $label with severity class for $status', ({ status, label, bg }) => {
    render(<StatusBadge status={status} />)
    const el = screen.getByText(label)
    expect(el).toBeInTheDocument()
    expect(el.className).toContain(bg)
  })
})
