import '@testing-library/jest-dom/vitest';
import { cleanup, render, screen } from '@testing-library/svelte';
import { afterEach, describe, expect, it } from 'vitest';
import StateBadge from './StateBadge.svelte';

afterEach(cleanup);

describe('StateBadge', () => {
  it('renders the label with the matching state class', () => {
    render(StateBadge, { props: { label: 'on' } });
    expect(screen.getByText('on')).toHaveClass('state-badge', 'state-on');
  });

  it('falls back to the unknown class for oddly cased backend labels', () => {
    render(StateBadge, { props: { label: 'StandBy' } });
    const badge = screen.getByText('StandBy');
    expect(badge).toHaveClass('state-unknown');
    expect(badge).not.toHaveClass('state-standby');
  });

  it('marks unverified and stale variants', () => {
    render(StateBadge, { props: { label: 'sleep', unverified: true } });
    expect(screen.getByText('sleep')).toHaveClass('unverified');
    cleanup();
    render(StateBadge, { props: { label: 'sleep', stale: true } });
    expect(screen.getByText('sleep')).toHaveClass('stale');
  });

  it('shows a spinner while booting', () => {
    const { container } = render(StateBadge, { props: { label: 'booting', booting: true } });
    expect(container.querySelector('.spin')).not.toBeNull();
  });
});
