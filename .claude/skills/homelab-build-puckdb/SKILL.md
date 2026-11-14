---
name: homelab-build-puckdb
description: Build puckdb image using homelab builders, deploy to cluster
user-invocable: true
allowed-tools: Bash(docker-build-multiarch *), Bash(git *), Bash(argocd *), Bash(argocd-login), Edit, Read
---

# homelab-build-puckdb

**Legacy — do not run the build steps below by default.** This skill builds
`docker-build-multiarch` from whatever this checkout happens to have locally
checked out, not from merged `main`, bypassing the `onPush` image-build
pipeline in hollingsworth (wiki `hollingsworth/plans/kelos-image-builds.md`,
"Callers as built"). A stale-checkout build like this once shipped a
`maurice-web` image without a fix that had already merged.

Use the supported path instead:
- merging to `main` triggers the `onPush` build automatically;
- `just image-build puckdb` (in hollingsworth) builds on demand.

When this skill is invoked, first show the user the notice above (legacy,
why, and the supported alternatives) and stop there. Only continue to the
build steps below if the user, after seeing that notice, explicitly confirms
they want to run this legacy skill anyway.

## Prerequisites

- Remote builders configured: `~/code/hollingsworth/scripts/setup-docker-builders.sh`
- Docker Hub login: `docker login`
- ArgoCD CLI installed

## Instructions (legacy build — only after explicit confirmation above)

1. **Change to the root of this checkout** (the primary checkout or a worktree, whichever the skill was invoked from):
   ```bash
   cd "$(git rev-parse --show-toplevel)"
   ```

2. **Build and push multi-arch image**:
   ```bash
   docker-build-multiarch ericsperano/puckdb [tag]
   ```

   - If no tag argument provided, uses default: `dev-YYYYMMDD-HHMMSS`
   - Wait for the build to complete (typically 2-3 minutes)
   - Extract the tag from the output line: `Pushed: ericsperano/puckdb:TAG`

3. **Update Helm values with new tag**:
   - Read `~/code/hollingsworth/helm/puckdb/values-hollingsworth.yaml`
   - Find the line with `tag: &apiTag "..."` (the anchor the other puckdb images alias)
   - Update it to the new tag from step 2
   - Use the Edit tool to make this change

4. **Commit and push hollingsworth**:
   ```bash
   cd ~/code/hollingsworth
   git add helm/puckdb/values-hollingsworth.yaml
   git commit -m "chore: update puckdb image to TAG"
   git push
   ```

   Replace TAG with the actual tag used.

5. **Refresh ArgoCD**:
   ```bash
   argocd-login
   argocd app get --refresh puckdb --grpc-web
   ```

6. **Verify deployment**:
   ```bash
   argocd app get puckdb --grpc-web --refresh | head -20
   ```

   Report the sync status to the user.

## Usage Examples

```
/homelab-build-puckdb              # Build with auto-generated dev tag
/homelab-build-puckdb my-feature   # Build with tag "my-feature"
```
