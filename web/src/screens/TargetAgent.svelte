<script lang="ts">
  // The agent of one Target (T-097). The gate refusal links here.
  import type { PlumbApi } from "../lib/client";
  import { liveSnapshot } from "../lib/live";
  import { href } from "../lib/router";
  import type { TargetView } from "../lib/types";
  import AgentCard from "../ui/AgentCard.svelte";
  import ProblemBox from "../ui/ProblemBox.svelte";

  let { api, name }: { api: PlumbApi; name: string } = $props();
  let target = $state<TargetView | null>(null);
  let error = $state<unknown>(null);

  $effect(() => {
    const live = liveSnapshot({
      load: () => api.getTarget(name),
      subscribe: (onEvent, onOpen) => api.events(undefined, onEvent, onOpen),
      onSnapshot: (t) => (target = t),
      onError: (e) => (error = e),
      onEvent: (e, l) => {
        if (e.subject === `target:${name}` || e.type === "run.finished") l.reloadSoon();
      },
    });
    return () => live.stop();
  });
</script>

<p class="back"><a href={href({ name: "target-edit", target: name })}>← Target {name}</a></p>
<ProblemBox {error} />
{#if target}
  <AgentCard {api} {target} headingLevel={1} />
{:else if !error}
  <h1 id="page-title" tabindex="-1">Agent of <span class="mono">{name}</span></h1>
  <p class="muted" aria-live="polite">Loading…</p>
{/if}
