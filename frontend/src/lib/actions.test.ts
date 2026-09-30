import { afterEach, describe, expect, it } from 'vitest';
import { focusTrap } from './actions';

afterEach(() => {
  document.body.innerHTML = '';
});

describe('focusTrap', () => {
  it.each([false, true])('keeps focus inside when the focused control becomes disabled (reverse=%s)', (reverse) => {
    const dialog = document.createElement('div');
    dialog.tabIndex = -1;
    const cancel = document.createElement('button');
    const confirm = document.createElement('button');
    dialog.append(cancel, confirm);
    document.body.append(dialog);
    const action = focusTrap(dialog);
    confirm.focus();
    // An external operation can disable Confirm while this modal stays open.
    confirm.disabled = true;
    const tab = new KeyboardEvent('keydown', {
      key: 'Tab', shiftKey: reverse, bubbles: true, cancelable: true
    });
    confirm.dispatchEvent(tab);
    expect(tab.defaultPrevented).toBe(true);
    expect(document.activeElement).toBe(cancel);
    action.destroy();
  });

  it('moves forward and backward from the initially focused dialog container', () => {
    const dialog = document.createElement('div');
    dialog.tabIndex = -1;
    const first = document.createElement('button');
    const last = document.createElement('button');
    dialog.append(first, last);
    document.body.append(dialog);

    const action = focusTrap(dialog);
    expect(document.activeElement).toBe(dialog);

    dialog.dispatchEvent(new KeyboardEvent('keydown', { key: 'Tab', bubbles: true }));
    expect(document.activeElement).toBe(first);

    dialog.focus();
    dialog.dispatchEvent(new KeyboardEvent('keydown', { key: 'Tab', shiftKey: true, bubbles: true }));
    expect(document.activeElement).toBe(last);
    action.destroy();
  });

  it('wraps focus at both ends', () => {
    const dialog = document.createElement('div');
    const first = document.createElement('button');
    const last = document.createElement('button');
    dialog.append(first, last);
    document.body.append(dialog);
    const action = focusTrap(dialog);

    last.focus();
    last.dispatchEvent(new KeyboardEvent('keydown', { key: 'Tab', bubbles: true }));
    expect(document.activeElement).toBe(first);

    first.focus();
    first.dispatchEvent(new KeyboardEvent('keydown', { key: 'Tab', shiftKey: true, bubbles: true }));
    expect(document.activeElement).toBe(last);
    action.destroy();
  });
});
