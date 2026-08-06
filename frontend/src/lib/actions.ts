const FOCUSABLE = 'button, [href], input, select, textarea, [tabindex]:not([tabindex="-1"])';

/**
 * Traps Tab focus inside the node, focuses the first control on mount and
 * restores focus to the previously focused element on destroy.
 */
export function focusTrap(node: HTMLElement) {
  const previouslyFocused = document.activeElement as HTMLElement | null;

  function focusable(): HTMLElement[] {
    return Array.from(node.querySelectorAll<HTMLElement>(FOCUSABLE))
      .filter((el) => !el.hasAttribute('disabled'));
  }

  function keydown(event: KeyboardEvent) {
    if (event.key !== 'Tab') return;
    const items = focusable();
    if (!items.length) return;
    const first = items[0];
    const last = items[items.length - 1];
    if (event.shiftKey && document.activeElement === first) {
      event.preventDefault();
      last.focus();
    } else if (!event.shiftKey && document.activeElement === last) {
      event.preventDefault();
      first.focus();
    }
  }

  node.addEventListener('keydown', keydown);
  focusable()[0]?.focus();

  return {
    destroy() {
      node.removeEventListener('keydown', keydown);
      // The element that opened the dialog may have been removed from the
      // list while the drawer was open; never focus a detached node.
      if (previouslyFocused && previouslyFocused.isConnected) previouslyFocused.focus();
    }
  };
}

/** Focuses and selects the input content when it mounts. */
export function autofocus(node: HTMLInputElement) {
  node.focus();
  node.select();
}
