// Names for the generated API types (api.gen.ts, from api/openapi.yaml).
// No API type is written by hand.
import type { components } from "./api.gen";

type S = components["schemas"];
export type Session = S["Session"];
export type Problem = S["Problem"];
export type Risk = S["Risk"];
export type Change = S["Change"];
export type ImageView = S["ImageView"];
export type BlockView = S["BlockView"];
export type TargetView = S["TargetView"];
export type TargetDoc = S["target"];
export type TargetSpec = TargetDoc["spec"];
export type BlockDoc = S["block"];
export type Proposal = S["Proposal"];
export type Plan = S["Plan"];
export type ResourcePlan = S["ResourcePlan"];
export type Run = S["Run"];
export type RunStep = S["RunStep"];
export type Issue = S["Issue"];
export type IssueAction = S["IssueAction"];
export type PlumbEvent = S["Event"];
