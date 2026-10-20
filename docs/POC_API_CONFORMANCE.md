# PoC API conformance vs ai-grid-provider-backend

Status check of every API the **ai-grid-cloud-provider-poc** console uses
against what **ai-grid-provider-backend** exposes today (`/api/v1/`, proto +
grpc-gateway).

| | |
| --- | --- |
| PoC portal API | `ai-grid-cloud-provider-poc/backend/` — base `/api` |
| PoC UI clients | `HttpSubscriptionService`, `HttpUserService`, Keycloak token |
| PoC OpenAPI | `ai-grid-cloud-provider-poc/docs/openapi.json` |
| Production API | this repo — OpenAPI `docs/openapi/ai_grid.swagger.json` |
| Payload detail | [`POC_API_PAYLOAD_UI_MAPPING.md`](./POC_API_PAYLOAD_UI_MAPPING.md) |
| Standards | [`API.md`](./API.md) |

**Verdict:** Only a **partial** subset of PoC portal capabilities exists in
provider-backend. Clusters List/Update exist in a **non-conforming** OSAC shape.
Tenants are **List-only** and **non-conforming**. Models, locations,
subscriptions, users, and login token exchange are **not implemented** here.

---

## 1. Endpoint matrix

Legend:

| Status | Meaning |
| --- | --- |
| **Yes** | RPC/route exists and covers the PoC operation |
| **Partial** | Related capability exists; wire shape or behavior differs |
| **No** | Not implemented in provider-backend |
| **External** | PoC uses a sibling service (not this backend) |

### 1.1 Portal API (Node `/api` — PoC OpenAPI)

| PoC method | PoC path | PoC purpose | provider-backend | Status |
| --- | --- | --- | --- | --- |
| `GET` | `/api/health` | Git storage health | `GET /api/v1/ping` (liveness-style) | **Partial** |
| `GET` | `/api/clusters` | List `GridCluster[]` | `GET /api/v1/clusters` → `ManagedCluster` list envelope | **Partial** |
| `PATCH` | `/api/clusters/{name}` | Enroll `{ "aiGridEnabled": true }` | `PATCH /api/v1/clusters/{id}` label merge | **Partial** |
| — | — | Full ManagedCluster CRUD (not in PoC UI) | `GET/POST/PATCH/DELETE /api/v1/clusters…` | **Extra** (prod-only) |
| `GET` | `/api/tenants` | List tenants + IdP config | `GET /api/v1/tenants` → Keycloak orgs | **Partial** |
| `GET` | `/api/tenants/{name}` | Get tenant + `ETag` | — | **No** |
| `POST` | `/api/tenants` | Create tenant + IdP (+ Keycloak sync) | — | **No** |
| `PUT` | `/api/tenants/{name}` | Update domains/IdP + `If-Match` | — | **No** |
| `DELETE` | `/api/tenants/{name}` | Delete + `If-Match` (+ Keycloak sync) | — | **No** |
| `GET` | `/api/locations` | Deploy locations for models | — | **No** |
| `GET` | `/api/models` | List managed models | — | **No** |
| `POST` | `/api/models` | Create model + placement | — | **No** |
| `GET` | `/api/models/{name}` | Get model | — | **No** |
| `DELETE` | `/api/models/{name}` | Delete model + `If-Match` | — | **No** |
| `GET` | `/api/subscriptions` | List subscriptions | — | **No** |
| `POST` | `/api/subscriptions` | Create subscription (+ auth policy) | — | **No** |
| `GET` | `/api/subscriptions/{name}` | Subscription info (joined models/policies) | — | **No** |
| `PUT` | `/api/subscriptions/{name}` | Update + `If-Match` | — | **No** |
| `DELETE` | `/api/subscriptions/{name}` | Delete + `If-Match` | — | **No** |
| `GET` | `/api/openapi.json`, `/api/docs` | PoC docs | `/openapi.json`, `/docs/` | **Partial** (prod OpenAPI) |

### 1.2 Sibling APIs used by PoC UI (not in PoC OpenAPI)

| PoC method | PoC path | Purpose | provider-backend | Status |
| --- | --- | --- | --- | --- |
| `POST` | `/auth/token` | Keycloak password grant (Vite proxy) | — | **External** |
| `GET` | `/api/users` | List users | — | **External / No** |
| `POST` | `/api/users` | Create user | — | **External / No** |
| `GET` | `/api/users/me` | Current user | — | **External / No** |
| `GET` | `/api/users/{username}` | Get user | — | **External / No** |
| `PUT` | `/api/users/{username}` | Update user | — | **External / No** |
| `DELETE` | `/api/users/{username}` | Delete user | — | **External / No** |
| `PUT` | `/api/users/{username}/groups` | Replace groups | — | **External / No** |
| `PUT` | `/api/users/{username}/claims` | Set `tenant_id` claim | — | **External / No** |

