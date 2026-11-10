<script lang="ts">
  import type { Tone } from "../lib/view";

  let { tone, label, title }: { tone: Tone; label: string; title?: string } = $props();
</script>

<span class="badge {tone}" {title}>{label}</span>

<style>
  .badge {
    display: inline-block;
    font-size: var(--text-xs);
    font-weight: 700;
    padding: 0.1rem 0.5rem;
    border-radius: 999px;
    border: 1px solid currentColor;
    white-space: nowrap;
  }
  .ok {
    color: var(--color-ok);
    background: var(--color-ok-bg);
  }
  .warn {
    color: var(--color-warn);
    background: var(--color-warn-bg);
  }
  .danger {
    color: var(--color-danger);
    background: var(--color-danger-bg);
  }
  .info {
    color: var(--color-info);
    background: var(--color-info-bg);
  }
  .muted {
    color: var(--color-text-muted);
    background: var(--color-muted-bg);
  }
</style>
