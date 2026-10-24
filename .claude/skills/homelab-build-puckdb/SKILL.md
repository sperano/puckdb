---
name: homelab-build-puckdb
description: Build puckdb image using homelab builders, deploy to cluster
user-invocable: true
allowed-tools: Bash(docker-build-multiarch *), Bash(git *), Bash(argocd *), Bash(argocd-login), Edit, Read
---

# homelab-build-puckdb

Build multi-arch puckdb images from this checkout using homelab Docker builders, update Helm values, and deploy to cluster.

## Prerequisites

- Remote builders configured: `~/code/hollingsworth/scripts/setup-docker-builders.sh`
- Docker Hub login: `docker login`
- ArgoCD CLI installed

## Instructions

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