PoC Users API lives outside this monorepo (`RedHat/keycloak/api`, proxied `:18082`).
It is still a **console dependency** and is **not** folded into provider-backend.

---

## 2. Implemented but non-conforming

These exist in provider-backend but **do not match** the PoC wire contract the
console was written against. A production console must adapt (preferred) or the
backend must add presentation facades.

### 2.1 Clusters — list

| | PoC | provider-backend |
| --- | --- | --- |
| Path | `GET /api/clusters` | `GET /api/v1/clusters` |
| Body | Flat array `GridCluster[]` | `{ items, size, total }` of `ManagedCluster` |
| Naming | camelCase | snake_case proto JSON (`UseProtoNames`) |
| Identity | `name` | `id` + `metadata.name` |
| Ready state | top-level `status`: Ready/Offline/Unknown | `status.available` / `status.available_condition` |
| AI Grid flag | `aiGridEnabled` boolean | label `metadata.labels["ai-grid/enabled"]` |
| Provisioned | `provisioned`, `provisioningStatus` | labels only (`ai-grid/provisioned`, …) — **not projected** as typed fields |
| Location | `location` string | label `location` only — **not projected** as typed field |
| Pagination | none (full list) | `offset` / `limit` supported |

**Conforms for:** hub ManagedCluster inventory exists; labels needed for enroll
are readable via `metadata.labels`.

**Does not conform for:** PoC `GridCluster` DTO, flat array response, derived
presentation fields (`status`, `aiGridEnabled`, `provisioned`,
`provisioningStatus`, `location`).

### 2.2 Clusters — enroll

| | PoC | provider-backend |
| --- | --- | --- |
| Path | `PATCH /api/clusters/{name}` | `PATCH /api/v1/clusters/{id}` |
| Body | `{ "aiGridEnabled": true }` only | Full/partial `ManagedCluster` + optional `update_mask` |
| Effect | Set label `ai-grid/enabled=true` | Merge `metadata.labels` (incl. `ai-grid/enabled`) |

**Conforms for:** enroll is achievable as a label patch (see README smoke
examples).

**Does not conform for:** PoC-strict enroll body; PoC returns a `GridCluster`
view after patch.

### 2.3 Tenants — list

| | PoC | provider-backend |
| --- | --- | --- |
| Path | `GET /api/tenants` | `GET /api/v1/tenants` |
| Source | Git tenant + OIDC IdP config | Keycloak Organizations only |
| Body | `Tenant[]` (name, domains, identityProvider, revision, status, timestamps) | `{ items, size, total }` of OSAC `Tenant` |
| Spec | domains + full OIDC IdP (secret-by-ref) | `spec.domains` only |
| Concurrency | `revision` / `ETag` / `If-Match` | none |
| Mutations | Create/Update/Delete in PoC | **List only** |

**Conforms for:** listing tenant-like identities with domains.

**Does not conform for:** IdP config, Git revision concurrency, name-keyed
resources, or any write path the PoC tenant wizard needs.

### 2.4 Health / ping

| | PoC | provider-backend |
| --- | --- | --- |
| Path | `GET /api/health` | `GET /api/v1/ping` |
| Response | `{ status, storage, autoCommit }` | Ping greeting payload |

**Partial:** useful for smoke; not a storage-health substitute.

### 2.5 Cross-cutting non-conformance

| Topic | PoC | provider-backend |
| --- | --- | --- |
| API prefix | `/api` | `/api/v1` |
| JSON names | mostly camelCase | snake_case |
| Object shape | ad-hoc DTOs | OSAC `id` + `metadata` + `spec` + `status` |
| Errors | `{ code, message, fieldErrors? }` | gRPC status → gateway HTTP errors |
| Optimistic lock | `If-Match` + `revision` string | `metadata.version` / `lock` (clusters; not fully enforced) |
| Auth on portal | none (loopback PoC) | not yet productized on these routes |
| Create success | often **201** + `Location` + `ETag` | typical **200** with body |
| Delete success | often **204** | empty gRPC delete response |

---

## 3. Not implemented (must be built or explicitly deferred)

Group by PoC domain. Detail and UI bindings:
[`POC_API_PAYLOAD_UI_MAPPING.md`](./POC_API_PAYLOAD_UI_MAPPING.md).

### 3.1 Tenants (writes + get)

