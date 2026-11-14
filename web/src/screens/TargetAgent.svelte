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

  // D-367: does the agent see hand changes of `ip` / `nft` live? Missing for
  // a type the Target does not use; null before a hello that reports it.
  const WATCH = {
    network: { what: "the network settings", tool: "ip" },
    firewall: { what: "the firewall", tool: "nft" },
  } as const;
  const watch = $derived(
    Object.entries(target?.status.agent_live_watch ?? {}).filter(
      (e): e is [keyof typeof WATCH, boolean] => e[0] in WATCH && typeof e[1] === "boolean",
    ),
  );

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
{#if target}
  <ProblemBox {error} />
  <AgentCard {api} {target} headingLevel={1} />
  {#if watch.length}
    <section class="card" aria-labelledby="watch-title" id="live-watch">
      <h2 id="watch-title">Hand changes in the guest</h2>
      <ul>
        {#each watch as [type, live] (type)}
          <li id="live-watch-{type}" class:warn={!live}>
            {#if live}
              The agent sees a change by hand of {WATCH[type].what} (<code>{WATCH[type].tool}</code
              >) within seconds.
            {:else}
              The agent cannot watch {WATCH[type].what} live. A change by hand with
              <code>{WATCH[type].tool}</code> is seen at the full check, every 15 minutes.
            {/if}
          </li>
        {/each}
      </ul>
      <p class="muted">A change of a file that Plumb writes is seen at once.</p>
    </section>
  {/if}
{:else}
  <!-- The page title is the focus target of a route change, also on a load error. -->
  <h1 id="page-title" tabindex="-1">Agent of <span class="mono">{name}</span></h1>
  <ProblemBox {error} />
  {#if !error}<p class="muted" aria-live="polite">Loading…</p>{/if}
{/if}

<style>
  ul {
    margin: 0 0 var(--space-2);
    padding-left: var(--space-6);
  }
  .warn {
    color: var(--color-warn);
  }
</style>
