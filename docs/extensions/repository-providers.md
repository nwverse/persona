# Repository providers

An extension can be the source of repositories on a code-hosting platform Persona does not know, such as a self-hosted
or company platform with its own address layout and sign-in. Its repositories can then be saved in **Settings ›
Repositories** and its issues and review requests attached to a to-do, as GitHub's and GitLab's are. The extension
already talks to the platform (its own credentials, its own API); Persona asks it, and checks every answer. Design and
decisions: #10330, modeled on [secret providers](secret-providers.md).

GitHub and GitLab stay built in and work exactly as before (`server/internal/assistant/repository/identity.go`,
`check.go`, `task.go`); the provider list puts them first (`repository.Builtins`).

## The contribution

```json
"contributes": {"repository_providers": [{
  "id": "acme",
  "name": "Acme Code",
  "hosts": ["code.example.com"],
  "link_kinds": [{"kind": "pull_request", "label": "MR"}, {"kind": "issue", "label": "Issue"}]
}]}
```

- `name` (1–80 characters) is what the apps show for the source and for each repository saved from it.
- `hosts`: 1 to 16 exact lowercase host names, no wildcard, port or IP address. `github.com`, `gitlab.com` and the
  aliases the built-in rules map to them (`www.github.com`, `api.github.com`, `ssh.github.com`, `altssh.gitlab.com`;
  `extension.ReservedRepositoryHosts`) are refused. A host another installed extension claims, switched on or off, fails
  the preview and the install, and one manifest cannot claim a host twice: an address names its provider by its host
  alone.
- `link_kinds` only relabels the two kinds every client knows, `issue` and `pull_request` (at most one label each, 1–24
  characters): there are no new kinds.
- The program must speak the protocol. The manager asks it only while it is enabled, without a problem, with the
  contribution switched on and its program running.
- The install page says what it agrees to: Persona asks it to read addresses and check repositories and links on these
  hosts, so it learns which repositories and links you add there. The hosts are part of the permission digest (empty for
  every other extension, whose digests do not change), so an upgrade that adds a host is agreed to again.
- A provider's hosts give it no network access: a sandboxed program still reaches only `permissions.network`, so declare
  the platform's API host there.

## Methods

Namespace `repo.`, types in `schema/extension/repository.schema.json`. Only the manager calls them; no other extension,
device or token can.

| Method | Parameters → answer | Notes |
| --- | --- | --- |
| `repo.parse` | `{contribution, url}` → `{instance_url, path, canonical_url, clone_url?, display_name?}` | `url` is what the user or an Agent gave: a repository page, a clone address or a link inside the repository, on one of the declared hosts and without credentials. `display_name` becomes the repository's name when the user gave none. |
| `repo.check` | `{contribution, canonical_url}` → `{}` | Does the repository exist for the signed-in account? Asked when a repository is saved or its address changes, as the built-in check is. |
| `repo.link` | `{contribution, repo, url?, kind?, number?}` → `{kind, number, canonical_url, title?}` | `repo` is `{instance_url, path, canonical_url}`. Asked with a pasted `url`, or with the `kind` and `number` an Agent gave. |
| `repo.describe` (optional) | `{contribution, canonical_url}` → `{title, state?}` | A link's title, asked when a new link has none. `state` is `open`, `closed` or `merged`; anything else is dropped. A provider without it answers `unknown_method` and the link simply has no title. |

Errors a provider answers with a code: `not_found`, `not_signed_in`, `refused` (its words are shown) and `failed`.
Anything else is a failure with its words. An older program that does not know the method (`unknown_method`,
`no_contribution`) is a failure that says so.

## The manager's side

Code: `server/internal/extmgr/repository_providers.go` (the calls), `server/internal/assistant/repository/provider.go`
(`Registry`, the provider ids and every re-check), `server/internal/extension/repository_providers.go` (who claims a
host).

- **Routing**: an address with no provider chosen goes to the extension that claims its host, otherwise to the built-in
  rules as before. An explicit `github` or `gitlab` is the user's choice and wins, even on a claimed host (a self-hosted
  GitLab stays one). A link address the built-in rules give a GitHub or GitLab repository the to-do or the catalog
  already has stays with that repository, on any host. An address is sent to an extension only when it is on that
  extension's hosts and carries no credentials; a link only when it is plain (a kind and a number, or an http(s)
  address with nothing but a path).
- **Bounds**: 5 seconds per call; one request's provider calls (a save's parse and check, a to-do save's new links)
  share an 8-second budget.
- **Re-checks**: every answer is checked before it is used or stored, and refused (as a failure that says why) when
  - `instance_url` is not `https://` and one of the declared hosts, with nothing after the host but an optional port;
  - `path` is not 1 to 20 plain segments (letters, digits, `-._~`) of at most 512 bytes, or `canonical_url` is not
    `instance_url/path`;
  - `clone_url` is not a credential-free `https://`, `ssh://git@` or `git@host:` address on a declared host;
  - a link is not an `issue` or `pull_request` with a number of 1 to 20 digits (no leading zero), is not the link asked
    about, or its `canonical_url` is not an `https://` address strictly under the repository's own with no query or
    fragment.
  Names and titles are put on one line and cut (80 characters for `display_name`, 1024 bytes for a title). A refused
  answer is written to the extension's log with the reason.
