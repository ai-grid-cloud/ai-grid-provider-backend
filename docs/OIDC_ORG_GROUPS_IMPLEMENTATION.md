# Org groups & shared OIDC — implementation brief

**Branch:** `docs/oidc-org-groups-plan`  
**Created:** 2026-10-27  
**Updated:** 2026-10-28 (Lindani Slack sync)  
**Status:** Implementation brief for provider-backend + Keycloak IdP linking (not an ADR)

This combines:

- Working notes in `AI_GRID_OIDC_IDENTITY_AND_API_PLAN.md` (mvp workspace)
- Oct 27 Working Group / CE standup takeaways (RJ + Lindani: finalize group org structure)
- Findings from [kahowell/ai-grid-tenant `apps/keycloak`](https://github.com/kahowell/ai-grid-tenant/tree/main/apps/keycloak)
- **Oct 28 Lindani Slack** (below)

Primary contract: [ODH-ADR-MS-0007](https://github.com/opendatahub-io/architecture-decision-records/blob/main/architecture-decision-records/model-serving/ODH-ADR-MS-0007-ai-grid-shared-oidc-identity-contract.md).

---

## 0. Lindani sync updates (2026-10-28) — read first

| Topic | What we thought | What Lindani said | Implication |
| --- | --- | --- | --- |
| Org ids on tokens | “Tokens don’t have `org_id` yet; Phase 1 Keycloak claims first” | **Tokens should already have org ids.** It’s in the ADR. **His Keycloak setup already generates that shape.** | Do **not** wait on a future Phase 1 to match. Match ADR / his token shape **now** on subscriptions (`organization.id` + `org/group`). |
| Creating orgs | Tenants API talks **directly** to Keycloak Admin API today | **GitOps would be better** for org-related APIs | Direction change for Tenants: prefer persist → GitOps → automation over live Keycloak CRUD as the long-term model. |
| Keycloak GitOps | Working Group: don’t productize KC GitOps | There **is** a Keycloak GitOps setup ([`apps/keycloak`](https://github.com/kahowell/ai-grid-tenant/tree/main/apps/keycloak)) but it **“was not meant to be the real thing”** | Use it as the **reference/demo** for claim shape and IdP linking; don’t treat it as production org lifecycle yet. Design the “real” GitOps org path with him Monday+. |

**Answer to “as long as I match it it should work?”:**  
Yes for **subscription / AuthPolicy subject matching**: if `organization.id` and qualified groups match what his tokens put in `https://redhat.com/maas/orgs` and `groups`, MaaS can tie the transaction together. That does **not** by itself mean Tenants Create should keep calling Keycloak forever — org **lifecycle** he prefers via GitOps.

**Monday sync:** already agreed; use it for GitOps org create design + any claim quirks (tenant-2 still wrong in realm JSON).

---

## 1. Goal

Make subscription **organization id** and **owner groups** line up with the shared OIDC broker (ADR + Lindani’s Keycloak token shape) so MaaS can authorize org users — without breaking API keys or OpenShift console admin.

**In scope for this repo:** catalog write rules, validation, docs, tests; Tenants direction toward GitOps (design, not necessarily this PR).  
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
| **Keycloak `apps/keycloak`** | Reference IdP link + ADR claim emit (Lindani’s setup already does org ids); **not** final prod org GitOps |
| **Future org GitOps** | Preferred path to **create/update orgs** (Lindani); TBD reconciler vs realm import |
| **MaaS gateway / maas-api** | Validate JWT; inject headers; org-scoped authz |
| **This backend** | Match token org id on subscriptions; qualify `owner.groups` → `org/group` in git/AuthPolicy; today Tenants still hit Keycloak Admin API |

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

### A. IdP / Keycloak (Lindani owns; RJ consumes shape)

**A0. Treat claim emit as done for planning** (Lindani 2026-10-28)

- His Keycloak already mints ADR-shaped tokens with org ids.
- RJ’s job: **match** `organization_id` / qualified groups on subscriptions — not wait for “Phase 1 someday.”
- Still fix tenant-2 inconsistency in realm JSON when touching Keycloak.
- Real Entra link remains Lindani’s experiment track (Monday sync).

**A1. Org lifecycle → GitOps (direction, Monday+)**

- Prefer **GitOps** over provider → Keycloak Admin API for org create.
- Today: `internal/keycloak` `CreateOrganization` used by Tenants service.
- Target (design with Lindani): Tenants (or catalog) write desired org → GitOps/automation applies to Keycloak; demo `apps/keycloak` is scaffold only.

---

### B. This backend (provider API / catalog) — do now

**B1. Match token org id on subscriptions (unblocked)**

- [ ] Document that `Subscription.spec.organization.id` **must** equal token `https://redhat.com/maas/orgs[].organization_id` (e.g. alice’s `42c3e46f-…`).
- [ ] Confirm `qualifiedOwnerGroups` + tests for UUID orgs + bare groups → file `org/group`.
- [ ] Optional fixture aligned with alice.

**B2. Docs**

- [ ] README / UI_API_REFERENCE: bare groups in API; `org/group` on wire; link ADR + this brief.
- [ ] Correct any “tokens don’t have org yet” language — Lindani: they should / his setup does.

**B3. Tenants / org create (plan, don’t big-bang without Monday)**

- [ ] Keep current Keycloak Admin create working for MVP demos if needed.
- [ ] Spike/design GitOps org create with Lindani (what YAML, who reconciles, how id is allocated so subscriptions can reference it).
- [ ] Do **not** assume `apps/keycloak` realm import is the production write path.

**B4. Console**

- [ ] Subscription UX: org id from tenant/org that matches token claim; bare group names.

**B5. Non-goals**

- No JWT validation in this service.
- No treating demo Keycloak GitOps as production org lifecycle without a design.
- No org-global rate-limit field.

**Exit (near-term):** Subscription with alice’s org id + `ai-grid-users` → AuthPolicy `42c3e46f-…/ai-grid-users`. Org GitOps is a follow-up design, not a blocker for matching.

---

### C. MaaS (dependency; not this PR)

- Phase 2: inject `x-maas-orgid` / `x-maas-roles` when claim present (`strictIdentityContractConfigured`).
- Phase 3: dual-read bare vs qualified groups; org gate when header present.
- Phase 5: require org claim only after Keycloak emit is live.

Provider work (B) can proceed in parallel; **end-to-end org SSO** needs A then C.

---

## 6. Monday sync agenda (with Lindani)

1. Confirm sample token (alice / his env) — decode `groups` + `maas/orgs` together.
2. Org id source of truth when Tenants create via GitOps vs today’s Keycloak Organization UUID.
3. Shape of GitOps org create (what lands in git; who applies to Keycloak; how console gets the id).
4. How far demo `apps/keycloak` should go vs “real” setup.
5. Entra link timeline; tenant-2 claim cleanup.
6. Anything else for Provider Console tenants finish-up.

---

## 7. Suggested PR sequence

| # | Repo | Change |
| --- | --- | --- |
| 1 | **This repo** | This brief + subscription match docs/tests (B1–B2) |
| 2 | **This repo** / design doc | Tenants GitOps org-create spike (B3) after Monday |
| 3 | ai-grid-tenant | tenant-2 fix + Entra (Lindani) |
| 4 | models-as-a-service | Gateway/API if still gaps (C) |
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
