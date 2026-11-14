// The calls the UI makes. T-006 runs against the mock (mock/mock.ts);
// T-014 adds the HTTP implementation of the same interface.
import type { components } from "./api.gen";
import type {
  BlockView,
  ImageView,
  Issue,
  IssueAction,
  PageOf,
  Plan,
  PlumbEvent,
  Proposal,
  RevisionDiff,
  RevisionInfo,
  RuleScope,
  RuleView,
  Run,
  SecretInfo,
  Session,
  TargetView,
  Token,
  TokenCreated,
  TokenRequest,
  ImportRequest,
  BackupList,
  BackupRun,
  OneTimePassword,
  RestoreState,
} from "./types";

type S = components["schemas"];
export type CatalogBlock = S["CatalogBlock"];
export type User = S["User"];
export type UserRole = S["Role"];
export type PinRefresh = S["PinRefresh"];
/** The answer of `POST /images/{name}/refresh`: a Plan, or the pin is current. */
export type RefreshResult = { plan: Plan } | { unchanged: PinRefresh };
/** Controller settings (D-361): the backup schedule and the Proxmox view. */
export type Settings = S["Settings"];
export type ChangeSettings = S["ChangeSettingsRequest"];
/** Slice 6 part B (ARCHITECTURE §19): repair, install command, adoption. */
export type RepairRequest = S["RepairRequest"];
export type InstallCommand = S["InstallCommand"];
export type AdoptRequest = S["AdoptRequest"];
export type UnmanagedGuest = S["UnmanagedGuest"];
export type RepairKeyView = S["RepairKeyView"];
export type RepairState = S["RepairState"];

