// @vitest-environment jsdom
import { expect, it } from "vitest";
import fixture from "../../../spec/fixtures/task-repositories/contract.json";
import { cleanRepositoryDraft, repositoryDraft, repositoryRelativePath, validRepositoryAlias, validRequestedRef, type RequestedRef, type TaskRepository } from "./task-repositories";

it("shares alias, layout and typed-ref fixtures with the manager", () => {
  for (const alias of fixture.aliases.valid) expect(validRepositoryAlias(alias), alias).toBe(true);
  for (const alias of fixture.aliases.invalid) expect(validRepositoryAlias(alias), alias).toBe(false);
  for (const ref of fixture.refs) expect(validRequestedRef(ref as RequestedRef)).toBe(true);
  expect(repositoryRelativePath("root", "", 1)).toBe(".");
  expect(repositoryRelativePath("root", "", 2)).toBeUndefined();
  expect(repositoryRelativePath("container", "web", 1)).toBe("web");
});

it("preserves typed refs and numeric string links when round-tripping a saved repository", () => {
  const row = { repo_id: "web", requested_ref: fixture.refs[3], links: [{ kind: "pull_request", number: "9007199254740993", canonical_url: "https://github.com/example/web/pull/9007199254740993" }] } as TaskRepository;
  expect(repositoryDraft([row])[0]).toMatchObject({ requested_ref: fixture.refs[3], links: [{ kind: "pull_request", number: "9007199254740993" }] });
  expect(cleanRepositoryDraft([{ repo_id: "web", links: [{ url: "  https://github.com/example/web/pull/7  " }, { url: "" }] }])[0].links).toEqual([{ url: "https://github.com/example/web/pull/7" }]);
});