Missing RPCs under `ai_grid.v1.Tenants` (today only `List`):

- `Get`, `Create`, `Update`, `Delete`
- PoC fields: `identityProvider` (OIDC URLs, client id, secret **reference**)
- PoC concurrency: `revision` / `If-Match`
- Optional Keycloak org sync side effects on create/delete (PoC → Users API)

**Product fork:** PoC = Git-stored tenant + IdP; backend = Keycloak Organization.
Need an explicit decision before cloning PoC 1:1.

### 3.2 Models

| Method | Path | Notes |
| --- | --- | --- |
| `GET` | `/api/models` | Managed model list for UI + subscription pickers |
| `POST` | `/api/models` | Create + MaaS registration + location placement |
| `GET` | `/api/models/{name}` | Detail |
| `DELETE` | `/api/models/{name}` | Delete + revision |

No `Models` proto/service in this repo.

### 3.3 Locations

| Method | Path | Notes |
| --- | --- | --- |
| `GET` | `/api/locations` | Ready locations with provisioned AI Grid nodes |

Derived from cluster labels / placements in PoC. No Locations API here.

### 3.4 Subscriptions

| Method | Path | Notes |
| --- | --- | --- |
| `GET` | `/api/subscriptions` | Full list |
| `POST` | `/api/subscriptions` | Create + optional matching auth policy |
| `GET` | `/api/subscriptions/{name}` | Joined models + policies + metering |
| `PUT` | `/api/subscriptions/{name}` | Update with revision |
| `DELETE` | `/api/subscriptions/{name}` | Delete with revision |

See PoC `docs/subscription-api-contract.md`. No `Subscriptions` proto/service.

### 3.5 Users & auth (sibling today)

Not in provider-backend. Console still needs them for `/login` and `/users*`:

- Token: `POST /auth/token` (Keycloak)
- Users CRUD, groups, claims, `me`

**Options:** fold into this service as `Users`, keep a dedicated IdP API, or
proxy — undecided.

---

## 4. Implemented extras (not in PoC portal OpenAPI)

Useful for ops / future console, not required by current PoC pages:

| Method | Path | Notes |
| --- | --- | --- |
| `GET` | `/api/v1/clusters/{id}` | Single ManagedCluster |
| `POST` | `/api/v1/clusters` | Create hub ManagedCluster CR stub |
| `DELETE` | `/api/v1/clusters/{id}` | Delete ManagedCluster |
| `GET` | `/api/v1/ping` | Ping |

PoC cluster UX is list + enroll only; it does not create/delete CRs via the
portal API.

---

## 5. Coverage summary

| Domain | PoC console needs | In provider-backend? | Conforms to PoC wire? |
| --- | --- | --- | --- |
| Health | yes | Partial (`ping`) | No |
| Clusters list | yes | Yes | **No** (OSAC shape) |
| Clusters enroll | yes | Yes (via label PATCH) | **No** (body/DTO) |
| Tenants list | yes | Yes | **No** (org vs Git+IdP) |
| Tenants CRUD | yes | **No** | — |
| Models | yes | **No** | — |
| Locations | yes | **No** | — |
| Subscriptions | yes | **No** | — |
| Users | yes | **No** (external PoC) | — |
| Login token | yes | **No** (Keycloak direct) | — |

Rough count (portal OpenAPI operations only): **~18** PoC operations →
**~2** fully present as routes with related semantics (clusters list + update),
both **non-conforming** for the PoC UI without an adapter; tenants list is a
**third** partial; everything else is missing.

---

## 6. Suggested implementation order

1. **Console adapter** for Clusters (map OSAC ↔ `GridCluster`) *or* add a
   presentation facade — unblocks `/clusters` against today’s API.
2. **Tenants product decision** (Keycloak-only vs Git+IdP), then Get/Create/
   Update/Delete + domains (+ IdP if required).
3. **Models + Locations** (subscription picker dependency).
4. **Subscriptions** (+ auth-policy side effects).
5. **Users** ownership decision (absorb vs keep sibling).

Keep proto/OSAC as the long-term contract (`API.md`); treat PoC camelCase DTOs
as a migration surface, not the production source of truth.

---

## 7. Related reading

- Payload/UI detail: [`POC_API_PAYLOAD_UI_MAPPING.md`](./POC_API_PAYLOAD_UI_MAPPING.md)
- API standards: [`API.md`](./API.md)
- PoC OpenAPI: `ai-grid-cloud-provider-poc/docs/openapi.json`
- PoC subscription contract: `ai-grid-cloud-provider-poc/docs/subscription-api-contract.md`
