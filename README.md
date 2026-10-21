# EdgeCDN-X API

The API operates on the EdgeCDN-X Kubernetes CRDs. Its placement configuration must match the controller.

## Feature Gates

The Services module is disabled by default. Enable it explicitly with the same gate used by the controller:

```sh
go run ./src --feature-gates=cachingreconciler
```

`--feature-gates` accepts comma-separated gate names. Without `cachingreconciler`, the Services module is not initialized and its HTTP routes are not registered (requests return 404). Other modules remain enabled. Enable the gate on both the API and controller when using Services. This setting does not change the UI configuration.

## Project Placement

Namespace-per-project placement is the default:

```sh
go run ./src --namespaced=true
```

The tenant ID is the Project's `metadata.name`, which is also its namespace. Project creation provisions that namespace before creating the Project. The default admin Project also receives its own namespace.

For a shared namespace, create the namespace beforehand and run:

```sh
go run ./src --namespaced=false --target-namespace=edgecdnx
```

`--namespace` remains an alias for `--target-namespace`; it only applies to shared placement. Existing OIDC, database, and other application configuration still applies.

## Ownership And Informers

All non-Project resources use `spec.project.name` as their authoritative tenant ownership. Labels are not an authorization or tenant-filtering mechanism. Creates set ownership on the server, and resource reads, updates, and deletes reject resources belonging to another Project. DNS endpoint membership in a Zone uses `spec.zone.name`.

Resource informers watch all namespaces in dedicated placement, or only the configured namespace in shared placement. Tenant lists use a composite namespace/Project index. Admin views intentionally list across tenant namespaces, subject to admin authorization. List responses are eventually consistent; direct reads and mutations use the API server.

API-key authentication resolves keys by indexed object name and credential digest across namespaces, then validates Project placement and the Project owner UID. Multiple matching credentials across namespaces are rejected. Project informer policies are restricted to their own tenant domain and removed when the Project is deleted.

## Kubernetes Permissions

Dedicated placement requires cluster-wide access to the namespaced EdgeCDN-X CRDs, including `get`, `list`, and `watch` for informers and appropriate create/update/delete permissions for API operations. The API also needs cluster-scoped `get` and `create` permissions on core `namespaces`. It does not need namespace deletion permission. Shared placement can scope tenant resource permissions to the target namespace.

Project creation will not adopt an unrelated existing namespace. Retries can reuse namespaces labeled `app.kubernetes.io/managed-by=edgecdnx-api` and `edgecdnx.com/project-name=<project-name>`. Namespace-provisioning or Project-creation failures are returned to the caller. A namespace created before a failed Project request is deliberately retained for a retry, rather than deleted automatically.

Namespace-per-project placement does not automatically configure Kubernetes RoleBindings for end users. Shared namespaces do not provide Kubernetes RBAC isolation between Projects; restrict direct Kubernetes access and enforce authorization through the API.

## Migration Notes

Install compatible controller CRDs and admission webhooks first. Backfill `spec.project.name` on existing resources, including Locations, and rename API-key `spec.projectRef` to `spec.project`. Legacy labels alone no longer make a resource visible to a tenant. Move resources to their Project namespaces before enabling dedicated placement.

Routing selectors remain Kubernetes label selectors for selecting nodes; they are not the API's tenant ownership filter. Review `--default_route_selector` (the existing default selects `project=global`) and ensure external DNS/router/healthchecker consumers independently enforce the new Project boundaries. The API cannot enforce tenant isolation inside those separate components.

The controller requires a same-Project Zone covering each generated Service domain and host alias. The API's shared `--service_base_domain` does not provision those Zones or allocate tenant subdomains. Establish a per-tenant DNS/Zone strategy before relying on automatic Service DNS generation.

## Validation

```sh
go test ./...
go vet ./...
```