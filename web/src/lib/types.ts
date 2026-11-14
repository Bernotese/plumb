// Names for the generated API types (api.gen.ts, from api/openapi.yaml).
// No API type is written by hand.
import type { components } from "./api.gen";

type S = components["schemas"];
export type Session = S["Session"];
export type Problem = S["Problem"];
export type Risk = S["Risk"];
export type Change = S["Change"];
export type ImageView = S["ImageView"];
export type ImageDoc = S["image"];
export type ImageSpec = ImageDoc["spec"];
export type ImageStatus = S["ImageStatus"];
export type ImageVersion = S["ImageVersion"];
export type ImagePlan = S["ImagePlan"];
export type ImageRun = S["ImageRun"];
export type DiskUse = S["DiskUse"];
export type BlockView = S["BlockView"];
export type TargetView = S["TargetView"];
export type TargetDoc = S["target"];
export type TargetSpec = TargetDoc["spec"];
export type BlockDoc = S["block"];
export type Proposal = S["Proposal"];
/**
 * `documents` and `documents_truncated` come with T-170 (Plan.documents);
 * they are optional, as an older server leaves them out.
 */
export type Plan = S["Plan"] & {
  documents?: S["DocumentChange"][];
  documents_truncated?: boolean;
};
export type ResourcePlan = S["ResourcePlan"];
export type Run = S["Run"];
export type RunStep = S["RunStep"];
export type Issue = S["Issue"];
export type IssueAction = S["IssueAction"];
export type PlumbEvent = S["Event"];
export type StateDocument = S["Document"];
export type DocumentRef = S["DocumentRef"];
export type RevisionInfo = S["RevisionInfo"];
export type RevisionDiff = S["RevisionDiff"];
export type DocumentChange = S["DocumentChange"];
/** One page of a list answer (`Page` with typed items). */
export interface PageOf<T> {
  items: T[];
  next: string | null;
}
export type Token = S["Token"];
export type BackupList = S["BackupList"];
export type BackupRun = S["BackupRun"];
export type RestoreState = S["RestoreState"];
export type OneTimePassword = S["OneTimePassword"];
export type TokenRequest = S["TokenRequest"];
export type TokenCreated = S["TokenCreated"];
export type Tier = S["Tier"];
export type SecretInfo = S["SecretInfo"];
export type ImportRequest = S["ImportRequest"];
export type RuleView = S["RuleView"];
export type RuleDoc = S["Rule"];
export type ResolveRequest = S["ResolveRequest"];
export type RuleScope = NonNullable<ResolveRequest["rule"]>;
export type IgnoredDrift = S["IgnoredDrift"];
export type TargetPlan = S["TargetPlan"];
