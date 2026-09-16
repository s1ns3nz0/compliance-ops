# Kubernetes deployment scaffold

Base manifests for the single `compliance-ops` Go service (HTTP API + embedded
web UI, port 3000). This directory is intentionally not deployable until the
placeholders are replaced in an approved environment-specific overlay.

Render locally with `kubectl kustomize deploy/kubernetes/base`.

## What the base defines

| Resource | Notes |
| --- | --- |
| `ServiceAccount/compliance-ops` | IRSA role ARN placeholder (reserved; the service currently authenticates to object storage with static keys only). |
| `Deployment/compliance-ops` | 2 replicas, service-account token automount disabled, non-root, pod group `65532`, `readOnlyRootFilesystem`, all capabilities dropped, `RuntimeDefault` seccomp. The API token Secret is projected read-only with mode `0440`; `/tmp` is an `emptyDir` (512 MiB) because evidence uploads are spooled to a temp file before being streamed to object storage. Probes hit `GET /healthz`. |
| `Service/compliance-ops` | ClusterIP on port 80 → container port `http` (3000). |

## Required before any apply

1. **Image.** Pin `REPLACE_WITH_APPROVED_IMAGE_DIGEST` to an immutable digest of
   the image built from the repository `Dockerfile`.

2. **ConfigMap `compliance-ops-runtime`** (non-secret, referenced via `envFrom`):

   | Key | Required | Example |
   | --- | --- | --- |
   | `COMPLIANCE_OSCAL_SOURCE_URL` | yes | `https://.../NIST_SP-800-53_rev5_catalog.json,https://.../NIST_SP800-218_ver1_catalog.json` — one or more comma-separated HTTPS URLs, no credentials |
   | `COMPLIANCE_BLOB_ENDPOINT` | yes for evidence | `s3.ap-northeast-2.amazonaws.com` or an in-cluster MinIO `host:port` |
   | `COMPLIANCE_BLOB_BUCKET` | no (default `compliance-evidence`) | `compliance-evidence-prod` |
   | `COMPLIANCE_BLOB_REGION` | no (default `us-east-1`) | `ap-northeast-2` |
   | `COMPLIANCE_BLOB_USE_SSL` | no (default `false`) | `true` for any non-local endpoint |
   | `COMPLIANCE_MAX_UPLOAD_BYTES` | no (default 50 MiB) | `52428800` |
   | `COMPLIANCE_AUDIT_ACTOR` | no (default `operator`) | Trusted server-side identity recorded for new audit entries; trimmed length 1–200. `X-Actor` request headers are ignored. |

3. **Secret `compliance-ops-secrets`** (the token file is mounted and remaining
   credentials are referenced via `envFrom.secretRef`; create it out-of-band
   with your secrets manager / External Secrets — never commit it):

   | Key | Purpose |
   | --- | --- |
   | `compliance-api-tokens.json` | JSON object mapping token labels to unique token68-safe bearer-token strings. The enforced minimum is 12 characters; use randomly generated values of at least 43 characters for production. It is projected read-only at `/run/secrets/compliance-api-tokens.json` with mode `0440`; pod `fsGroup: 65532` and `fsGroupChangePolicy: OnRootMismatch` make it readable by the non-root app. Config accepts this mode while rejecting group write/execute and all other access. The legacy `COMPLIANCE_API_TOKEN` variable is unsupported. |
   | `DATABASE_URL` | PostgreSQL DSN, e.g. `postgres://user:pass@host:5432/compliance?sslmode=require`. Without it the service runs in OSCAL-read-only mode and tracking routes return 503. |
   | `COMPLIANCE_BLOB_ACCESS_KEY` | Object storage access key (static credentials are the only supported mode today). |
   | `COMPLIANCE_BLOB_SECRET_KEY` | Object storage secret key. |

4. **Ingress and network policy.** Configure the approved ingress for user
   access; do not expose the ClusterIP service directly without a reviewed
   NetworkPolicy. Add an environment-specific NetworkPolicy after the trusted
   ingress source labels/addresses are approved.

5. **Dependencies.** Provision PostgreSQL 16+ and an S3-compatible bucket in
   the environment overlay; the base does not deploy them.

No Secret value, token, key, or production endpoint belongs in these base
manifests.
