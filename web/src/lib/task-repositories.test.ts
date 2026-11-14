// @vitest-environment jsdom
import { expect, it } from "vitest";
import fixture from "../../../spec/fixtures/task-repositories/contract.json";
import { cleanRepositoryDraft, repositoryDraft, repositoryLinkView, repositoryRelativePath, validRepositoryAlias, validRequestedRef, type RequestedRef, type TaskRepository } from "./task-repositories";

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

const github = { provider: "github", instance_url: "https://github.com", path: "example/web", canonical_url: "https://github.com/example/web" };
const gitlab = { provider: "gitlab", instance_url: "https://gitlab.example.com", path: "team/api", canonical_url: "https://gitlab.example.com/team/api" };

it("reads a pasted issue or PR URL only when it belongs to the saved repository", () => {
  expect(repositoryLinkView(github, { url: "https://github.com/example/web/pull/7" })).toEqual({ kind: "pull_request", number: "7", url: "https://github.com/example/web/pull/7", label: "PR #7" });
  expect(repositoryLinkView(github, { url: "https://github.com/example/web/issues/12" })).toMatchObject({ kind: "issue", number: "12", label: "#12" });
  // Another repository, another host, another scheme, credentials, a query, a fragment, a deeper path or a non-number: all refused.
  const credentials = "https://user:pw" + "@github.com/example/web/pull/7";
  for (const url of ["https://github.com/example/docs/pull/7", "https://gitlab.com/example/web/pull/7", "http://github.com/example/web/pull/7",
    credentials, "https://github.com/example/web/pull/7?x=1", "https://github.com/example/web/pull/7#c",
    "https://github.com/example/web/pull/7/files", "https://github.com/example/web/pull/", "https://github.com/example/web/pull/07",
    "https://github.com/example/web/pull/0", "https://github.com/example/web", "not a url", ""]) {
    expect(repositoryLinkView(github, { url }), url).toBeNull();
  }
});

it("reads saved kind and number for GitHub and GitLab, keeping the number a string", () => {
  expect(repositoryLinkView(gitlab, { kind: "pull_request", number: "9" })).toEqual({ kind: "pull_request", number: "9", url: "https://gitlab.example.com/team/api/-/merge_requests/9", label: "MR !9" });
  expect(repositoryLinkView(gitlab, { kind: "issue", number: "9" })).toMatchObject({ url: "https://gitlab.example.com/team/api/-/issues/9", label: "#9" });
  expect(repositoryLinkView(gitlab, { url: "https://gitlab.example.com/team/api/-/merge_requests/9007199254740993" })).toMatchObject({ number: "9007199254740993" });
  expect(repositoryLinkView(github, { kind: "issue", number: "0" })).toBeNull();
  expect(repositoryLinkView({ ...github, provider: "bitbucket" }, { kind: "issue", number: "1" })).toBeNull();
});
