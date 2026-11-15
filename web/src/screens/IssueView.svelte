<script lang="ts">
  import { tick } from "svelte";
  import type { PlumbApi } from "../lib/client";
  import { can, canTakeRisk, me, roleNote } from "../lib/session.svelte";
  import { offersRaw } from "../lib/raw";
  import { go, href } from "../lib/router";
  import type { Issue, IssueAction } from "../lib/types";
  import {
    ADDRESS_HINT,
    GUEST_ADDRESS,
    GUEST_CONFIG,
    GUEST_RESTORE_FAILED,
    addressIssueText,
    formatTime,
    isSensitive,
  } from "../lib/view";
  import ChangeTable from "../ui/ChangeTable.svelte";
  import ProblemBox from "../ui/ProblemBox.svelte";
  import RiskBadge from "../ui/RiskBadge.svelte";

  let { api, id }: { api: PlumbApi; id: number } = $props();
  /** The attribute of a drift of NIC 1 … 3 (T-219): exactly "nic1" … "nic3", one per NIC. */
  const NIC_ATTR = /^(nic[1-3])$/;
  let issue = $state<Issue | null>(null);
  let error = $state<unknown>(null);
  let actionError = $state<unknown>(null);
  let reason = $state("");
  let busy = $state(false);
  let ignored = $state(false);
  let allTargets = $state(false);
  let onlyChanged = $state(false);
  let comment = $state("");
  let commentMissing = $state(false);
  let commentBusy = $state(false);
  let commentError = $state<unknown>(null);
  let commentAdded = $state("");

  async function addComment(e: SubmitEvent) {
    e.preventDefault();
    commentAdded = "";
    commentMissing = !comment.trim();
    if (commentMissing) {
      document.getElementById("comment-body")?.focus();
      return;
    }
    commentBusy = true;
    commentError = null;
    try {
      const c = await api.commentIssue(id, comment.trim());
      if (issue) issue.comments = [...issue.comments, c];
      comment = "";
      commentAdded = "Comment added.";
    } catch (err) {
      commentError = err;
    } finally {
      commentBusy = false;
    }
  }

  $effect(() => {
    api.getIssue(id).then(
      (i) => (issue = i),
      (e: unknown) => (error = e),
    );
  });

  const hidden = $derived(issue?.changes.some(isSensitive) ?? false);
  // D-418: a drift of NIC 1 … 3 has no Accept (issue.AcceptRefused); say why.
  const nicDrift = $derived(
    issue?.resource === GUEST_CONFIG
      ? [...new Set(issue.changes.map((c) => NIC_ATTR.exec(c.attr)?.[1]).filter((n) => n))]
      : [],
  );
  const source = $derived(
    issue?.resource === "guest:config" ? "Proxmox" : "the agent in the guest",
  );

  // D-420: a failed restore; the Issue names the backup to restore (ignore only).
  const wayBack = $derived(
    issue?.resource === GUEST_RESTORE_FAILED ? (issue.restore_way_back ?? null) : null,
  );
  const restoreFailed = $derived(issue?.resource === GUEST_RESTORE_FAILED);
  // D-439: the restore worked, but the re-enroll Plan was refused; only a
  // re-enroll Plan of the Target passes the gate.
  const restoredRefused = $derived(
    restoreFailed &&
      issue?.status === "open" &&
      issue.changes.some(
        (c) =>
          c.attr === "restore" &&
          "from" in c &&
          typeof c.from === "string" &&
          c.from.startsWith("restored; the agent is not enrolled:"),
      ),
  );
  let reenrollBusy = $state(false);
  let reenrollError = $state<unknown>(null);
  async function reenroll() {
    if (!issue) return;
    reenrollBusy = true;
    reenrollError = null;
    try {
      const plan = await api.reenrollTarget(
        issue.target,
        `re-enroll after the restore (Issue ${issue.id})`,
      );
      go({ name: "plan", id: plan.id });
    } catch (err) {
      reenrollError = err;
    } finally {
      reenrollBusy = false;
    }
  }
  // T-234: while the last restore of the Target failed, its other Issues have no action.
  const blockedBy = $derived(issue?.status === "open" ? (issue.actions_blocked_by ?? null) : null);
  // M1 of #352: only a human admin session ends guest:restore-failed.
  const adminPerson = $derived(
    (me.session?.principal.startsWith("user:") ?? false) && me.session?.tier === "admin",
  );

  // guest:address (D-215): the Proxmox config is right, the guest does not follow it.
  const isAddress = $derived(issue?.resource === GUEST_ADDRESS);
  const canPlan = $derived(
    issue?.actions.includes("revert") || (issue?.actions.includes("accept") && !hidden),
  );

  // D-296: Accept on a Block resource makes it a raw resource of the Target.
  // `raw` and `source` come from the server (D-301); an older one sends neither.
  const raw = $derived(issue ? (issue.raw ?? offersRaw(issue.resource)) : false);
  // D-362: Accept into a Block parameter when the drift maps back to one.
  const params = $derived(issue?.accept_mode === "param" ? (issue.accept_params ?? []) : []);
  const paramUses = $derived([...new Set(params.map((p) => p.use))]);
  const acceptLabel = $derived(
    params.length
      ? `Accept into ${params.length === 1 ? "parameter" : "parameters"} ${params
          .map((p) => p.param)
          .join(", ")} of ${paramUses.join(", ")}`
      : raw
        ? "Accept as raw for this Target"
        : "Accept: keep the guest value",
  );

  const changedAttrs = $derived([...new Set(issue?.changes.map((c) => c.attr) ?? [])]);
  const canIgnore = $derived(
    issue?.actions.includes("ignore_once") || issue?.actions.includes("ignore_rule"),
  );

  let confirmIgnore = $state(false);
  async function askIgnore() {
    confirmIgnore = true;
    await tick();
    document.getElementById("ignore-confirm")?.focus();
  }

  async function ignoreOnce() {
    confirmIgnore = false;
    busy = true;
    actionError = null;
    try {
      issue = await api.ignoreOnce(id, reason || undefined);
      ignored = true;
    } catch (err) {
      actionError = err;
    } finally {
      busy = false;
    }
  }

  async function ignoreRule() {
    busy = true;
    actionError = null;
    try {
      const plan = await api.resolveIssue(id, "ignore_rule", reason || undefined, {
        all_targets: allTargets,
        ...(onlyChanged && changedAttrs.length ? { attrs: changedAttrs } : {}),
      });
      go({ name: "plan", id: plan.id });
    } catch (err) {
      actionError = err;
    } finally {
      busy = false;
    }
  }

  async function resolve(action: Exclude<IssueAction, "ignore_once" | "ignore_rule">) {
    busy = true;
    actionError = null;
    try {
      const plan = await api.resolveIssue(id, action, reason || undefined);
      go({ name: "plan", id: plan.id });
    } catch (err) {
      actionError = err;
    } finally {
      busy = false;
    }
  }
