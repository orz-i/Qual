// QUAL-46: codegen alone cannot prove the advertised archive versions match
// the actual v1/v2/v3 PostgreSQL ZIP encoder. Keep this build-time assertion.
import type { components } from "./schema";

type DeclaredVersion = components["schemas"]["ArchiveManifest"]["archive_schema_version"];
type TypeEqual<A, B> = (<T>() => T extends A ? 1 : 2) extends
  (<T>() => T extends B ? 1 : 2) ? true : false;
type Assert<T extends true> = T;
export type ArchiveManifestVersionContract = Assert<TypeEqual<DeclaredVersion, 1 | 2 | 3>>;
