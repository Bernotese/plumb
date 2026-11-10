<script lang="ts">
  import { tick } from "svelte";
  import type { PlumbApi } from "./lib/client";
  import { createMockApi, MOCK_USER } from "./lib/mock/mock";
  import { go, href, parse, type Route } from "./lib/router";
  import { applyTheme, loadTheme, type Theme } from "./lib/theme";
  import type { Session } from "./lib/types";
  import ChangePassword from "./screens/ChangePassword.svelte";
  import IssueView from "./screens/IssueView.svelte";
  import Issues from "./screens/Issues.svelte";
  import Login from "./screens/Login.svelte";
  import PlanView from "./screens/PlanView.svelte";
  import RunView from "./screens/RunView.svelte";
  import TargetForm from "./screens/TargetForm.svelte";
  import Targets from "./screens/Targets.svelte";

  // T-006 runs against the mock API; T-014 switches to the HTTP client.
  // "?as=admin" starts signed in, "?as=first-login" at the forced password
  // change (screenshots, development).
  const as = new URLSearchParams(location.search).get("as");
  const api: PlumbApi = createMockApi({
    signedIn: as === "admin",
    firstLogin: as === "first-login",
  });

  let route = $state<Route>(parse(location.hash));
  let session = $state<Session | null>(null);
  let ready = $state(false);
  let theme = $state<Theme>(loadTheme());

  $effect(() => applyTheme(theme));

  $effect(() => {
    api
      .session()
      .then(
        (s) => (session = s),
        () => (session = null),
      )
      .finally(() => (ready = true));
    const onHash = () => {
      route = parse(location.hash);
      // Move focus to the new page title, so keyboard and screen-reader
      // users start at the top of the new screen.
      void tick().then(() => document.getElementById("page-title")?.focus());
    };
    window.addEventListener("hashchange", onHash);
    return () => window.removeEventListener("hashchange", onHash);
  });

  // Which screen to show: login and the forced password change come first.
  const screen = $derived.by(() => {
    if (!ready) return "loading";
    if (!session) return "login";
    if (session.must_change_password) return "password";
    return "app";
  });

  function onLogin(s: Session) {
    session = s;
    void tick().then(() => document.getElementById("page-title")?.focus());
    if (!s.must_change_password && (route.name === "login" || route.name === "password"))
      go({ name: "targets" });
  }

  function onPasswordChanged() {
    if (session) session = { ...session, must_change_password: false };
    go({ name: "targets" });
  }

  async function logout() {
    await api.logout();
    session = null;
    go({ name: "login" });
  }

  const navCurrent = (names: Route["name"][]) => (names.includes(route.name) ? "page" : undefined);
</script>

<a class="skip" href="#main">Skip to content</a>
<header>
  <a class="brand" href={href({ name: "targets" })}>Plumb</a>
  {#if screen === "app"}
    <nav aria-label="Main">
      <a
        href={href({ name: "targets" })}
        aria-current={navCurrent(["targets", "target-new", "target-edit"])}>Targets</a
      >
      <a href={href({ name: "issues" })} aria-current={navCurrent(["issues", "issue"])}>Issues</a>
    </nav>
  {/if}
  <div class="tools">
    <label for="theme" class="sr-only">Theme</label>
    <select id="theme" bind:value={theme}>
      <option value="system">System theme</option>
      <option value="light">Light</option>
      <option value="dark">Dark</option>
    </select>
    {#if session && screen === "app"}
      <span class="mono user">{session.principal}</span>
      <button class="btn" type="button" onclick={logout}>Log out</button>
    {/if}
  </div>
</header>
<p class="demo">Demo data: this build uses a mock of the Plumb API.</p>

<main id="main" tabindex="-1">
  {#if screen === "loading"}
    <p class="muted" aria-live="polite">Loading…</p>
  {:else if screen === "login"}
    <Login {api} onlogin={onLogin} hint="Demo login: {MOCK_USER.username} / {MOCK_USER.password}" />
  {:else if screen === "password"}
    <ChangePassword {api} ondone={onPasswordChanged} />
  {:else if route.name === "targets" || route.name === "login" || route.name === "password"}
    <Targets {api} />
  {:else if route.name === "target-new"}
    <TargetForm {api} />
  {:else if route.name === "target-edit"}
    {#key route.target}<TargetForm {api} name={route.target} />{/key}
  {:else if route.name === "plan"}
    {#key route.id}<PlanView {api} id={route.id} />{/key}
  {:else if route.name === "run"}
    {#key route.id}<RunView {api} id={route.id} />{/key}
  {:else if route.name === "issues"}
    <Issues {api} />
  {:else if route.name === "issue"}
    {#key route.id}<IssueView {api} id={route.id} />{/key}
  {:else}
    <h1 id="page-title" tabindex="-1">Page not found</h1>
    <p>
      There is no page at <span class="mono">{route.path}</span>.
      <a href={href({ name: "targets" })}>Go to the targets</a>.
    </p>
  {/if}
</main>

<style>
  .skip {
    position: absolute;
    left: -999px;
    top: var(--space-2);
    background: var(--color-surface);
    padding: var(--space-2) var(--space-3);
    z-index: 10;
  }
  .skip:focus {
    left: var(--space-2);
  }
  header {
    display: flex;
    flex-wrap: wrap;
    gap: var(--space-4);
    align-items: center;
    padding: var(--space-3) var(--space-6);
    background: var(--color-surface);
    border-bottom: 1px solid var(--color-border);
  }
  .brand {
    font-weight: 800;
    font-size: var(--text-lg);
    color: var(--color-text);
    text-decoration: none;
  }
  nav {
    display: flex;
    gap: var(--space-4);
  }
  nav a {
    color: var(--color-text-muted);
    text-decoration: none;
    font-weight: 600;
    padding: var(--space-1) 0;
    border-bottom: 2px solid transparent;
  }
  nav a[aria-current="page"] {
    color: var(--color-text);
    border-bottom-color: var(--color-accent);
  }
  .tools {
    margin-left: auto;
    display: flex;
    gap: var(--space-3);
    align-items: center;
  }
  .user {
    color: var(--color-text-muted);
  }
  .demo {
    margin: 0;
    padding: var(--space-1) var(--space-6);
    font-size: var(--text-xs);
    background: var(--color-info-bg);
    color: var(--color-info);
  }
  main {
    max-width: 72rem;
    margin: 0 auto;
    padding: var(--space-6);
  }
  main:focus {
    outline: none;
  }
  @media (max-width: 40rem) {
    header,
    main {
      padding-left: var(--space-4);
      padding-right: var(--space-4);
    }
  }
</style>
