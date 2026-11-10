<script lang="ts">
  import type { PlumbApi } from "../lib/client";
  import type { Session } from "../lib/types";
  import ProblemBox from "../ui/ProblemBox.svelte";

  let {
    api,
    onlogin,
    hint,
    notice,
  }: { api: PlumbApi; onlogin: (s: Session) => void; hint?: string; notice?: string | undefined } =
    $props();
  let username = $state("");
  let password = $state("");
  let error = $state<unknown>(null);
  let busy = $state(false);

  async function submit(e: SubmitEvent) {
    e.preventDefault();
    busy = true;
    error = null;
    try {
      onlogin(await api.login(username, password));
    } catch (err) {
      error = err;
    } finally {
      busy = false;
    }
  }
</script>

<div class="narrow">
  <h1 id="page-title" tabindex="-1">Log in to Plumb</h1>
  {#if notice}<p class="banner warn" role="status">{notice}</p>{/if}
  <ProblemBox {error} />
  <form class="card" onsubmit={submit}>
    <div class="field">
      <label for="login-user">User name</label>
      <input id="login-user" type="text" autocomplete="username" required bind:value={username} />
    </div>
    <div class="field">
      <label for="login-password">Password</label>
      <input
        id="login-password"
        type="password"
        autocomplete="current-password"
        required
        bind:value={password}
      />
    </div>
    <button class="btn primary" type="submit" disabled={busy}>Log in</button>
  </form>
  {#if hint}<p class="muted">{hint}</p>{/if}
</div>

<style>
  .narrow {
    max-width: 24rem;
    margin: var(--space-8) auto;
  }
  input {
    width: 100%;
  }
</style>
