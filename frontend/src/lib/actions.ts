const FOCUSABLE = 'button, [href], input, select, textarea, [tabindex]:not([tabindex="-1"])';

/**
 * Traps Tab focus inside the node, focuses the first control on mount and
 * restores focus to the previously focused element on destroy.
 */
export function focusTrap(node: HTMLElement) {
  const previouslyFocused = document.activeElement as HTMLElement | null;

  function focusable(): HTMLElement[] {
    return Array.from(node.querySelectorAll<HTMLElement>(FOCUSABLE))
      .filter((el) => !el.hasAttribute('disabled') && !el.hasAttribute('hidden'));
  }

  function keydown(event: KeyboardEvent) {
    if (event.key !== 'Tab') return;
    const items = focusable();
    if (!items.length) {
      // Every control can be disabled at once (a modal busy-saving disables
      // all of them). Without a preventDefault the browser default would move
      // focus out of the trap; hold it on the dialog container instead.
      event.preventDefault();
      if (node.hasAttribute('tabindex')) node.focus();
      return;
    }
    const first = items[0];
    const last = items[items.length - 1];
    const active = document.activeElement;
    if (active === node || !node.contains(active)) {
      event.preventDefault();
      (event.shiftKey ? last : first).focus();
    } else if (event.shiftKey && active === first) {
      event.preventDefault();
      last.focus();
    } else if (!event.shiftKey && active === last) {
      event.preventDefault();
      first.focus();
    }
  }

  node.addEventListener('keydown', keydown);
  // A dialog container carrying tabindex="-1" is the better initial target:
  // screen readers announce the dialog's accessible name before any control,
  // and keyboard users can Tab into the content from there.
  if (node.hasAttribute('tabindex')) {
    node.focus();
  } else {
    focusable()[0]?.focus();
  }

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
