<script lang="ts">
  // The agent of one Target (T-097). The gate refusal links here.
  // T-194 (ARCHITECTURE §19): SSH repair state and action (D-372, D-384,
  // D-385), and the one-time install command (D-387).
  import { onDestroy, tick } from "svelte";
  import type { InstallCommand, PlumbApi } from "../lib/client";
  import { liveSnapshot } from "../lib/live";
  import { go, href } from "../lib/router";
  import { can, roleNote } from "../lib/session.svelte";
  import { formatTime } from "../lib/view";
  import Badge from "../ui/Badge.svelte";
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

  // --- Repair (D-384, D-385) ---
  const REPAIR_REASON: Record<string, string> = {
    "no-line":
      "The guest has no repair line: its agent never ran with a version that writes it. Update the agent while it runs, or, without an agent, use the install command below.",
    "no-sshd": "The guest has no SSH server.",
    "root-refused":
      "The SSH server refuses root also with a forced command. Set permit_root_login of the Block ssh-hardening to forced-commands-only.",
    "no-pin": "No host key of the guest is pinned yet.",
    firewall: "The firewall of the Target does not let Plumb reach the SSH port.",
    "host-key-changed":
      "The SSH host keys of the guest changed: no pinned key is left. Check a new key on the guest (ssh-keygen -lf /etc/ssh/ssh_host_ed25519_key.pub) and accept it in the repair.",
    "host-key-new":
      "The guest has SSH host keys that are not pinned. The repair still checks the pinned key.",
  };
  const repair = $derived(target?.status.repair ?? null);
  const repairTone = { ready: "ok", off: "warn", unknown: "muted" } as const;
  const repairLabel = { ready: "ready", off: "not possible", unknown: "unknown" } as const;
  // The line holds only the previous key: not when there is no line at all.
  const oldKey = $derived(
    repair?.key_confirmed === false &&
      repair.state !== "unknown" &&
      !repair.reasons.includes("no-line"),
  );
  const agentDown = $derived(target ? target.status.agent !== "online" : false);
  let repairReason = $state("");
  let acceptKey = $state("");
  let repairError = $state<unknown>(null);
  let repairBusy = $state(false);

  async function proposeRepair(ev: SubmitEvent) {
    ev.preventDefault();
    repairBusy = true;
    repairError = null;
    try {
      const plan = await api.repairTarget(name, {
        ...(repairReason.trim() ? { reason: repairReason.trim() } : {}),
        ...(acceptKey ? { accept_host_key: acceptKey } : {}),
      });
      go({ name: "plan", id: plan.id });
    } catch (err) {
      repairError = err;
    } finally {
      repairBusy = false;
    }
  }

  // --- Install command (D-387): shown once, never kept after leaving ---
  let command = $state<InstallCommand | null>(null);
  let commandError = $state<unknown>(null);
  let commandBusy = $state(false);
  let copied = $state(false);
  async function makeCommand() {
    commandBusy = true;
    commandError = null;
    copied = false;
    try {
      command = await api.createInstallCommand(name);
      await tick();
      document.getElementById("install-command-title")?.focus();
    } catch (err) {
      commandError = err;
    } finally {
      commandBusy = false;
    }
  }
  async function copyCommand() {
    if (!command) return;
    try {
      await navigator.clipboard.writeText(command.command);
      copied = true;
    } catch {
      copied = false;
    }
  }
  async function doneCommand() {
    command = null;
    copied = false;
    await tick();
    document.getElementById("install-command-make")?.focus();
  }
  onDestroy(() => (command = null));

  $effect(() => {
    const live = liveSnapshot({
      load: () => api.getTarget(name),
      subscribe: (onEvent, onOpen) => api.events(undefined, onEvent, onOpen),
      onSnapshot: (t) => (target = t),
      onError: (e) => (error = e),
      onEvent: (e, l) => {
        if (
          e.subject === `target:${name}` ||
          e.type === "run.finished" ||
          e.type === "repair.finished"
        )
          l.reloadSoon();
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
  {#if repair}
    <section class="card" aria-labelledby="repair-title" id="repair">
      <h2 id="repair-title">Repair over SSH</h2>
      <p>
        <Badge tone={repairTone[repair.state]} label={repairLabel[repair.state]} />
        {#if repair.state === "ready"}
          When the agent dies while the guest runs, Plumb can install it again over SSH with its
          repair key{repair.port ? ` (port ${repair.port})` : ""}. Nothing else runs in the guest.
        {:else if repair.state === "unknown"}
          The agent has not reported whether a repair over SSH is possible.
        {/if}
      </p>
      {#if repair.reasons.length}
        <ul class="reasons" id="repair-reasons">
          {#each repair.reasons as r (r)}<li>{REPAIR_REASON[r] ?? r}</li>{/each}
        </ul>
      {/if}
      {#if oldKey}
        <p class="warn" id="repair-old-key">
          The repair line of this guest still holds the previous repair key. An online agent gets
          the new key at once; an offline agent gets it when it is back.
        </p>
      {/if}
      {#if repair.host_keys?.length}
        <p class="keys">
          Pinned host {repair.host_keys.length === 1 ? "key" : "keys"}:
          {#each repair.host_keys as k (k)}<code class="mono">{k}</code>{/each}
        </p>
      {/if}
      {#if repair.host_keys_seen?.length}
        <div class="banner warn" id="repair-new-key" role="note">
          <strong>The guest reports a new host key.</strong> A repair connects only to a pinned key.
          A changed key can mean another machine answers: accept it only when you know why it
          changed (for example a reinstall).
          <ul>
            {#each repair.host_keys_seen as k (k)}<li><code class="mono">{k}</code></li>{/each}
          </ul>
        </div>
      {/if}

      {#if !agentDown}
        <p class="muted" id="repair-not-needed">The agent is online: no repair is needed.</p>
      {:else if repair.state === "ready" && can("repairTarget")}
        <form onsubmit={proposeRepair} novalidate>
          <div class="field">
            <label for="repair-reason">Reason (optional)</label>
            <textarea id="repair-reason" bind:value={repairReason}></textarea>
          </div>
          {#if repair.host_keys_seen?.length}
            <div class="field">
              <label for="repair-accept">New host key to accept with this repair</label>
              <select id="repair-accept" bind:value={acceptKey}>
                <option value="">(none: connect only to the pinned key)</option>
                {#each repair.host_keys_seen as k (k)}<option value={k}>{k}</option>{/each}
              </select>
            </div>
          {/if}
          <ProblemBox error={repairError} />
          <button class="btn primary" type="submit" disabled={repairBusy}>Propose repair</button>
          <p class="muted">
            This makes a plan. Only a person can approve it; the agent must still be offline then. A
            failed repair leaves the guest as it was.
          </p>
        </form>
      {:else if repair.state === "ready"}
        <p class="muted" id="repair-role">{roleNote("plan")}</p>
      {:else}
        <p class="muted" id="repair-use-command">
          Without a repair over SSH, install the agent by hand with an install command below.
        </p>
      {/if}
    </section>
  {/if}

  {#if can("createInstallCommand")}
    <section class="card" aria-labelledby="install-command-title" id="install-command">
      <h2 id="install-command-title" tabindex="-1">Install command</h2>
      {#if command}
        <p class="banner warn" role="note">
          <strong>This command is a secret until {formatTime(command.expires_at)}.</strong> It holds a
          one-time enrollment token. Run it once as root in the guest. Plumb does not show it again.
        </p>
        <pre class="mono command" id="install-command-text">{command.command}</pre>
        <div class="row">
          <button class="btn primary" type="button" onclick={copyCommand}>Copy command</button>
          <button class="btn" type="button" onclick={doneCommand}>Done, I copied it</button>
          <span role="status" class="muted">{copied ? "Copied to the clipboard." : ""}</span>
        </div>
      {:else}
        <p>
          A command that installs the agent by hand, for a guest without a repair over SSH (an
          adopted guest, a container without SSH). It works once, within 15 minutes. The current
          agent credential ends only when the guest enrolls with it.
        </p>
        <p class="muted" id="install-command-curl">
          It needs <code>curl</code> in the guest; a stock Debian LXC has none. Install it first (<code
            >apt-get install -y curl</code
          >). Every error line starts with <code>plumb-install:</code>; without curl the command
          stops with one such line, installs nothing and leaves no file.
        </p>
        <ProblemBox error={commandError} />
        <button
          class="btn"
          type="button"
          id="install-command-make"
          disabled={commandBusy}
          onclick={makeCommand}>Make an install command</button
        >
      {/if}
    </section>
  {/if}
{:else}
  <!-- The page title is the focus target of a route change, also on a load error. -->
  <h1 id="page-title" tabindex="-1">Agent of <span class="mono">{name}</span></h1>
  <ProblemBox {error} />
  {#if !error}<p class="muted" aria-live="polite">Loading…</p>{/if}
{/if}

<style>
  .reasons {
    margin: 0 0 var(--space-2);
    padding-left: var(--space-6);
  }
  .keys code {
    display: block;
    overflow-wrap: anywhere;
  }
  .command {
    white-space: pre-wrap;
    overflow-wrap: anywhere;
    background: var(--color-muted-bg);
    padding: var(--space-2) var(--space-3);
    border-radius: var(--radius);
    max-height: 16rem;
    overflow: auto;
  }
  ul {
    margin: 0 0 var(--space-2);
    padding-left: var(--space-6);
  }
  .warn {
    color: var(--color-warn);
  }
</style>
