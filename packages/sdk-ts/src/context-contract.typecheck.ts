// Type-only guard: a generated ContextBundle must expose a discriminated
// source union. A return to free-form [key:string]:unknown fails tsc.
import type { components } from "./schema.js";

type Fact = components["schemas"]["WorkContextFact"];
type Attempt = Extract<Fact, { source: "test_attempts" }>;
type Review = Extract<Fact, { source: "work_role_entries" }>;
type Dependency = Extract<Fact, { source: "work_dependencies" }>;
type Coverage = Extract<Fact, { source: "test_case_coverage" }>;
type Assert<T extends true> = T;
type IsUnknown<T> = unknown extends T ? true : false;

export type AttemptHasTypedStatus = Assert<IsUnknown<Attempt["status"]> extends false ? true : false>;
export type AttemptHasTypedIngestion = Assert<Attempt["ingested_at"] extends string ? true : false>;
export type ReviewHasTypedSelfReview = Assert<Review["self_review"] extends boolean ? true : false>;
export type DependencyHasRelationKey = Assert<Dependency["source_key"] extends string ? true : false>;
export type CoverageHasRelationKey = Assert<Coverage["source_key"] extends string ? true : false>;