- **Errors** are `repository.ProviderError` with a code: `no_provider` (no installed extension claims it), `provider_off`
  (installed but switched off, failing or not running), `not_found`, `not_signed_in`, `refused` and `failed`. The
  repository routes answer them as `repository_no_provider`, `repository_provider_off`, `repository_provider_not_found`,
  `repository_provider_not_signed_in`, `repository_provider_refused` and `repository_provider_failed`, and the to-do
  routes and tools as `task_repositories_…` with the same suffixes, in the asker's language and naming the provider.
- **No transaction waits on an extension**: a to-do's new links are asked about first and carried into the save, which
  never calls a program. Links in Agents' reports that a provider must be asked about are resolved in the background,
  so the hub's events never wait on an extension, and an address is asked about at most once every 5 minutes per
  to-do (`server/internal/assistant/todo_repository_providers.go`).
- **Right after a restart**, until the extensions are started and their providers listed, a link on a host no
  built-in owns waits: an interactive save waits within its budget, a report waits in the background, and the import
  of older links is put off to a later read, so nothing is taken for a GitHub repository by mistake.

`GET /api/v1/assistant/repositories/providers` lists the sources for the apps: `{manager, providers: [{id, name, builtin,
hosts, state, link_kinds?}]}`, GitHub and GitLab first, then the installed providers, `state` `available` or `off`. An
uninstalled provider is not listed. An older manager answers 404 (its `/{id}` route), meaning only the built-ins.

## Identities and what is stored

- A repository's `provider` is `github`, `gitlab` or `ext:<extension>/<contribution>` (`ext:acme-code/acme`). The
  extension's name is kept beside the identity as `provider_name`, on the saved repository and in a to-do's
  `source_snapshot`, so the row still reads when the extension is gone. GitHub and GitLab rows carry no `provider_name`,
  so their stored bytes and snapshot digests are unchanged.
- Everything stays in the existing places: the `assistant.repositories` setting and `todo_repository_sets.repositories`.
  No table changes.
- A to-do read adds, for an extension's rows only, `provider_state` (`available`, `off` or `missing`) and `link_kinds`
  (the provider's labels), computed like `catalog_state` and never stored.
- A manager from before #10330 does not know `ext:` providers: it refuses their addresses, and a link it is given by
  kind and number for such a row would be written with GitHub's layout. Don't roll a manager back while it holds them.

## When the provider is gone

When the extension is switched off, fails, is not running or is uninstalled:

- Saved repositories and a to-do's repositories and links still show and open: they are read from what was stored,
  never re-derived from the provider's layout.
- Editing a saved repository's name, aliases or purpose works; binding a saved repository to a to-do works; workspace
  preparation uses the stored clone address.
- Saving a to-do's repositories or links (`PUT /api/v1/todos/{id}/repositories`, `todo_repository_links_set`) keeps the
  links already there without asking the provider, and removing links works.
- Only adding a repository, changing its address, checking it, or adding a new link for that provider fails, with
  `provider_off` or `no_provider`. Agent reports that mention such a link skip it, and the import of a to-do's older PR
  links never calls an extension.

## Writing a provider

With the Go SDK, the namespace is `ext.RepositoryNamespace`:

```go
ext.Program{ID: "acme-code", Version: "1.0.0", Namespaces: map[string]ext.Handler{
	ext.RepositoryNamespace: ext.Methods{
		"parse": ext.Method(func(ctx context.Context, p wire.RepoParseParams) (wire.RepoParseResult, error) {
			// Read p.URL with the platform's own rules and API.
			return wire.RepoParseResult{InstanceURL: "https://code.example.com", Path: "team/app",
				CanonicalURL: "https://code.example.com/team/app"}, nil
		}),
		"check": ext.Method(func(ctx context.Context, p wire.RepoCheckParams) (wire.Empty, error) {
			return wire.Empty{}, nil // or &ext.Error{Code: "not_found"} / "not_signed_in"
		}),
		"link": ext.Method(func(ctx context.Context, p wire.RepoLinkParams) (wire.RepoLinkResult, error) {
			return wire.RepoLinkResult{Kind: "pull_request", Number: "12",
				CanonicalURL: p.Repo.CanonicalURL + "/reviews/12", Title: "Fix the build"}, nil
		}),
	}.For("acme"),
}}
```

Test against hosts under `.test` (`code.acme.test`), as `server/internal/extmgr/repository_providers_test.go` does with a
fake provider.

## In the apps

The web's **Settings › Repositories** and a to-do's repository editor show the sources and their states (the second
step of #10330). iPhone and Android have no repository UI.

## What comes next (#10330)

The web's source list, link labels and "provider unavailable" state follow in the second step of #10330.
