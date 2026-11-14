<script lang="ts">
  // Users and roles (T-154; D-329, D-330, D-343). Admin session only. There
  // is no delete: a disabled user keeps its name. A one-time password is
  // shown once and lives only in this component; it is gone when the page
  // or the tab changes.
  import { tick } from "svelte";
  import { ApiError } from "../lib/api";
  import type { PlumbApi, User, UserRole } from "../lib/client";
  import { pointerErrors } from "../lib/formErrors";
  import { liveSnapshot } from "../lib/live";
  import type { Session } from "../lib/types";
  import { formatTime } from "../lib/view";
  import Badge from "../ui/Badge.svelte";
  import ProblemBox from "../ui/ProblemBox.svelte";

  let { api, session }: { api: PlumbApi; session: Session } = $props();

  const ROLES: UserRole[] = ["read", "plan", "apply", "admin"];
  const roleText: Record<UserRole, string> = {
    read: "Sees everything. Changes nothing.",
    plan: "Also proposes changes (plans), comments on issues, drops plans.",
    apply: "Also approves plans up to the risk disruptive, and ignores drift once.",
    admin: "Everything: destructive plans, users, API tokens, secrets, backups, import.",
  };

  let users = $state<User[] | null>(null);
  let error = $state<unknown>(null);

  $effect(() => {
    const live = liveSnapshot({
      load: () => api.listUsers(),
      subscribe: (onEvent, onOpen) => api.events(undefined, onEvent, onOpen),
      onSnapshot: (u) => (users = u),
      onError: (e) => (error = e),
      onEvent: (e, l) => {
        if (e.type.startsWith("user.")) l.reloadSoon();
      },
    });
    return () => live.stop();
  });

  const own = (u: User) => session.principal === `user:${u.name}`;

  // --- create ---
  let name = $state("");
  let role = $state<UserRole>("read");
  let createBusy = $state(false);
  let createError = $state<unknown>(null);
  const createErrs = $derived(pointerErrors(createError));
  let nameMissing = $state(false);

  async function create(e: SubmitEvent) {
    e.preventDefault();
    createError = null;
    nameMissing = !name.trim();
    if (nameMissing) {
      document.getElementById("user-name")?.focus();
      return;
    }
    createBusy = true;
    try {
      const otp = await api.createUser(name.trim(), role);
      await show(otp.username, otp.password, "created");
      name = "";
      role = "read";
    } catch (err) {
      createError = err;
      if (err instanceof ApiError && err.problem.status !== 409)
        document.getElementById("user-name")?.focus();
    } finally {
      createBusy = false;
    }
  }

  // --- the one-time password, shown once ---
  let shown = $state<{ username: string; password: string; why: "created" | "reset" } | null>(null);
  let copied = $state(false);

  async function show(username: string, password: string, why: "created" | "reset") {
    shown = { username, password, why };
    copied = false;
    await tick();
    document.getElementById("otp-title")?.focus();
  }

  $effect(() => {
    if (!shown) return;
    const warn = (e: BeforeUnloadEvent) => e.preventDefault();
    window.addEventListener("beforeunload", warn);
    return () => window.removeEventListener("beforeunload", warn);
  });

  async function copy() {
    if (!shown) return;
    try {
      await navigator.clipboard.writeText(shown.password);
      copied = true;
    } catch {
      const el = document.getElementById("otp-value");
      const range = document.createRange();
      if (el) range.selectNodeContents(el);
      window.getSelection()?.removeAllRanges();
      window.getSelection()?.addRange(range);
    }
  }

  async function done() {
    shown = null;
    copied = false;
    await tick();
    document.getElementById("users-title")?.focus();
  }

  // --- row actions: role, disable or enable, one-time password ---
  type Action =
    | { kind: "role"; user: string; role: UserRole }
    | { kind: "disable" | "enable" | "password"; user: string };
  let action = $state<Action | null>(null);
  let rowBusy = $state(false);
  let rowError = $state<{ user: string; error: unknown } | null>(null);

  async function open(a: Action) {
    action = a;
    rowError = null;
    await tick();
    document.getElementById(`act-${a.user}`)?.focus();
  }

  async function cancel() {
    const user = action?.user;
    action = null;
    await tick();
    if (user) document.getElementById(`row-${user}`)?.focus();
  }

  async function run() {
    const a = action;
    if (!a) return;
    rowBusy = true;
    rowError = null;
    try {
      if (a.kind === "password") {
        const otp = await api.resetUserPassword(a.user);
        action = null;
        await show(otp.username, otp.password, "reset");
        return;
      }
      const change = a.kind === "role" ? { role: a.role } : { disabled: a.kind === "disable" };
      const after = await api.changeUser(a.user, change);
      if (users) users = users.map((u) => (u.name === after.name ? after : u));
      action = null;
      await tick();
      document.getElementById(`row-${a.user}`)?.focus();
    } catch (err) {
      rowError = { user: a.user, error: err };
    } finally {
      rowBusy = false;
    }
  }

  function stateOf(u: User): { label: string; tone: "ok" | "warn" | "danger" | "muted" }[] {
    const out: { label: string; tone: "ok" | "warn" | "danger" | "muted" }[] = [];
    if (u.disabled) out.push({ label: "disabled", tone: "muted" });
    if (u.locked) out.push({ label: "locked by a restore", tone: "danger" });
    if (u.must_change_password && !u.disabled)
      out.push({ label: "must change password", tone: "warn" });
    if (!out.length) out.push({ label: "active", tone: "ok" });
    return out;
  }

  const question = (a: Action) =>
    a.kind === "role"
      ? `Change the role of "${a.user}" to ${a.role}?`
      : a.kind === "disable"
        ? `Disable "${a.user}"?`
        : a.kind === "enable"
          ? `Enable "${a.user}" again?`
          : `Give "${a.user}" a one-time password?`;
  const consequence = (a: Action, u: User) =>
    a.kind === "role"
      ? `Every session of ${a.user} ends.${u.role === "admin" && a.role !== "admin" ? " The API tokens it made are disabled until an admin enables them." : ""}`
      : a.kind === "disable"
        ? `${a.user} cannot log in, every session ends, and the API tokens it made are disabled. The name stays taken.`
        : a.kind === "enable"
          ? `${a.user} can log in again with the old password. Its tokens stay disabled until an admin enables them.`
          : "The old password stops working, and every session of this user ends.";
