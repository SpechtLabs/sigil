// What the Node tests need of the service, bundled for Node by
// test/node/lib/build.ts: Node runs TypeScript only file by file, without the
// "@/" paths and extensionless imports the service uses, so the tests load
// this bundle instead of the sources.

export { loadConfig } from "@/lib/config/config";
export { PLATFORM_FILES, TEAM_FILES, TEAMS_YAML } from "@/lib/embedded";
export { embeddedBundle } from "@/lib/store/bundle";
export { TeamDirectory } from "@/lib/teams/directory";
export { fakeTelemetry, teamSource, testWasm } from "@/lib/testing";
export { createService } from "@/server/service";
