---
name: coroot-rebase-upstream
description: Синхронизация ветки add_mongodb_tls-2 с upstream coroot тэгом через candidate-ветку: точный SHA тэга, отдельная детекция SNI-gap и generic TLS, тесты/сборка до пуша, публикация только после явного подтверждения
---

# coroot-rebase-upstream — Синхронизация MongoDB TLS/SNI форка с upstream

## Overview

This skill synchronizes the fork branch `add_mongodb_tls-2` with an upstream `coroot/coroot` release tag.

The workflow is **candidate-first**: work happens on a throwaway candidate branch created from the exact tag SHA. The shared branch `add_mongodb_tls-2` is never rewritten until the candidate is validated and the user explicitly confirms publication.

Use it when you need to:

- rebase `add_mongodb_tls-2` onto a specific upstream tag
- check whether upstream already covers generic MongoDB TLS (UI/docs) and, separately, whether the exact SNI gap (`sni` in `collector.ApplicationInstrumentation`) is still open
- validate with tests and a local image build before any push
- build and publish the image `ghcr.io/tribunadigital/coroot` after explicit confirmation

Required input:

- `TAG` — upstream tag, for example `v1.25.0`

Hardcoded constants used by this skill:

- Upstream URL: `https://github.com/coroot/coroot.git`
- Upstream remote name: `upstream`
- Fork (shared) branch: `add_mongodb_tls-2`
- Backup ref: `add_mongodb_tls-2-pre-rebase-${TAG}` (immutable once created)
- Candidate branch: `add_mongodb_tls-2-v${VERSION}-candidate`
- Docker registry: `ghcr.io/tribunadigital/coroot`
- Image tag: `${VERSION}-mongo-tls-2` (for `v1.25.0` → `ghcr.io/tribunadigital/coroot:1.25.0-mongo-tls-2`)
- Dockerfile: `dev.dockerfile` (always the upstream tag's version — never carry fork Dockerfile drift)

## Fork patch scope (what the candidate carries)

The fork differs from upstream by exactly two things:

1. **SNI transport patch** — `collector/config.go` (+ `collector/config_test.go`): JSON `sni` on `collector.ApplicationInstrumentation`, emitted only for MongoDB instrumentation on non-Kubernetes physical instances backed by an installed node-agent, using the trusted node telemetry hostname (`instance.Node.Name.Value()`, guarded by `Node.IsAgentInstalled()`, `instance.Pod == nil`). Never synthesized for Kubernetes pods, non-Mongo instrumentation, or node-less synthetic external instances.
2. **Operational files** — `.opencode/skills/coroot-rebase-upstream/SKILL.md` and `.opencode/commands/coroot-rebase-upstream.md`.

The candidate must **NOT** carry any of these historical fork changes:

- old TLS UI in `front/src/components/ApplicationInstrumentation.vue` (commit `b77a889`)
- old TLS docs in `docs/docs/databases/mongodb.md` (commit `b77a889`)
- Dockerfile drift in `dev.dockerfile` (commit `bf0e5b4`)

Upstream `front/` and `docs/` are always preserved as-is. On any conflict there, take the upstream side.

## Prerequisites

Before starting, verify all of the following:

```bash
# Check tools available
git --version || { echo "ERROR: git not found"; exit 1; }
docker --version || { echo "ERROR: docker not found"; exit 1; }
pkg-config --exists liblz4 || echo "WARNING: liblz4-dev missing — go test ./... will fail (cgo golz4); install liblz4-dev before validation"

# Check clean working directory
if [ -n "$(git status --porcelain)" ]; then
  echo "ERROR: Working directory has uncommitted changes. Commit or stash before proceeding."
  git status --short
  # STOP
fi

# Verify correct repository
ORIGIN=$(git remote get-url origin 2>/dev/null)
if ! echo "$ORIGIN" | grep -q "tribunadigital/coroot"; then
  echo "ERROR: Not in tribunadigital/coroot repo. Current origin: $ORIGIN"
  # STOP
fi
```

Additional checks:

- confirm the current shell can reach `github.com`
- confirm the `origin` remote exists
- confirm the user is willing to rebase `add_mongodb_tls-2`

If any prerequisite fails, stop immediately.

## Workflow

### Step 1: Configure Upstream Remote

```bash
UPSTREAM_URL=$(git remote get-url upstream 2>/dev/null)
if [ -z "$UPSTREAM_URL" ]; then
  git remote add upstream https://github.com/coroot/coroot.git
else
  git remote set-url upstream https://github.com/coroot/coroot.git
fi
```

If this command fails, report the error and STOP.

### Step 2: Fetch Upstream Tags

```bash
git fetch upstream --tags
```

If fetch fails, assume a network or access problem and STOP.

### Step 3: Validate Tag and Resolve Exact SHA

```bash
git rev-parse --verify "refs/tags/${TAG}" >/dev/null 2>&1
```

If the tag is missing, show the latest tags (`git tag --sort=-version:refname | head -20`) and STOP.

Resolve and pin the exact commit SHA — the candidate branches from the SHA, never from the mutable tag name:

```bash
UPSTREAM_SHA=$(git rev-parse "${TAG}^{commit}")
echo "Tag ${TAG} = ${UPSTREAM_SHA}"
```

### Step 4: Extract Version Number

```bash
VERSION=$(echo "${TAG}" | sed 's/^v//')
# v1.25.0 → 1.25.0
```

Use this version for the candidate branch name and the Docker image tag.

### Step 5: Detect Gaps — generic TLS and exact SNI are SEPARATE checks

Run both checks against `UPSTREAM_SHA`'s tree, not the current branch. Do NOT merge them into one score.

#### 5a. Generic upstream MongoDB TLS (UI/docs/params)

```bash
# Indicator 1: TLS annotation in MongoDB docs
INDICATOR_DOCS=$(git show ${UPSTREAM_SHA}:docs/docs/databases/mongodb.md 2>/dev/null | grep -i "mongodb-scrape-param-tls\|mongodb-scrape-tls-secret")

# Indicator 2: TLS UI element in ApplicationInstrumentation
INDICATOR_UI=$(git show ${UPSTREAM_SHA}:front/src/components/ApplicationInstrumentation.vue 2>/dev/null | grep -iE "mongodb.*tls|mongo.*tls|'tls'")

GENERIC_TLS=NO
[ -n "$INDICATOR_DOCS" ] && [ -n "$INDICATOR_UI" ] && GENERIC_TLS=YES
```

- If `GENERIC_TLS=YES` → upstream covers generic MongoDB TLS. Upstream UI and docs are preserved untouched; the fork's old TLS UI/docs (`b77a889`) must NOT be reapplied. No user decision needed — this is informational.
- If `GENERIC_TLS=NO` → report that upstream lacks generic TLS support; continue (the fork keeps relying on upstream params for in-cluster TLS; UI/docs still stay upstream-only).

#### 5b. Exact SNI gap in collector instrumentation config

```bash
INDICATOR_SNI=$(git show ${UPSTREAM_SHA}:collector/config.go 2>/dev/null | grep 'json:"sni"')
SNI_GAP=YES
[ -n "$INDICATOR_SNI" ] && SNI_GAP=NO
```

- If `SNI_GAP=YES` → the exact gap is open; the candidate must carry the SNI patch from the fork branch (`collector/config.go`, `collector/config_test.go`). Continue.
- If `SNI_GAP=NO` → upstream implements SNI itself. Show upstream's implementation to the user and ask: apply ours, drop ours, or reconcile. STOP — wait for user response.

Key rule: `GENERIC_TLS=YES` does **not** close the SNI gap, and `SNI_GAP=NO` says nothing about UI/docs. Never use a combined indicator count.

### Step 6: Create Immutable Backup Ref

The backup pins the current shared branch tip. It must already exist if this TAG was attempted before:

```bash
BACKUP="add_mongodb_tls-2-pre-rebase-${TAG}"
EXPECTED_TIP=$(git rev-parse add_mongodb_tls-2)
if git show-ref --verify --quiet "refs/heads/${BACKUP}"; then
  if [ "$(git rev-parse ${BACKUP})" != "$EXPECTED_TIP" ]; then
    echo "ERROR: ${BACKUP} exists at $(git rev-parse ${BACKUP}) but shared branch tip is ${EXPECTED_TIP}. Resolve manually."
    # STOP
  fi
else
  git branch "${BACKUP}" "${EXPECTED_TIP}"
fi
```

If backup creation fails for any other reason, STOP. Never move or delete an existing backup ref.

### Step 7: Create/Reset Candidate Branch from Exact SHA

```bash
CANDIDATE="add_mongodb_tls-2-v${VERSION}-candidate"
if git show-ref --verify --quiet "refs/heads/${CANDIDATE}"; then
  git rev-parse ${CANDIDATE} | grep -q "^${UPSTREAM_SHA}" || git branch -f "${CANDIDATE}" "${UPSTREAM_SHA}"
else
  git branch "${CANDIDATE}" "${UPSTREAM_SHA}"
fi
git checkout "${CANDIDATE}"
git rev-parse HEAD | grep -q "^${UPSTREAM_SHA}" || { echo "ERROR: candidate HEAD != ${UPSTREAM_SHA}"; # STOP
}
```

The shared branch `add_mongodb_tls-2` is NOT touched in this workflow.

### Step 8: Apply Fork Patch as Small Commits

Cherry-pick or re-apply, as logically small commits in repo style (`config:`, `chore:`, `docs:` prefixes), only the in-scope changes:

1. SNI patch: `collector/config.go` + `collector/config_test.go` (skip if `SNI_GAP=NO` per Step 5b)
2. `.opencode/skills/coroot-rebase-upstream/SKILL.md` + `.opencode/commands/coroot-rebase-upstream.md`

Rules:

- Do not carry `b77a889` (TLS UI/docs) or `bf0e5b4` (Dockerfile drift).
- If a cherry-pick conflicts in `front/` or `docs/`, take the upstream version (`git checkout --theirs -- <path>`) — never reapply fork UI/docs.
- If a cherry-pick conflicts in `collector/` or `model/`, STOP and show the conflict. Do not auto-resolve model/API drift — the SNI patch may need rework against new upstream APIs.
- Keep upstream UI (`front/`) and docs (`docs/`) byte-identical to `${UPSTREAM_SHA}`.

### Step 9: Validate BEFORE Any Push

```bash
go build ./...
go test ./...            # at minimum: go test ./collector/
git diff --check
docker build --progress=plain \
  --build-arg VERSION=v${VERSION}-mongo-tls-2 \
  -f dev.dockerfile \
  -t ghcr.io/tribunadigital/coroot:${VERSION}-mongo-tls-2 .
```

If tests, `git diff --check`, or the build fail, show the full error output and STOP. Nothing is pushed at this point.

### Step 10: Publication — Explicit Confirmation Required

Before any push, present exactly what will happen and wait for explicit user confirmation:

```text
Готово к публикации:
  - git push --force-with-lease=refs/heads/add_mongodb_tls-2:${EXPECTED_TIP} origin add_mongodb_tls-2-v${VERSION}-candidate:add_mongodb_tls-2
  - docker push ghcr.io/tribunadigital/coroot:${VERSION}-mongo-tls-2
Продолжить?
```

Notes:

- The shared branch is updated by pushing the candidate to it with an explicit lease pinning `EXPECTED_TIP` from Step 6 (fast-forward or safe overwrite). Use the lease form, never bare `--force`.
- If the user wants the candidate kept separate, push it under its own name instead — only on request.
- After confirmation, run the push commands. If git push fails, STOP. If docker push fails, STOP and report that the branch is already pushed.

Never publish without an explicit "yes" to the exact commands shown.

### Step 11: Success Report

```text
✅ Синхронизация завершена успешно!

Candidate: add_mongodb_tls-2-v${VERSION}-candidate (из ${TAG} = ${UPSTREAM_SHA})
Shared:    add_mongodb_tls-2 → ${VERSION} (если пуш подтверждён)
Backup:    add_mongodb_tls-2-pre-rebase-${TAG}
Image:     ghcr.io/tribunadigital/coroot:${VERSION}-mongo-tls-2
Tests:     go test ./... — PASS
Generic TLS upstream: ${GENERIC_TLS}; SNI gap closed by patch: YES

Для отката: git push --force-with-lease=refs/heads/add_mongodb_tls-2:$(git rev-parse add_mongodb_tls-2) origin add_mongodb_tls-2-pre-rebase-${TAG}:add_mongodb_tls-2
```

## Error Handling

| Ситуация | Действие |
|----------|----------|
| Dirty working directory | Сообщить о незакоммиченных изменениях, STOP |
| Upstream fetch failed | Проверить сеть/доступ к github.com, STOP |
| Тэг не существует | Показать доступные тэги, STOP |
| Backup ref существует и указывает на другой SHA | Показать оба SHA, STOP (не перезаписывать backup) |
| Candidate уже существует на другом SHA | `git branch -f` на точный SHA допустимо (candidate — throwaway) |
| Upstream уже имеет `sni` в collector/config.go | Показать реализацию, спросить пользователя, STOP |
| Конфликт в front/ или docs/ | Взять upstream-версию, продолжить |
| Конфликт в collector/ или model/ | Показать конфликт, STOP (НЕ авто-разрешать) |
| Тесты/`git diff --check`/build упали | Полный вывод ошибки, STOP (ничего не запушено) |
| Git push failed | Сообщить ошибку, STOP |
| Docker push failed | Сообщить ошибку, STOP (ветка уже pushed) |

Every error case must end with a terminal action: STOP.

## Quick Reference

```text
prereqs → remote → fetch → tag SHA → version → detect generic TLS (info) + SNI gap (decision) → backup ref → candidate branch → small commits → test + diff --check + build → explicit confirmation → push with lease → report
```

Operational reminders:

- Candidate-first: never rewrite the shared branch before validation and explicit confirmation.
- Always branch from the exact tag SHA, never from the tag name.
- Generic TLS detection and SNI gap detection are independent checks with independent decisions.
- Upstream UI and docs are always preserved; old fork TLS UI/docs and Dockerfile drift are never carried.
- Every publication requires an explicit user confirmation of the exact commands.
- Pushes use `--force-with-lease=<ref>:<expected-sha>`, never `--force`.

### Input checklist

- `TAG` provided by the user
- repository is clean
- upstream remote is reachable
- user confirms publication in Step 10

### Output checklist

- immutable backup ref at the pre-existing shared tip
- candidate branch at exact tag SHA with minimal patch commits
- `go test`, `git diff --check`, local docker build — all green before any push
- shared branch and image published only after explicit confirmation

### Minimal user prompt

```text
Enter upstream tag (for example v1.25.0), then follow the workflow exactly.
```
