<script lang="ts">
  import { agentState } from "../lib/agent";
  import type { PlumbApi } from "../lib/client";
  import { can } from "../lib/session.svelte";
  import { liveSnapshot } from "../lib/live";
  import { href } from "../lib/router";
  import type { TargetView } from "../lib/types";
  import {
    REPORTED_MARK,
    REPORTED_TITLE,
    ageText,
    formatTime,
    reportedAddrs,
    reportedText,
    targetState,
    waitingSince,
  } from "../lib/view";
  import Badge from "../ui/Badge.svelte";
  import ProblemBox from "../ui/ProblemBox.svelte";

  let { api }: { api: PlumbApi } = $props();
  let targets = $state<TargetView[] | null>(null);
  let error = $state<unknown>(null);

  $effect(() => {
    // The list follows the event stream (live.ts): reload on every
    // (re)open and, coalesced, on status, run and issue events.
    const live = liveSnapshot({
      load: () => api.listTargets(),
      subscribe: (onEvent, onOpen) => api.events(undefined, onEvent, onOpen),
      onSnapshot: (t) =>
        (targets = t.sort((a, b) => a.document.name.localeCompare(b.document.name))),
      onError: (e) => (error = e),
      onEvent: (e, l) => {
        if (
          e.type === "target.status" ||
          e.type === "target.addrs" ||
          e.type === "run.finished" ||
          e.type.startsWith("issue.")
        )
          l.reloadSoon();
      },
    });
    return () => live.stop();
  });
</script>

<div class="spread">
  <h1 id="page-title" tabindex="-1">Targets</h1>
  <div class="row">
    {#if can("adoptTarget")}<a class="btn" href={href({ name: "target-adopt" })}>Adopt a guest</a
      >{/if}
    {#if can("createPlan")}<a class="btn primary" href={href({ name: "target-new" })}>New target</a
      >{/if}
  </div>
</div>
<ProblemBox {error} />
{#if targets === null && !error}
  <p class="muted" aria-live="polite">Loading…</p>
{:else if targets && targets.length === 0}
  <p class="muted">No targets yet. Create the first one.</p>
{:else if targets}
  <!-- svelte-ignore a11y_no_noninteractive_tabindex -->
  <div
    class="table-wrap card flush"
    tabindex="0"
    role="region"
    aria-label="Targets and their status, scrolls sideways"
  >
    <table>
      <caption class="sr-only">Targets and their status</caption>
      <thead>
        <tr>
          <th scope="col">Name</th>
          <th scope="col">Status</th>
          <th scope="col">Image</th>
          <th scope="col">Size</th>
          <th scope="col">Address</th>
          <th scope="col">Last seen</th>
        </tr>
      </thead>
      <tbody>
        {#each targets as t (t.document.name)}
          {@const st = targetState(t)}
          {@const g = t.document.spec.guest}
          {@const since = waitingSince(t.status.waiting ?? [])}
          {@const reported = reportedAddrs(t.status)}
          {@const agent = agentState(t.status)}
          <tr>
            <th scope="row">
              <a class="mono" href={href({ name: "target-edit", target: t.document.name })}
                >{t.document.name}</a
              >
            </th>
            <td
              ><Badge tone={st.tone} label={st.label} title={st.detail} /><span class="sr-only"
                >: {st.detail}</span
              >{#if since}{#if t.status.phase !== "restart_needed"}
                  <span class="extra"
                    ><Badge
                      tone="warn"
                      label="Restart needed"
                      title="Values set in Proxmox wait for a stop and start of the VM."
                    /></span
                  >{/if}<span class="ignored" title={formatTime(since)}
                  >waiting for {ageText(since)}</span
                >{/if}{#if t.status.open_issues > 0}<a
                  class="ignored"
                  href={href({ name: "issues", target: t.document.name })}
                  >{t.status.open_issues} open issue{t.status.open_issues === 1 ? "" : "s"}</a
                >{/if}{#if t.status.ignored_drift?.length}<span class="ignored"
                  >{t.status.ignored_drift.length} ignored by rule</span
                >{/if}{#if t.status.agent_version || agent?.label}<span class="ignored agent"
                  >agent <span class="mono">{t.status.agent_version ?? "?"}</span>{#if agent?.label}
                    <a
                      href={href({ name: "target-agent", target: t.document.name })}
                      aria-label="Agent of {t.document.name}: {agent.label}"
                      ><Badge tone={agent.tone} label={agent.label} /></a
                    >{/if}</span
                >{/if}{#if t.status.agent === "offline"}<a
                  class="ignored"
                  id="repair-mark-{t.document.name}"
                  href={href({ name: "target-agent", target: t.document.name })}
                  >agent offline{t.status.repair?.state === "ready" ? " · repair possible" : ""}</a
                >{:else if t.status.repair?.key_confirmed === false && t.status.repair.state !== "unknown" && !t.status.repair.reasons.includes("no-line")}<a
                  class="ignored"
                  id="repair-mark-{t.document.name}"
                  href={href({ name: "target-agent", target: t.document.name })}>old repair key</a
                >{/if}</td
            >
            <td class="mono"
              >{#if "adopted" in t.document.spec}<span class="muted">adopted</span>{:else}{t
                  .document.spec.image}{/if}{#if t.status.image_version}
                v{t.status.image_version}{/if}</td
            >
            <td class="nowrap">{g.cores} CPU · {g.memory / 1024} GiB · {g.disk} GiB</td>
            <td
              ><span class="sr-only">Configured:&nbsp;</span><span class="mono"
                >{t.status.ips?.join(", ") || ((g.ip ?? "dhcp") === "dhcp" ? "DHCP" : "–")}</span
              >{#if reported}<span class="reported" title={REPORTED_TITLE}
                  ><span class="mono">{reportedText(reported)}</span>
                  <span class="mark">{REPORTED_MARK}</span></span
                >{/if}</td
            >
            <td class="nowrap">{formatTime(t.status.last_seen)}</td>
          </tr>
        {/each}
      </tbody>
    </table>
  </div>
{/if}

<style>
  .ignored {
    display: block;
    font-size: var(--text-xs);
    color: var(--color-text-muted);
  }
  .reported {
    display: block;
    font-size: var(--text-xs);
    color: var(--color-text-muted);
    overflow-wrap: anywhere;
  }
  .mark {
    font-style: italic;
  }
  .extra {
    margin-left: var(--space-1);
  }
  .flush {
    padding: 0;
    overflow: hidden;
  }
</style>
