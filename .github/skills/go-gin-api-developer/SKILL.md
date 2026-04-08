---
name: go-gin-api-developer
description: 'Develop and extend the EdgeCDN-X Go Gin API. Use for adding routes, wiring CLI flags, building reusable app components, injecting shared dependencies into modules, following existing config and module patterns, and validating changes with focused Go tests.'
argument-hint: 'Describe the API change, endpoint, module, or reusable component to add'
user-invocable: true
---

# Go Gin API Developer

## When To Use
- Add or change Gin routes in this API
- Introduce reusable runtime components in the `app` package
- Wire new CLI flags through bootstrap and config
- Inject shared dependencies into modules without broad coupling
- Add focused tests for handlers or reusable HTTP clients
- Extend admin, auth, project, service, or zone modules while preserving current patterns

## Outcome
Produce a minimal, consistent Go API change that:
- follows the repo's existing bootstrap and module structure
- keeps dependencies explicit and reusable
- avoids unrelated refactors
- validates behavior with focused tests and compile checks

## Procedure
1. Find the integration point first.
   - Read the entrypoint and app bootstrap before editing.
   - For this repo, start with `src/main.go`, `src/config/config.go`, and `src/modules/app/`.
   - If the change affects a module, read that module's `module.go` and `routes.go` before deciding where logic belongs.

2. Match the existing pattern instead of inventing a new one.
   - Put reusable runtime concerns in the `app` package.
   - Keep module-specific HTTP handlers inside the owning module.
   - Thread new settings through CLI flags and config structs rather than hardcoding values.

3. Make dependency flow explicit.
   - Prefer dependency injection over hidden globals when a module needs shared runtime state.
   - If multiple modules may need a shared client, expose it from `app.App` and inject it during module registration.
   - Keep interfaces or opt-in capability checks narrow when only some modules need the dependency.

4. Keep the change small and local.
   - Preserve current public APIs unless the task requires a change.
   - Avoid renaming unrelated code or reformatting untouched files.
   - Prefer the smallest patch that satisfies the behavior.

5. Add the endpoint or component.
   - For HTTP endpoints, register routes close to existing routes in the target module.
   - Prefer inline function definitions on Gin routes for single-route behavior instead of extracting a separate handler method.
   - Extract a shared handler method only when the logic is reused or the inline function would become hard to read.
   - Reuse existing auth middleware and authorization builders.
   - Return stable JSON responses and map errors to clear HTTP status codes.
   - For external HTTP clients, validate configuration up front and fail early on invalid input.

6. Validate the exact behavior.
   - Run formatting on changed Go files.
   - Run focused tests first, for example the specific package touched.
   - Use targeted error checking on edited files if available.
   - If a new client or handler was added, cover the happy path and at least one failure path.

7. Summarize what changed and how to use it.
   - Mention the new flag, route, or injected dependency.
   - Show the exact query or endpoint behavior when relevant.
   - Note what was validated and what was not run.

## Decision Rules

### When adding a new configurable runtime dependency
- Add a CLI flag in `src/main.go`
- Store it in `src/config/config.go`
- Initialize the shared client in `src/modules/app/`
- Inject it into modules that need it

### When adding a new route to an existing module
- Keep route registration in that module's `routes.go`
- Keep dependency fields on the module struct in `module.go`
- Prefer inline handler functions in `group.GET`, `group.POST`, and similar Gin route declarations
- Reuse existing middleware and authz patterns

### When the module needs a shared app dependency
- Add a narrow capability method, such as a setter for that dependency
- Inject it from `app.App.RegisterModule`
- Avoid making unrelated modules aware of the new dependency

### When writing tests
- Prefer package-focused tests over whole-repo test runs
- Use `httptest` for Gin handlers and external HTTP clients
- Assert the exact route path, query string, and response status

## Quality Checks
- Is the config wired from CLI to runtime without hardcoding?
- Is the new logic placed in the smallest responsible package?
- Can the module use the shared dependency without a new global singleton?
- Does the Gin route use an inline handler unless extraction is justified by reuse or complexity?
- Does the route return consistent JSON and useful status codes?
- Are there focused tests for the new behavior?
- Were changed files formatted?

## Repo-Specific Notes
- The main bootstrap is in `src/main.go`
- Shared runtime helpers live in `src/modules/app/`
- Modules follow `module.go` plus `routes.go`
- Authorization commonly uses `auth.NewAuthzBuilder()` on route registration
- Prefer inline route handlers in `routes.go` for endpoint-specific logic
- Minimize coupling between modules; inject shared clients instead

## Example Prompts
- `/go-gin-api-developer add an admin endpoint that queries a shared client`
- `/go-gin-api-developer add a CLI flag and wire it through app config`
- `/go-gin-api-developer create a reusable HTTP client in app and inject it into modules`
- `/go-gin-api-developer add a Gin route with focused httptest coverage`