</script>

<section aria-labelledby="users-title">
  <h2 id="users-title" tabindex="-1">Users</h2>
  <p class="muted">
    Each person has a role. Plumb never deletes a user: disable it instead. Its name is never given
    again.
  </p>

  {#if shown}
    <div class="shown" role="group" aria-labelledby="otp-title">
      <h3 id="otp-title" tabindex="-1">
        {shown.why === "created" ? "User" : "One-time password for"} "{shown.username}"{shown.why ===
        "created"
          ? " is ready"
          : ""}
      </h3>
      <p>
        <strong>You cannot see this password again.</strong> Give it to {shown.username} in a safe way.
        At the first login, {shown.username} must choose a new password.
      </p>
      <pre class="mono value" id="otp-value">{shown.password}</pre>
      <div class="row">
        <button class="btn primary" type="button" onclick={copy}>Copy password</button>
        <button class="btn" type="button" onclick={done}>Done, I copied it</button>
        <span role="status" class="muted">{copied ? "Copied to the clipboard." : ""}</span>
      </div>
    </div>
  {/if}

  <ProblemBox {error} />
  {#if users === null && !error}
    <p class="muted" aria-live="polite">Loading…</p>
  {:else if users}
    <!-- svelte-ignore a11y_no_noninteractive_tabindex -->
    <div
      class="table-wrap card flush"
      tabindex="0"
      role="region"
      aria-label="Users, scrolls sideways"
    >
      <table class="stack">
        <caption class="sr-only">Users</caption>
        <thead>
          <tr>
            <th scope="col">User</th>
            <th scope="col">Role</th>
            <th scope="col">State</th>
            <th scope="col">Last login</th>
            <th scope="col">Made by</th>
            <th scope="col">Action</th>
          </tr>
        </thead>
        <tbody>
          {#each users as u (u.name)}
            <tr>
              <th scope="row" class="mono" id="row-{u.name}" tabindex="-1"
                >{u.name}{#if own(u)}<span class="you">&nbsp;(you)</span>{/if}</th
              >
              <td data-label="Role"><span><Badge tone="info" label={u.role} /></span></td>
              <td data-label="State"
                ><span class="states"
                  >{#each stateOf(u) as st (st.label)}<Badge
                      tone={st.tone}
                      label={st.label}
                    />{/each}</span
                ></td
              >
              <td data-label="Last login" class="nowrap">{formatTime(u.last_login_at)}</td>
              <td data-label="Made by" class="mono">{u.created_by}</td>
              <td data-label="Action">
                {#if own(u)}
                  <span class="muted">Change your own password in your session.</span>
                {:else if action?.user === u.name}
                  {@const a = action}
                  <div class="confirm" role="group" aria-labelledby="ask-{u.name}">
                    <p id="ask-{u.name}"><strong>{question(a)}</strong></p>
                    {#if a.kind === "role"}
                      <label for="act-{u.name}">New role</label>
                      <select
                        id="act-{u.name}"
                        value={a.role}
                        onchange={(e) =>
                          (action = {
                            kind: "role",
                            user: u.name,
                            role: (e.currentTarget as HTMLSelectElement).value as UserRole,
                          })}
                      >
                        {#each ROLES as r (r)}<option value={r}>{r}</option>{/each}
                      </select>
                      <p class="muted small">{roleText[a.role]}</p>
                    {/if}
                    <p class="small">{consequence(a, u)}</p>
                    <div class="row">
                      <button
                        class="btn {a.kind === 'enable' ? 'primary' : 'danger'}"
                        type="button"
                        id={a.kind === "role" ? undefined : `act-${u.name}`}
                        disabled={rowBusy || (a.kind === "role" && a.role === u.role)}
                        onclick={run}
                        >{a.kind === "role"
                          ? "Change role"
                          : a.kind === "disable"
                            ? "Disable"
                            : a.kind === "enable"
                              ? "Enable"
                              : "Make a one-time password"}</button
                      >
                      <button class="btn" type="button" onclick={cancel}>Cancel</button>
                    </div>
                  </div>
                {:else}
                  <div class="row actions">
                    <button
                      class="btn"
                      type="button"
                      aria-label="Change the role of {u.name}"
                      disabled={action !== null}
                      onclick={() => open({ kind: "role", user: u.name, role: u.role })}
                      >Role…</button
                    >
                    {#if u.disabled}
                      <button
                        class="btn"
                        type="button"
                        aria-label="Enable {u.name}"
                        disabled={action !== null}
                        onclick={() => open({ kind: "enable", user: u.name })}>Enable…</button
                      >
                    {:else}
                      <button
                        class="btn"
                        type="button"
                        aria-label="Disable {u.name}"
                        disabled={action !== null}
                        onclick={() => open({ kind: "disable", user: u.name })}>Disable…</button
                      >
                      <button
                        class="btn"
                        type="button"
                        aria-label="Give {u.name} a one-time password"
                        disabled={action !== null}
                        onclick={() => open({ kind: "password", user: u.name })}
                        >One-time password…</button
                      >
                    {/if}
                  </div>
                {/if}
                {#if rowError?.user === u.name}<ProblemBox error={rowError.error} />{/if}
              </td>
            </tr>
          {/each}
        </tbody>
      </table>
    </div>
  {/if}
</section>

<form class="card" onsubmit={create} novalidate aria-labelledby="new-user-title">
  <h2 id="new-user-title">New user</h2>
  <div class="row fields">
    <div class="field">
      <label for="user-name">User name</label>
      <input
        id="user-name"
        type="text"
        bind:value={name}
        maxlength="32"
        autocomplete="off"
        spellcheck="false"
        aria-invalid={nameMissing || !!createErrs["/name"]}
        aria-describedby="user-name-hint{nameMissing || createErrs['/name']
          ? ' user-name-error'
          : ''}"
        oninput={() => (nameMissing = false)}
      />
      <span class="hint" id="user-name-hint"
        >a-z, 0-9, _ and -; up to 32 characters. A name is never given twice.</span
      >
      {#if nameMissing || createErrs["/name"]}<span class="error" id="user-name-error"
          >{nameMissing ? "Enter a user name." : createErrs["/name"]}</span
        >{/if}
    </div>
    <div class="field">
      <label for="user-role">Role</label>
      <select id="user-role" bind:value={role} aria-describedby="user-role-hint">
        {#each ROLES as r (r)}<option value={r}>{r}</option>{/each}
      </select>
      <span class="hint" id="user-role-hint">{roleText[role]}</span>
    </div>
  </div>
  <ProblemBox error={createErrs["/name"] ? null : createError} />
  <button class="btn primary" type="submit" disabled={createBusy}>Create user</button>
  <p class="muted small">
    Plumb makes a one-time password and shows it once. The user must change it at the first login.
  </p>
</form>

<style>
  h2 {
    margin-top: 0;
  }
  .card {
    margin-top: var(--space-4);
  }
  .flush {
    padding: 0;
    overflow: hidden;
  }
  .nowrap {
    white-space: nowrap;
  }
  .you {
    font-family: var(--font-sans);
    font-weight: 400;
    color: var(--color-text-muted);
  }
  .states {
    display: flex;
    flex-wrap: wrap;
    gap: var(--space-1);
  }
  .actions {
    flex-wrap: wrap;
  }
  .confirm {
    display: flex;
    flex-direction: column;
    gap: var(--space-1);
    min-width: 16rem;
  }
  .confirm p {
    margin: 0;
  }
  .small {
    font-size: var(--text-sm);
  }
  .fields {
    align-items: flex-start;
    flex-wrap: wrap;
  }
  .error {
    font-size: var(--text-xs);
    color: var(--color-danger);
  }
  .shown {
    border: 1px solid var(--color-border);
    border-left: 4px solid var(--color-warn);
    border-radius: var(--radius);
    padding: var(--space-3);
    margin-bottom: var(--space-3);
    background: var(--color-surface);
  }
  .shown h3 {
    margin-top: 0;
  }
  .value {
    padding: var(--space-2);
    background: var(--color-surface-2);
    border-radius: var(--radius);
    overflow-wrap: anywhere;
    white-space: pre-wrap;
  }
</style>
