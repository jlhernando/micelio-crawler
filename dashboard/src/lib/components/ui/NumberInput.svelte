<script lang="ts">
  import { untrack } from 'svelte';

  let {
    value = $bindable(0),
    id = '',
    min = undefined as number | undefined,
    max = undefined as number | undefined,
    step = undefined as number | string | undefined,
    className = '',
  } = $props();

  let focused = $state(false);
  let displayValue = $state('');
  // Empty or not a number after editing. The bound value keeps its last valid
  // number, so forms must check `data-number-invalid` before using it.
  let invalid = $state(false);

  function format(n: number): string {
    if (n == null || isNaN(n)) return '';
    return n.toLocaleString('en-US');
  }

  // Text to number, ignoring thousands separators and spaces, clamped to
  // min/max. null when the text is empty or not a number.
  function parse(text: string): number | null {
    const t = String(text ?? '').replace(/[,\s]/g, '');
    if (t === '') return null;
    const n = Number(t);
    if (!Number.isFinite(n)) return null;
    let clamped = n;
    if (min != null && clamped < min) clamped = min;
    if (max != null && clamped > max) clamped = max;
    return clamped;
  }

  // A new value from the parent (preset, saved settings) replaces the text.
  $effect(() => {
    const v = value;
    if (!untrack(() => focused)) {
      displayValue = format(v);
      invalid = false;
    }
  });

  // Show the value that will be sent, or an empty invalid field. Writes the
  // DOM directly: an edit and a blur in the same tick can leave the state
  // equal to its old value, and then Svelte skips the update.
  function settle(input: HTMLInputElement) {
    invalid = parse(input.value) === null;
    displayValue = invalid ? '' : format(value);
    input.value = displayValue;
  }

  // The text is left as it is on focus. Rewriting it ("1,000,000" to
  // "1000000") cleared the user's selection, so typing appended digits.
  function onFocus() {
    focused = true;
  }

  function onBlur(e: FocusEvent) {
    focused = false;
    settle(e.target as HTMLInputElement);
  }

  // Each edit updates the bound value at once, so a Start click that does not
  // blur the field still sends what was typed.
  function onInput(e: Event) {
    const target = e.target as HTMLInputElement;
    const parsed = parse(target.value);
    if (parsed !== null) value = parsed;
    if (focused) {
      displayValue = target.value;
      invalid = parsed === null;
    } else {
      settle(target); // programmatic edit without focus: no blur will follow
    }
  }
</script>

<input
  {id}
  type="text"
  inputmode="numeric"
  value={displayValue}
  class="{className} aria-[invalid=true]:border-danger"
  aria-invalid={invalid}
  data-number-invalid={invalid ? '' : undefined}
  onfocus={onFocus}
  onblur={onBlur}
  oninput={onInput}
/>