</script>

<h1 id="page-title" tabindex="-1">Issue {id}</h1>
<ProblemBox {error} />
{#if issue}
  <div class="card">
    {#if issue.resource === "guest:missing"}
      <p class="hint-box" id="restore-hint">
        After a restore: the guest of this Target is gone from Proxmox (it was deleted after the
        backup). Plumb never makes it again by itself. Delete the Target, or delete it and add it
        again by a Plan.
      </p>
    {:else if issue.resource === GUEST_RESTORE_FAILED}
      <p class="hint-box" id="restore-hint">
        A restore of this guest failed after Proxmox began. Proxmox removes the guest's disks before
        it writes the backup, so the guest may have no disk now.
        {#if wayBack}The way back: restore the safety backup of {formatTime(wayBack.time)} (backup
          <span class="mono">{wayBack.backup_id}</span>), or another backup.{:else}The way back:
          restore another backup.{/if}
        A restore that works ends this issue. Re-enroll the agent only after a restore that worked.
      </p>
      {#if issue.status === "open"}
        <p id="restore-way-back">
          {#if wayBack && can("restoreGuest")}
            <a
              href={href({
                name: "target-backups",
                target: issue.target,
                restore: wayBack.backup_id,
              })}>Restore the safety backup of {formatTime(wayBack.time)}…</a
            > ·
          {/if}
          <a href={href({ name: "target-backups", target: issue.target })}
            >Backups of {issue.target}</a
          >
        </p>
      {/if}
    {:else if issue.resource === "agent:silent"}
      <p class="hint-box" id="restore-hint">
        After a restore: the guest runs, but its agent did not say hello within 10 minutes. It may
        have enrolled again after the backup, so Plumb does not know its credential.
        <a href={href({ name: "target-edit", target: issue.target })}>Re-enroll the agent</a> on the Target
        page.
      </p>
    {/if}
    {#if isAddress}
      <p id="address-text">{addressIssueText(issue)}</p>
      <p>
        <span class="mono">{issue.resource}</span> on <span class="mono">{issue.target}</span>.
        Plumb found it at {formatTime(issue.opened_at)} in the address list that the agent in the guest
        reports.
      </p>
      <p class="hint-box" id="address-hint">{ADDRESS_HINT}</p>
      <p>
        Plumb cannot revert or accept this: the address is set only when the guest is created. ·
        Status: {issue.status}
      </p>
    {:else if issue.resource === GUEST_RESTORE_FAILED}
      <p id="restore-failed-found">
        <span class="mono">{issue.resource}</span> on <span class="mono">{issue.target}</span>.
        Plumb opened it at {formatTime(issue.opened_at)}, when the restore failed. · Status: {issue.status}
      </p>
    {:else}
      <p>
        <span class="mono">{issue.resource}</span> on <span class="mono">{issue.target}</span>
        differs from the desired state. Found by {source} at {formatTime(issue.opened_at)}.
      </p>
      <p>Risk of a revert: <RiskBadge risk={issue.risk} /> · Status: {issue.status}</p>
    {/if}
    {#if ignored}
      <p class="banner ok" role="status">
        {#if restoreFailed}
          Ended. Plumb compares the guest again, and the actions of the other issues of this Target
          come back.
        {:else}
          Ignored once. The same drift opens no new issue until the resource matches again or a run
          applies it.
        {/if}
      </p>
    {/if}
    {#if issue.resolution}
      <p>
        Resolved: <strong
          >{issue.resolution.action === "ignore" ? "ignored" : issue.resolution.action}</strong
        >
        by
        <span class="mono">{issue.resolution.by}</span> at {formatTime(issue.resolution.at)}.
      </p>
    {/if}
    {#if issue.plan_id}
      <p>Plan: <a href={href({ name: "plan", id: issue.plan_id })}>Plan {issue.plan_id}</a></p>
    {/if}
  </div>

  <h2>Difference</h2>
  <ChangeTable
    changes={issue.changes}
    caption="Difference of {issue.resource}"
    resource={issue.resource}
    fromLabel={isAddress
      ? "Reported by the guest"
      : issue.status === "open"
        ? "On the guest now"
        : "On the guest when found"}
    toLabel={isAddress ? "Configured" : "Desired"}
  />
  {#if hidden}
    <p class="muted">
      A secret value changed. Plumb never shows it, and "Accept" is not offered for it.
    </p>
  {/if}

  {#if issue.status === "open" && blockedBy === null && nicDrift.length && !issue.actions.includes("accept")}
    <p class="muted" id="accept-nic">
      Accept is not possible for a network card ({nicDrift.join(", ")}). Revert it, or change
      <span class="mono">guest.nics</span> in the
      <a href={href({ name: "target-edit", target: issue.target })}>Target {issue.target}</a>.
    </p>
  {/if}

  {#if restoredRefused}
    <section class="card" aria-labelledby="restored-title" id="restored-refused">
      <h2 id="restored-title">Restored, the agent is not enrolled</h2>
      <p>{issue.actions_note}</p>
      <ProblemBox error={reenrollError} />
      {#if can("reenrollTarget")}
        <button type="button" id="restored-reenroll" disabled={reenrollBusy} onclick={reenroll}>
          Propose the re-enroll of {issue.target}
        </button>
      {:else}
        <p class="muted">{roleNote("plan")} You cannot propose the re-enroll.</p>
      {/if}
    </section>
  {/if}

  {#if blockedBy !== null}
    <p class="banner warn" role="note" id="actions-blocked">
      {issue.actions_note}
      <a href={href({ name: "issue", id: blockedBy })}>Open Issue {blockedBy}</a>
    </p>
  {:else if issue.status === "open" && !can("resolveIssue")}
    <p class="muted" id="resolve-role">{roleNote("plan")} You cannot resolve this issue.</p>
  {:else if issue.status === "open"}
    <section class="card" aria-labelledby="resolve-title">
      <h2 id="resolve-title">Resolve</h2>
      <div class="field">
        <label for="resolve-reason">Reason (optional)</label>
        <textarea id="resolve-reason" bind:value={reason}></textarea>
      </div>
      <ProblemBox error={actionError} />
      {#if canPlan}<div class="row">
          {#if issue.actions.includes("revert")}
            <button
              class="btn primary"
              type="button"
              disabled={busy}
              onclick={() => resolve("revert")}
            >
              Revert: apply the desired state
            </button>
          {/if}
          {#if issue.actions.includes("accept") && !hidden}
            <button class="btn" type="button" disabled={busy} onclick={() => resolve("accept")}>
              {acceptLabel}
            </button>
          {/if}
        </div>
        {#if params.length && issue.actions.includes("accept") && !hidden}<p
            class="muted"
            id="param-help"
          >
            Accept writes the guest value into the {params.length === 1
              ? "parameter"
              : "parameters"}
            {#each params as p, i (i)}{i ? ", " : ""}<span class="mono">{p.param}</span>{/each} of this
            Target's use of the Block {#each paramUses as u, i (u)}{i ? ", " : ""}<span class="mono"
                >{u}</span
              >{/each}. The Block itself does not change, and the resource keeps following it.
          </p>{/if}
        {#if raw && issue.actions.includes("accept") && !hidden}<p class="muted" id="raw-help">
            Accept as raw keeps the guest value for this Target only.
            {#if issue.source}The Block <span class="mono">{issue.source.block}</span>
              {#if issue.source.use !== issue.source.block}(use
                <span class="mono">{issue.source.use}</span>){/if} does not set these attributes on it
              any more.{:else}The Block does not set these attributes on it any more.{/if} The Target
            page lists it under "Raw resources", where you can edit it or go back to the Block.
            {#if issue.accept_reason}<br /><span id="raw-reason"
                >No Block parameter fits: {issue.accept_reason}</span
              >{/if}
          </p>{/if}
        <p class="muted">
          {#if issue.actions.includes("accept") && !hidden}Both make a plan.{:else}This makes a
            plan.{/if}
          Nothing changes until you approve it.
        </p>{/if}

      {#if canIgnore}
        <h3>Ignore</h3>
        {#if restoreFailed && issue.actions.includes("ignore_once") && !adminPerson}
          <p class="muted" id="ignore-admin-only">
            Only a person with the role admin can end this issue (an API token never can).
          </p>
        {:else if issue.actions.includes("ignore_once") && canTakeRisk(issue.risk)}
          <div class="option">
            {#if confirmIgnore}
              <div class="confirm" role="group" aria-labelledby="ignore-q">
                {#if restoreFailed}
                  <p id="ignore-q">
                    <strong>End the issue of the failed restore?</strong>
                    {issue.actions_note}
                  </p>
                {:else}<p id="ignore-q">
                    <strong>Ignore this drift?</strong> The issue is resolved at once and no screen can
                    undo it. The same drift opens no new issue until the resource matches again or a run
                    applies it.
                  </p>{/if}
                <div class="row">
                  <button
                    id="ignore-confirm"
                    class="btn"
                    type="button"
                    disabled={busy}
                    onclick={ignoreOnce}
                    >{restoreFailed ? "End the issue" : "Ignore this drift"}</button
                  >
                  <button class="btn" type="button" onclick={() => (confirmIgnore = false)}
                    >Cancel</button
                  >
                </div>
              </div>
            {:else}
              <button class="btn" type="button" disabled={busy} onclick={askIgnore}>
                Ignore once
              </button>
              <p class="muted">
                {#if restoreFailed}
                  Ends this issue now, without a plan. Plumb refuses it while the guest has no disk.
                {:else if isAddress}
                  Resolves this issue now, without a plan. When the reported list changes and the
                  configured address is still missing, the issue opens again.
                {:else}
                  Resolves this issue now, without a plan. The guest keeps its value until the next
                  apply. A new, different drift opens a new issue.
                {/if}
              </p>
            {/if}
          </div>
        {/if}
        {#if issue.actions.includes("ignore_rule")}
          <fieldset class="option">
            <legend>Ignore by rule</legend>
            <div class="choices" role="radiogroup" aria-label="Targets of the rule">
              <label
                ><input type="radio" name="rule-scope" value={false} bind:group={allTargets} />
                Only target <span class="mono">{issue.target}</span></label
              >
              <label
                ><input type="radio" name="rule-scope" value={true} bind:group={allTargets} />
                All targets, also future ones</label
              >
            </div>
            <div class="choices" role="radiogroup" aria-label="Attributes of the rule">
              <label
                ><input type="radio" name="rule-attrs" value={false} bind:group={onlyChanged} />
                All attributes of <span class="mono">{issue.resource}</span></label
              >
              <label
                ><input type="radio" name="rule-attrs" value={true} bind:group={onlyChanged} />
                Only the changed attributes:
                <span class="mono">{changedAttrs.join(", ")}</span></label
              >
            </div>
            <button class="btn" type="button" disabled={busy} onclick={ignoreRule}>
              Make ignore-rule plan
            </button>
            <p class="muted">
              The rule is a document of the desired state. Only a person can approve the plan.
              {#if isAddress}While the rule exists, Plumb opens no address issue here.{:else}The
                next apply still writes the desired state.{/if}
            </p>
          </fieldset>
        {/if}
      {/if}
    </section>
  {/if}

  <h2 id="comments-title">Comments</h2>
  {#each issue.comments as c (c.id)}
    <div class="card">
      <p class="muted"><span class="mono">{c.author}</span> · {formatTime(c.created_at)}</p>
      <p class="body">{c.body}</p>
    </div>
  {:else}
    <p class="muted">No comments yet.</p>
  {/each}
  {#if can("commentIssue")}<form
      class="card"
      onsubmit={addComment}
      novalidate
      aria-labelledby="comments-title"
    >
      <div class="field">
        <label for="comment-body">Add a comment</label>
        <textarea
          id="comment-body"
          bind:value={comment}
          maxlength="10000"
          aria-invalid={commentMissing}
          aria-describedby={commentMissing ? "comment-error comment-hint" : "comment-hint"}
          oninput={() => (commentMissing = false)}></textarea>
        {#if commentMissing}<p id="comment-error" class="error">Write a comment first.</p>{/if}
        <p id="comment-hint" class="muted hint">
          Plain text. Everyone who can read this issue sees it, also API tokens.
        </p>
      </div>
      <ProblemBox error={commentError} />
      <div class="row">
        <button class="btn" type="submit" disabled={commentBusy}>Add comment</button>
        {#if commentAdded}<span role="status" class="muted">{commentAdded}</span>{/if}
      </div>
    </form>{/if}
{:else if !error}
  <p class="muted" aria-live="polite">Loading…</p>
{/if}

<style>
  h3 {
    font-size: var(--text-md);
    margin: var(--space-4) 0 var(--space-2);
  }
  .option {
    margin-bottom: var(--space-3);
  }
  .option p {
    margin: var(--space-1) 0 0;
  }
  .confirm p {
    margin: 0 0 var(--space-2);
  }
  .choices {
    display: flex;
    flex-direction: column;
    gap: var(--space-1);
    margin-bottom: var(--space-3);
  }
  .choices label {
    display: flex;
    gap: var(--space-2);
    align-items: baseline;
    flex-wrap: wrap;
  }
  .hint-box {
    border-left: 4px solid var(--color-info);
    padding-left: var(--space-2);
  }
  .body {
    white-space: pre-wrap;
    margin: 0;
  }
  .error {
    margin: var(--space-1) 0 0;
    font-size: var(--text-sm);
    color: var(--color-danger);
  }
  .hint {
    margin: var(--space-1) 0 0;
    font-size: var(--text-sm);
  }
</style>
