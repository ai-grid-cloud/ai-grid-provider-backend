# Org groups & shared OIDC — implementation brief

**Branch:** `docs/oidc-org-groups-plan`  
**Created:** 2026-10-27  
**Status:** Implementation brief for provider-backend + Keycloak IdP linking (not an ADR)

This combines:

- Working notes in `AI_GRID_OIDC_IDENTITY_AND_API_PLAN.md` (mvp workspace)
- Oct 27 Working Group / CE standup takeaways (RJ + Lindani: finalize group org structure)
- Findings from [kahowell/ai-grid-tenant `apps/keycloak`](https://github.com/kahowell/ai-grid-tenant/tree/main/apps/keycloak)

Primary contract: [ODH-ADR-MS-0007](https://github.com/opendatahub-io/architecture-decision-records/blob/main/architecture-decision-records/model-serving/ODH-ADR-MS-0007-ai-grid-shared-oidc-identity-contract.md).

---

## 1. Goal

Make subscription **organization id** and **owner groups** line up with the shared OIDC broker so MaaS can authorize org users from ADR-shaped tokens — without breaking API keys, OpenShift console admin, or today’s bare-group SSO until IdP claims are live.

**In scope for this repo:** catalog write rules, validation, docs, tests, and a clear contract for Keycloak.  
**Out of scope here:** gateway AuthPolicy (MaaS), maas-api dual-read, Dex, provider CRUD JWT authn.

---

## 2. Architecture (who does what)

```text
  [grid-tenant-N-idp]          stub or real org IdP (Entra later)
           |
           | OIDC broker
           v
  [saas realm]  -------------->  JWT: groups + https://redhat.com/maas/orgs
           |                     (+ aud / token_use for MaaS)
           v
  [MaaS gateway] ------------->  x-maas-* headers
           |
           v
  [maas-api]  <---------------  catalog AuthPolicy subjectGroups
                                  (org/group from provider git write)
```

| Layer | Responsibility |
| --- | --- |
| **Keycloak `apps/keycloak`** | Link IdPs; emit ADR claims (Phase 1) |
| **MaaS gateway / maas-api** | Validate JWT; inject headers; org-scoped authz (Phases 2–3) |
| **This backend** | `organization.id` + qualify `owner.groups` → `org/group` in git/AuthPolicy (Phase 4) |

Provider-console admin SSO (kube-auth-proxy) is a **different** path. Do not put org-user JWT validation in this service for MVP.

---

## 3. Wire contract (locked)

| Item | Value |
| --- | --- |
| Qualified group | `{organization_id}/{group}` (separator **`/`**) |
| Org claim | `https://redhat.com/maas/orgs` → `[{organization_id, organization_name?, roles[]}]` |
| MVP role | `admins` (org-scoped; not from API keys) |
| User id | `iss` + `sub` (email optional) |
| Broker | Keycloak (not Dex) |
| ADR `:` example | Treat as errata; align on `/` |

**Already in this repo:** `qualifiedOwnerGroups` in `internal/servers/subscriptions_git.go` writes `org + "/" + name` into catalog files; API may accept bare or same-org-qualified names and still returns bare names on read.

---

## 4. Keycloak app — what you were pointed at

**Location:** [ai-grid-tenant/apps/keycloak](https://github.com/kahowell/ai-grid-tenant/tree/main/apps/keycloak)  
**Origin:** Migrated from `lphiri/maas-portal-integration-poc`.

### 4.1 Realms

| Realm | Role |
| --- | --- |
| `saas` | Shared broker / MaaS-facing issuer (`AITenant` OIDC points here) |
| `grid-tenant-1-idp` | Stand-in IdP for org 1 |
| `grid-tenant-2-idp` | Stand-in IdP for org 2 |

Flow: user logs into tenant IdP → `saas` brokers that IdP → `saas` issues the JWT MaaS trusts.

### 4.2 Claim pipeline (already sketched)

1. Tenant IdP client mappers emit `groups` and `https://redhat.com/maas/orgs`.
2. `saas` IdP mappers copy them into user attrs `maas_groups` / `maas_orgs_json`.
3. `saas` client mappers put those attrs on the **access token**, plus audience `tenant-demo` and `token_use=access`.

### 4.3 Demo data (align with provider)

**Tenant 1 (`alice`) — ADR-shaped (use as reference):**

- Org id: `42c3e46f-2477-44d7-a85b-d3b43f6b31fa`
- Groups: `42c3e46f-…/ai-grid-admins`, `42c3e46f-…/ai-grid-users`
- `maas_orgs_json`: `[{"organization_id":"42c3e46f-…","roles":["admins"]}]`

**Tenant 2 (`bob`) — inconsistent (fix in Keycloak work):**

- Group form `grid-tenant-2:ai-grid-users` (`:` and non-UUID)
- Org JSON uses `tenant_id` instead of `organization_id`

### 4.4 Operational constraints

- Realm JSON import runs on **first start** only; Keycloak skips realms already in Postgres. Later JSON edits need Admin API / planned export-import ([README](https://github.com/kahowell/ai-grid-tenant/blob/main/apps/keycloak/README.md)).
- Working Group: do **not** productize “manage Keycloak entirely via GitOps” for customers; still OK to improve demo realm imports and document live mapper changes.
- Secrets stay in ExternalSecrets (`keycloak-secrets`); broker client secrets are generated, not in Git.

---

## 5. Work breakdown

### A. IdP / Keycloak (Lindani + RJ sync; changes in ai-grid-tenant)

**A1. Link / replace stub IdPs**

- Keep `saas` as the shared issuer.
- For real Entra/Verizon: add or replace `identityProviders` entries (same pattern as `grid-tenant-1` / `grid-tenant-2`).
- Keep IdP → attr → token mapper pipeline for `groups` + `maas/orgs`.

**A2. Normalize claim shape**

- Emit only `{organization_id}/{group}` in `groups` (or dual-emit during migration — decide in sync).
- Emit ADR org JSON (`organization_id` + `roles`); fix tenant-2.
- Confirm `admins` assignment (org role vs group membership).

**A3. Prove a token**

- Login as alice (and later Entra user); decode access token; confirm claims before tightening MaaS.

**Exit:** Sample JWT matches §3; tenant-2 no longer uses `:` / `tenant_id`.

---

### B. This backend (provider API / catalog)

**B1. Contract check vs Keycloak org ids**

- [ ] Compare `qualifiedOwnerGroups` / `subscriptionNameRE` validation to Keycloak org UUIDs (`42c3e46f-…`). UUIDs are DNS-label-safe today; document that `organization.id` **must** equal Keycloak / `maas_orgs` `organization_id`.
- [ ] Reject or document cross-org qualified groups (already InvalidArgument if prefix ≠ org).

**B2. Docs**

- [ ] README / UI_API_REFERENCE / subscription comments: bare groups in API; `org/group` on wire/catalog; link ADR + this brief.
- [ ] Note separator `/` and that ADR `:` example is wrong.

**B3. Tests / fixtures**

- [ ] Ensure git subscription tests cover UUID org ids + bare/`same-org` qualified groups (extend if missing).
- [ ] Optional: fixture subscription for `42c3e46f-…` + `ai-grid-users` matching alice.

**B4. Console (separate repo, track here)**

- [ ] Create/edit subscription UX: org id = Keycloak org UUID; group picker sends bare names.
- [ ] Do not invent K8s groups for org end-users.

**B5. Explicit non-goals**

- No JWT validation / `maas/orgs` parsing in this service.
- No Keycloak realm CRUD API.
- No org-global rate-limit field (usage is observability elsewhere).

**Exit:** Creating a subscription with alice’s org id + `ai-grid-users` writes AuthPolicy subjects `42c3e46f-…/ai-grid-users`.

---

### C. MaaS (dependency; not this PR)

- Phase 2: inject `x-maas-orgid` / `x-maas-roles` when claim present (`strictIdentityContractConfigured`).
- Phase 3: dual-read bare vs qualified groups; org gate when header present.
- Phase 5: require org claim only after Keycloak emit is live.

Provider work (B) can proceed in parallel; **end-to-end org SSO** needs A then C.

---

## 6. Lindani sync agenda (resolve before coding hard)

1. Org id source of truth: Keycloak Organization id vs hardcoded UUID in realm JSON vs provider Tenants API id.
2. Emit qualified-only vs dual bare+qualified during dual-read.
3. How `admins` is represented in Keycloak.
4. Entra link target: replace `grid-tenant-*-idp` or add a third IdP alias.
5. Who updates live hub realms (import-once constraint).
6. Whether provider UI needs org usage in MVP (Working Group: monitor/expose logs, not org rate limits).

---

## 7. Suggested PR sequence

| # | Repo | Change |
| --- | --- | --- |
| 1 | **This repo** | This brief + subscription docs/tests alignment (B1–B3) |
| 2 | ai-grid-tenant | Fix tenant-2 claim shape; document IdP link steps (A2) |
| 3 | ai-grid-tenant | Entra / real IdP broker entry (A1) — with Lindani |
| 4 | models-as-a-service | Gateway/API dual-read (C) — MaaS owners |
| 5 | ai-grid-provider-console | Org id + bare groups UX (B4) |

---

## 8. Quick reference — code & files

| What | Where |
| --- | --- |
| Qualify groups on git write | `internal/servers/subscriptions_git.go` → `qualifiedOwnerGroups` |
| Subscription org field | `proto/ai_grid/v1/subscription_type.proto` → `SubscriptionOrganization` |
| SaaS realm + IdP links | `ai-grid-tenant/apps/keycloak/manifests/realms/saas-realm.json` |
| Tenant-1 ADR demo user | `…/grid-tenant-1-idp-realm.json` (`alice`) |
| Tenant-2 to fix | `…/grid-tenant-2-idp-realm.json` (`bob`) |
| Hub AITenant OIDC | `ai-grid-tenant/apps/rhoai/hub/02-aitenant-oidc-policy.yaml` |
| MaaS org claim envelope | `models-as-a-service/.../maasauthpolicy_identity.go` |

---

## 9. Related meeting actions (2026-10-27)

- **[RJ] Organize groups with Lindani** — this brief is the working package for that.
- Keycloak **not** managed as customer GitOps product requirement.
- Merge models/subscriptions / UI integration PRs onto canonical cluster separately from identity claim work.
- OSAC Postgres+reconciler for subscriptions is a **later** architecture track, not this brief.
