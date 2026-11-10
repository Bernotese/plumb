<script lang="ts">
  import { ApiError, get, type TargetView } from "./lib/api";

  type Load =
    | { state: "loading" }
    | { state: "ok"; targets: TargetView[] }
    | { state: "error"; message: string };

  let load = $state<Load>({ state: "loading" });

  $effect(() => {
    const ctrl = new AbortController();
    get("/targets", { signal: ctrl.signal })
      .then((page) => (load = { state: "ok", targets: page.items }))
      .catch((err: unknown) => {
        if (ctrl.signal.aborted) return;
        load = {
          state: "error",
          message:
            err instanceof ApiError && err.problem.type !== "about:blank"
              ? err.message
              : "The Plumb API is not reachable. Try again later.",
        };
      });
    return () => ctrl.abort();
  });
</script>

<header>
  <span class="brand">Plumb</span>
</header>

<main>
  <h1>Targets</h1>
  {#if load.state === "loading"}
    <p class="muted" aria-live="polite">Loading…</p>
  {:else if load.state === "error"}
    <p class="error" role="alert">{load.message}</p>
  {:else if load.targets.length === 0}
    <p class="muted">No targets yet.</p>
  {:else}
    <table>
      <thead>
        <tr><th scope="col">Name</th><th scope="col">Type</th><th scope="col">Status</th></tr>
      </thead>
      <tbody>
        {#each load.targets as t (t.document.name)}
          <tr>
            <td class="mono">{t.document.name}</td>
            <td>{t.document.spec.type}</td>
            <td>{t.status.phase}</td>
          </tr>
        {/each}
      </tbody>
    </table>
  {/if}
</main>

<style>
  header {
    display: flex;
    align-items: center;
    padding: var(--space-3) var(--space-6);
    background: var(--color-surface);
    border-bottom: 1px solid var(--color-border);
  }
  .brand {
    font-weight: 700;
    font-size: var(--text-lg);
  }
  main {
    max-width: 960px;
    margin: 0 auto;
    padding: var(--space-6);
  }
  h1 {
    font-size: var(--text-lg);
    margin: 0 0 var(--space-4);
  }
  .muted {
    color: var(--color-text-muted);
  }
  .error {
    color: var(--color-danger);
  }
  .mono {
    font-family: var(--font-mono);
  }
  table {
    width: 100%;
    border-collapse: collapse;
    background: var(--color-surface);
    border: 1px solid var(--color-border);
    border-radius: var(--radius);
  }
  th,
  td {
    text-align: left;
    padding: var(--space-2) var(--space-3);
    border-bottom: 1px solid var(--color-border);
    font-size: var(--text-sm);
  }
</style>
