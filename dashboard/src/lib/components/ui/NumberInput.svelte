<script lang="ts">
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

  function format(n: number): string {
    if (n == null || isNaN(n)) return '';
    return n.toLocaleString('en-US');
  }

  $effect(() => {
    if (!focused) {
      displayValue = format(value);
    }
  });

  function onFocus() {
    focused = true;
    displayValue = value != null && !isNaN(value) ? String(value) : '';
  }

  function onBlur() {
    const parsed = parseFloat(displayValue.replace(/,/g, ''));
    if (!isNaN(parsed)) {
      let clamped = parsed;
      if (min != null && clamped < min) clamped = min;
      if (max != null && clamped > max) clamped = max;
      value = clamped;
    }
    focused = false;
  }

  function onInput(e: Event) {
    const target = e.target as HTMLInputElement;
    displayValue = target.value;
  }
</script>

<input
  {id}
  type="text"
  inputmode="numeric"
  value={displayValue}
  class={className}
  onfocus={onFocus}
  onblur={onBlur}
  oninput={onInput}
/>
