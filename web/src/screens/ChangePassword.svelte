<script lang="ts">
  import type { PlumbApi } from "../lib/client";
  import ProblemBox from "../ui/ProblemBox.svelte";

  let { api, ondone }: { api: PlumbApi; ondone: () => void } = $props();
  let current = $state("");
  let next = $state("");
  let confirm = $state("");
  let error = $state<unknown>(null);
  let mismatch = $state(false);
  let busy = $state(false);

  async function submit(e: SubmitEvent) {
    e.preventDefault();
    mismatch = next !== confirm;
    if (mismatch) return;
    busy = true;
    error = null;
    try {
      await api.changePassword(current, next);
      ondone();
    } catch (err) {
      error = err;
    } finally {
      busy = false;
    }
  }
</script>

<div class="narrow">
  <h1 id="page-title" tabindex="-1">Change your password</h1>
  <p class="banner info">This is your first login. Choose a new password before you continue.</p>
  <ProblemBox {error} />
  <form class="card" onsubmit={submit}>
    <div class="field">
      <label for="pw-current">Current password</label>
      <input
        id="pw-current"
        type="password"
        autocomplete="current-password"
        required
        bind:value={current}
      />
    </div>
    <div class="field">
      <label for="pw-new">New password</label>
      <input
        id="pw-new"
        type="password"
        autocomplete="new-password"
        required
        minlength="12"
        aria-describedby="pw-new-hint"
        bind:value={next}
      />
      <span class="hint" id="pw-new-hint">At least 12 characters.</span>
    </div>
    <div class="field">
      <label for="pw-confirm">Repeat the new password</label>
      <input
        id="pw-confirm"
        type="password"
        autocomplete="new-password"
        required
        aria-invalid={mismatch}
        aria-describedby={mismatch ? "pw-confirm-error" : undefined}
        bind:value={confirm}
      />
      {#if mismatch}<span class="error" id="pw-confirm-error"
          >The two passwords are not the same.</span
        >{/if}
    </div>
    <button class="btn primary" type="submit" disabled={busy}>Change password</button>
  </form>
</div>

<style>
  .narrow {
    max-width: 26rem;
    margin: var(--space-8) auto;
  }
  input {
    width: 100%;
  }
</style>
