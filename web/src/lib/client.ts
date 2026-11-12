// The calls the UI makes. T-006 runs against the mock (mock/mock.ts);
// T-014 adds the HTTP implementation of the same interface.
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
} from "./types";

export interface PlumbApi {
  session(): Promise<Session>;
  login(username: string, password: string): Promise<Session>;
  logout(): Promise<void>;
  changePassword(current: string, next: string): Promise<void>;
  listImages(): Promise<ImageView[]>;
  getImage(name: string): Promise<ImageView>;
  /** Plans a manual build of a built Image (origin `build`, T-062). 422 for a stock Image. */
  buildImage(name: string, reason?: string): Promise<Plan>;
  listBlocks(): Promise<BlockView[]>;
  getBlock(name: string): Promise<BlockView>;
  listTargets(): Promise<TargetView[]>;
  getTarget(name: string): Promise<TargetView>;
  createPlan(proposal: Proposal): Promise<Plan>;
  /** Plans a re-enrollment of a target (D-038). 422 for a running VM with allow_restart off (D-194). */
  reenrollTarget(name: string, reason: string): Promise<Plan>;
  getPlan(id: number): Promise<Plan>;
  approvePlan(id: number, comment?: string): Promise<Run>;
  getRun(id: number): Promise<Run>;
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
