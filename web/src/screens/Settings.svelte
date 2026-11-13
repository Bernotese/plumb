<script lang="ts">
  // Settings (T-035): API tokens, secrets, export and import, backups (T-118). Only a person
  // who logged in (tier admin) manages these (D-046, D-087, D-095).
  import type { PlumbApi } from "../lib/client";
  import { href, type SettingsTab } from "../lib/router";
  import type { RestoreState, Session } from "../lib/types";
  import Backups from "./Backups.svelte";
  import ExportImport from "./ExportImport.svelte";
  import Secrets from "./Secrets.svelte";
  import Tokens from "./Tokens.svelte";

  let {
    api,
    session,
    tab,
    onRestore,
  }: {
    api: PlumbApi;
    session: Session;
    tab: SettingsTab;
    onRestore?: (r: RestoreState) => void;
  } = $props();
  const tabs: { tab: SettingsTab; label: string }[] = [
    { tab: "tokens", label: "API tokens" },
    { tab: "secrets", label: "Secrets" },
    { tab: "export", label: "Export and import" },
    { tab: "backups", label: "Backups" },
  ];
</script>

<h1 id="page-title" tabindex="-1">Settings</h1>
{#if session.tier !== "admin"}
  <div class="banner warn" role="alert">
    <strong>Only a person who logged in can open the settings.</strong> You use the API token
    <span class="mono">{session.principal}</span>. A token cannot manage tokens, secrets or imports.
  </div>
{:else}
  <nav class="tabs" aria-label="Settings">
    {#each tabs as t (t.tab)}
      <a
        href={href({ name: "settings", tab: t.tab })}
        aria-current={t.tab === tab ? "page" : undefined}>{t.label}</a
      >
    {/each}
  </nav>
  {#if tab === "tokens"}
    <Tokens {api} />
  {:else if tab === "secrets"}
    <Secrets {api} />
  {:else if tab === "backups"}
    <Backups {api} {onRestore} />
  {:else}
    <ExportImport {api} />
  {/if}
{/if}

<style>
  .tabs {
    display: flex;
    flex-wrap: wrap;
    gap: var(--space-1) var(--space-4);
    border-bottom: 1px solid var(--color-border);
    margin-bottom: var(--space-4);
  }
  .tabs a {
    color: var(--color-text-muted);
    text-decoration: none;
    font-weight: 600;
    padding: var(--space-2) 0;
    border-bottom: 2px solid transparent;
    margin-bottom: -1px;
  }
  .tabs a[aria-current="page"] {
    color: var(--color-text);
    border-bottom-color: var(--color-accent);
  }
</style>
