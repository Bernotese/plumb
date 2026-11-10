// The calls the UI makes. T-006 runs against the mock (mock/mock.ts);
// T-014 adds the HTTP implementation of the same interface.
import type {
  BlockView,
  ImageView,
  Issue,
  IssueAction,
  Plan,
  PlumbEvent,
  Proposal,
  Run,
  Session,
  TargetView,
} from "./types";

export interface PlumbApi {
  session(): Promise<Session>;
  login(username: string, password: string): Promise<Session>;
  logout(): Promise<void>;
  changePassword(current: string, next: string): Promise<void>;
  listImages(): Promise<ImageView[]>;
  listBlocks(): Promise<BlockView[]>;
  listTargets(): Promise<TargetView[]>;
  getTarget(name: string): Promise<TargetView>;
  createPlan(proposal: Proposal): Promise<Plan>;
  getPlan(id: number): Promise<Plan>;
  approvePlan(id: number, comment?: string): Promise<Run>;
  getRun(id: number): Promise<Run>;
  listIssues(): Promise<Issue[]>;
  getIssue(id: number): Promise<Issue>;
  resolveIssue(id: number, action: IssueAction, reason?: string): Promise<Plan>;
  /** Subscribes to the event stream after `since`; returns the unsubscribe function. */
  events(since: number, onEvent: (e: PlumbEvent) => void): () => void;
}