export interface PlumbApi {
  session(): Promise<Session>;
  login(username: string, password: string): Promise<Session>;
  logout(): Promise<void>;
  changePassword(current: string, next: string): Promise<void>;
  listImages(): Promise<ImageView[]>;
  getImage(name: string): Promise<ImageView>;
  /** Plans a manual build of a built Image (origin `build`, T-062). 422 for a stock Image. */
  buildImage(name: string, reason?: string): Promise<Plan>;
  /**
   * Fetches the newest pin of an Image with `verify` (D-335): a Plan of origin `pin_refresh`
   * when it differs, else `unchanged`. 422 without `verify`, 502 `pin-source-failed`.
   */
  refreshImagePin(name: string): Promise<RefreshResult>;
  listBlocks(): Promise<BlockView[]>;
  /** The starter Blocks of this binary, compared with the head (D-328). */
  listCatalogBlocks(): Promise<CatalogBlock[]>;
  getBlock(name: string): Promise<BlockView>;
  listTargets(): Promise<TargetView[]>;
  getTarget(name: string): Promise<TargetView>;
  createPlan(proposal: Proposal): Promise<Plan>;
  /**
   * Proposes an agent update (origin `agent_update`, D-257). 409 `nothing-to-do` when the agent
   * is current; 422 for `manual` or `unknown`.
   */
  updateTargetAgent(name: string, reason?: string): Promise<Plan>;
  /** Plans a re-enrollment of a target (D-038). 422 for a running VM with allow_restart off (D-194). */
  reenrollTarget(name: string, reason: string): Promise<Plan>;
  getPlan(id: number): Promise<Plan>;
  approvePlan(id: number, comment?: string): Promise<Run>;
  getRun(id: number): Promise<Run>;
  /** One page of plans, newest first; `status` filters (T-153). */
  listPlans(
    filter?: { status?: Plan["status"] },
    cursor?: string,
    limit?: number,
  ): Promise<PageOf<Plan>>;
  /** Drops a pending plan: it becomes `stale` (DELETE /plans/{id}); 409 when not pending. */
  discardPlan(id: number): Promise<void>;
  /** One page of runs, newest first (T-153). */
  listRuns(cursor?: string, limit?: number): Promise<PageOf<Run>>;
  /** Issues, newest first; the filter uses the `target` and `status` query of GET /issues. */
  listIssues(filter?: { target?: string; status?: Issue["status"] }): Promise<Issue[]>;
  getIssue(id: number): Promise<Issue>;
  /** `accept`, `revert` and `ignore_rule` make a Plan; `rule` is the scope of an ignore rule. */
  resolveIssue(
    id: number,
    action: Exclude<IssueAction, "ignore_once">,
    reason?: string,
    rule?: RuleScope,
  ): Promise<Plan>;
  /** Adds a comment to an issue (T-153). */
  commentIssue(id: number, body: string): Promise<Issue["comments"][number]>;
  /** `ignore_once`: resolves the Issue without a Plan (D-091). */
  ignoreOnce(id: number, reason?: string): Promise<Issue>;
  /** Rules of the head revision (D-092). */
  listRules(): Promise<RuleView[]>;
  /** One page of revisions, newest first (T-034). */
  listRevisions(cursor?: string, limit?: number): Promise<PageOf<RevisionInfo>>;
  /** The documents that differ between `from` (default n-1) and revision `n`. */
  diffRevision(n: number, from?: number): Promise<RevisionDiff>;
  /** Plans a rollback to revision `n`: a Plan with origin `rollback`. */
  rollbackRevision(n: number, reason: string): Promise<Plan>;
  /** API tokens, newest first, revoked ones included (T-035, human session). */
  listTokens(): Promise<Token[]>;
  /** The answer holds the bearer once. The caller must not keep it. */
  createToken(req: TokenRequest): Promise<TokenCreated>;
  revokeToken(id: string): Promise<void>;
  /** Enables a token that a restore disabled (D-303, human session). */
  enableToken(id: string): Promise<Token>;
  /** Proposes an SSH repair of a dead agent (D-372, D-385): a human-only Plan. */
  repairTarget(name: string, req?: RepairRequest): Promise<Plan>;
  /** A one-time install command for the guest (D-387, admin session); shown once. */
  createInstallCommand(name: string): Promise<InstallCommand>;
  /** Guests of the pool that no Target or Image holds (D-373). */
  listUnmanagedGuests(): Promise<UnmanagedGuest[]>;
  /** Proposes to adopt a guest of the pool as the new Target `name` (D-386). */
  adoptTarget(name: string, req: AdoptRequest): Promise<Plan>;
  /** D-396: end the park of an agent left in a guest (admin). */
  forgetAgent(vmid: number): Promise<void>;
  /** A new repair key, or `revokeOld` the previous one (D-382, admin session). */
  rotateRepairKey(revokeOld?: boolean): Promise<RepairKeyView>;
  /** The backup schedule with its sources and the Proxmox view (D-361). */
  getSettings(): Promise<Settings>;
  /** Changes the backup schedule; `null` goes back to the install value (human admin). */
  changeSettings(change: ChangeSettings): Promise<Settings>;
  /** Backups, schedule, last run and the off-host copy state (D-302). */
  listBackups(): Promise<BackupList>;
  /** Starts one backup now; 409 `backup-running` or `restored-mode`. */
  createBackup(): Promise<BackupRun>;
  /** Restore state; `restored_mode` until a person confirms (D-303). */
  getRestore(): Promise<RestoreState>;
  /** Ends restored mode: the person states that the old controller is off. */
  confirmRestore(reason?: string): Promise<RestoreState>;
  /**
   * Gives another user a new one-time password (D-321, human session). The
   * answer holds it once; the caller must not keep it.
   */
  resetUserPassword(name: string): Promise<OneTimePassword>;
  /** Human users, sorted by name, disabled ones included (D-330, admin session). */
  listUsers(): Promise<User[]>;
  /** Makes a user; the answer holds its one-time password once. 409 `user-exists`. */
  createUser(name: string, role: UserRole): Promise<OneTimePassword>;
  /** Changes role or disabled; 409 `own-user` or `last-admin`. */
  changeUser(name: string, change: { role?: UserRole; disabled?: boolean }): Promise<User>;
  /** Secret metadata, sorted by name. No value ever comes back. */
  listSecrets(): Promise<SecretInfo[]>;
  putSecret(name: string, value: string): Promise<SecretInfo>;
  deleteSecret(name: string): Promise<void>;
  /** The export file (YAML text); with a passphrase it holds the encrypted secret bundle. */
  exportState(passphrase?: string): Promise<string>;
  /** Plans an import of an export file: a Plan with origin `import`. */
  importState(req: ImportRequest): Promise<Plan>;
  /**
   * Subscribes to the event stream: events after `since`, or only new ones
   * when `since` is undefined. Reconnects by itself. Returns the unsubscribe
   * function. `onOpen` runs on every (re)open of the stream: a snapshot
   * loaded before it can have missed events (see live.ts).
   */
  events(
    since: number | undefined,
    onEvent: (e: PlumbEvent) => void,
    onOpen?: () => void,
  ): () => void;
}
