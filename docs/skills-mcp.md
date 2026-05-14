# Skills MCP client flow

<!-- AI Training: Project 21 M4 skills MCP client docs for lesser-body -->

Status: Project 21 M4.4 (`lesser-body#136`). This document explains how MCP clients consume Lesser-published skills through
`lesser-body` without turning Body into a local skill catalog or an installer.

## Summary

`lesser-body` exposes two read-only MCP tools for skills:

| Tool | Scope | Profiles | Purpose |
|---|---|---|---|
| `skills_catalog` | `read` | `drone`, `souled` | List approved skill bundles from Lesser's canonical skill catalog. |
| `skill_bundle_get` | `read` | `drone`, `souled` | Fetch one approved Lesser skill bundle and optionally report read-only local install-state verification from caller-supplied file bytes. |

Both tools require an OAuth bearer that Body can forward to Lesser. They do not mutate the MCP client's filesystem,
workspace, Codex skills directory, runtime plugin directory, or any other local state.

## Authority and trust model

Lesser is the canonical skill/catalog/bundle authority.

- Lesser publishes approved skill catalog entries at `GET /api/v1/skills/catalog`.
- Lesser publishes an approved revision bundle at
  `GET /api/v1/skills/{skillId}/revisions/{revisionNumber}/bundle`.
- Body consumes those Lesser endpoints through the existing Lesser API client and forwards the MCP caller's OAuth bearer.
- Body preserves Lesser's catalog and bundle metadata, then adds MCP-facing `authority`, `content`, and `verification`
  summaries.
- Body does not maintain a local skill catalog, rank skills, approve skills, rewrite bundles, cache bundle authority, or
  install files.

The MCP response includes enough trust metadata for clients to display provenance and make explicit install decisions:

| Field | Role |
|---|---|
| `bundle.schema_version` | Lesser publication schema, currently `lesser.skill.bundle.v1`. |
| `bundle.bundle_id` | Stable selector, for example `skill:<skillId>:revision:00000001`. |
| `bundle.digests.bundle_digest` | Approved bundle content/metadata identity. |
| `bundle.digests.publication_digest` | Digest for the exact published contract shape, separate from file-content drift. |
| `bundle.digests.manifest_digest` | Approved manifest identity when Lesser has one. |
| `bundle.digests.content_digest` | Aggregate content identity when Lesser has content material. |
| `bundle.digests.approval_digest` | Approval material identity when available. |
| `bundle.files[].digest` | Per-file digest used for local file drift verification. |
| `bundle.files[].install_path` | Advisory placement path derived by Lesser; clients must still make an explicit write decision. |
| `bundle.install_hints` | Advisory layout, runtime targets, directory name, entrypoint, and required files. |
| `bundle.provenance[]` | Source/proposal references copied from the approved revision. |
| `bundle.approval_*`, `approved_by`, `principal_*` | Approval authority and principal metadata for trust display and audit. |

Clients can verify only the bytes and metadata they actually have. A digest in the response is not itself proof that a
local file matches; the client must hash local bytes and compare them to Lesser's `bundle.files[].digest`, either locally
or by supplying those bytes to `skill_bundle_get.local_files` for Body's read-only comparison report.

## Tool arguments and response additions

### `skills_catalog`

Arguments:

```json
{
  "exposure": "public",
  "limit": 20,
  "cursor": "optional-lesser-cursor"
}
```

- `exposure` is an optional Lesser exposure filter.
- `limit` asks Lesser for a bounded page. Body enforces the maximum server-side and caps oversized values to `100`
  before calling Lesser.
- `cursor` is the opaque `next_cursor` from a previous Lesser catalog response.

Body preserves Lesser's catalog response shape and adds:

```json
{
  "authority": {
    "source": "lesser",
    "endpoint": "/api/v1/skills/catalog",
    "catalog_authoritative": true,
    "body_cache_authority": false
  }
}
```

### `skill_bundle_get`

Arguments by explicit skill/revision:

```json
{
  "skill_id": "skill-a",
  "revision_number": 1,
  "include_content": true
}
```

Arguments by catalog bundle id:

```json
{
  "bundle_id": "skill:skill-a:revision:00000001"
}
```

Optional local verification input:

```json
{
  "skill_id": "skill-a",
  "revision_number": 1,
  "local_files": [
    {
      "path": "SKILL.md",
      "content": "# Skill A\n",
      "encoding": "utf-8"
    }
  ]
}
```

`local_files[]` entries must include `path` or `install_path`. `content` may be UTF-8/text or base64 encoded. Body uses
that content only to compute a digest comparison report; it does not echo local bytes back. Omit `local_files` when the
client cannot inspect local files. Send an explicit empty array (`"local_files": []`) only when the client inspected the
expected location and found no installed files.

Body preserves Lesser's bundle response and adds:

```json
{
  "content": {
    "mode": "inline",
    "files_total": 1,
    "inline_files": 1,
    "metadata_only_files": 0
  },
  "verification": {
    "state": "unknown_local_state",
    "reason": "local_files_not_provided",
    "source": "caller_supplied_local_file_bytes",
    "bundle_digest": "sha256:...",
    "publication_digest": "sha256:...",
    "checked_files": 0
  },
  "authority": {
    "source": "lesser",
    "endpoint": "/api/v1/skills/skill-a/revisions/1/bundle",
    "bundle_authoritative": true,
    "body_cache_authority": false,
    "workspace_mutated": false
  }
}
```

## Content modes

`skill_bundle_get.content.mode` describes what Lesser returned for the bundle files in this response:

| Mode | Meaning | Client handling |
|---|---|---|
| `inline` | Every bundle file has `content_included: true` and inline `content`. | The client has enough bytes to propose an install/write action, subject to user/runtime approval. |
| `metadata_only` | Files are listed with paths/digests/install hints, but no file content is inline. | Display metadata and trust data; do not claim installability from Body alone unless the client already has a separate content source. |
| `mixed` | Some files include inline content and some are metadata-only. | Install/write only the files whose bytes are available, or defer until all required files are available. |
| `no_files` | Lesser returned a bundle with no files. | Treat as a metadata-only capability bundle; no file install should be inferred. |

`include_content=true` asks Lesser to include inline content when the approved bundle has content available. It is not a
guarantee that every file will include bytes.

## Local install-state vocabulary

`skill_bundle_get.verification.state` is a read-only report. It does not install, repair, delete, or rewrite files.

| State | When Body reports it |
|---|---|
| `unknown_local_state` | `local_files` is omitted; the bundle has no files; local file content is missing/uninspectable; a bundle file lacks a digest; or metadata-only/unavailable bytes prevent a full comparison. |
| `not_installed` | The caller explicitly sends `local_files: []`, or every expected bundle file is unobserved after the client inspected local state. |
| `verified_match` | Every bundle file was actually byte-compared and every computed local digest matched Lesser's `bundle.files[].digest`. |
| `modified_local_copy` | At least one observed local file digest differs from Lesser's expected digest, or expected bundle files are missing while other local files were observed. |

Important verification rules:

- Omitted `local_files` means Body cannot inspect the client workspace and must report `unknown_local_state`.
- Metadata-only bundle data must not be described as verified unless local bytes are also supplied and compared.
- `verified_match` is valid only after local bytes are hashed and compared against Lesser file digests.
- `local_files: []` is an explicit observation: the client inspected and found no installed files, so Body reports
  `not_installed`.
- Body accepts at most 200 `local_files` entries, and each decoded local file content is limited to 1 MiB.

## Client install flow

A safe MCP client flow is:

1. Call `skills_catalog` to list Lesser-approved bundles.
2. Let the user or runtime choose a bundle by `bundle.bundle_id` or `skill_id` + `revision_number`.
3. Call `skill_bundle_get` with `include_content=true` if the client needs inline bytes.
4. Display trust metadata: provenance, approval/principal fields, bundle/publication/file digests, exposure, and install
   hints.
5. Decide outside the MCP tool whether to write files. This is a client/user action, not a Body side effect.
6. If files are written or already exist, hash local bytes locally or call `skill_bundle_get` again with `local_files` to
   get Body's verification report.

Clients must still enforce their own filesystem safety policy: normalize paths, confine writes to the intended skills or
plugin directory, reject traversal or absolute paths, and ask for user/runtime approval where required.

## Examples

The examples below show the `tools/call` payloads and abbreviated `result.structuredContent.data` fields. `content[0].text`
contains the same JSON payload serialized as text for text-only MCP clients.

### 1. Catalog selection

Request:

```json
{
  "jsonrpc": "2.0",
  "id": 10,
  "method": "tools/call",
  "params": {
    "name": "skills_catalog",
    "arguments": {
      "exposure": "public",
      "limit": 20
    }
  }
}
```

Abbreviated response:

```json
{
  "result": {
    "structuredContent": {
      "data": {
        "entries": [
          {
            "skill": {
              "id": "skill-a",
              "slug": "skill-a",
              "name": "Skill A",
              "default_exposure": "public",
              "status": "approved"
            },
            "revision": {
              "id": "skill-a-r1",
              "skill_id": "skill-a",
              "revision_number": 1,
              "status": "approved",
              "bundle_digest": "sha256:bundle",
              "approval_digest": "sha256:approval",
              "principal_id": "principal-1"
            },
            "bundle": {
              "schema_version": "lesser.skill.bundle.v1",
              "bundle_id": "skill:skill-a:revision:00000001",
              "digests": {
                "bundle_digest": "sha256:bundle",
                "publication_digest": "sha256:publication"
              },
              "files": [
                {
                  "path": "SKILL.md",
                  "digest": "sha256:file",
                  "install_path": "skill-a/SKILL.md",
                  "content_included": false
                }
              ],
              "install_hints": {
                "layout": "codex-skill",
                "directory_name": "skill-a",
                "entrypoint": "SKILL.md"
              },
              "provenance": [
                { "source_type": "proposal", "digest": "sha256:proposal" }
              ],
              "approval_id": "approval-1",
              "principal_id": "principal-1"
            }
          }
        ],
        "count": 1,
        "next_cursor": "next-catalog",
        "authority": {
          "source": "lesser",
          "endpoint": "/api/v1/skills/catalog",
          "catalog_authoritative": true,
          "body_cache_authority": false
        }
      }
    }
  }
}
```

### 2. Codex-targeted bundle consumption

A Codex-capable client can fetch inline content, inspect `install_hints.layout`, and then decide whether to write files to
its Codex skills directory. The MCP tool itself does not perform that write.

Request:

```json
{
  "jsonrpc": "2.0",
  "id": 11,
  "method": "tools/call",
  "params": {
    "name": "skill_bundle_get",
    "arguments": {
      "bundle_id": "skill:skill-a:revision:00000001",
      "include_content": true
    }
  }
}
```

Abbreviated response:

```json
{
  "result": {
    "structuredContent": {
      "data": {
        "bundle": {
          "bundle_id": "skill:skill-a:revision:00000001",
          "digests": {
            "bundle_digest": "sha256:bundle",
            "publication_digest": "sha256:publication",
            "manifest_digest": "sha256:manifest",
            "content_digest": "sha256:content",
            "approval_digest": "sha256:approval"
          },
          "files": [
            {
              "path": "SKILL.md",
              "digest": "sha256:3fc349d92cca36c2326da54076558a5d94b76d815cfe2bd1a0ce0ec284d53935",
              "install_path": "skill-a/SKILL.md",
              "content": "# Skill A\n",
              "encoding": "utf-8",
              "content_included": true
            }
          ],
          "install_hints": {
            "layout": "codex-skill",
            "runtime_targets": ["codex"],
            "directory_name": "skill-a",
            "entrypoint": "SKILL.md",
            "required_files": ["SKILL.md"]
          },
          "provenance": [
            { "source_type": "proposal", "digest": "sha256:proposal" }
          ],
          "approval_id": "approval-1",
          "approval_authority_type": "pilot",
          "approval_authority_id": "pilot@lessersoul.ai",
          "approved_by": "Pilot",
          "principal_id": "principal-1",
          "principal_approval_id": "pa-1"
        },
        "content": {
          "mode": "inline",
          "files_total": 1,
          "inline_files": 1,
          "metadata_only_files": 0
        },
        "verification": {
          "state": "unknown_local_state",
          "reason": "local_files_not_provided",
          "source": "caller_supplied_local_file_bytes",
          "checked_files": 0
        },
        "authority": {
          "source": "lesser",
          "endpoint": "/api/v1/skills/skill-a/revisions/1/bundle",
          "bundle_authoritative": true,
          "body_cache_authority": false,
          "workspace_mutated": false
        }
      }
    }
  }
}
```

After this response, a Codex client may present an explicit action such as: "Install `skill-a/SKILL.md` into the configured
Codex skills directory?" If the user/runtime approves, the client writes the file. That write is outside the MCP tool call.

### 3. Generic runtime bundle consumption

A non-Codex runtime follows the same trust model. It treats `install_hints` as advisory and maps the bundle into its own
runtime-specific package or plugin location only after explicit approval.

Request:

```json
{
  "jsonrpc": "2.0",
  "id": 12,
  "method": "tools/call",
  "params": {
    "name": "skill_bundle_get",
    "arguments": {
      "skill_id": "generic-skill",
      "revision_number": 2,
      "include_content": true
    }
  }
}
```

Generic client handling:

```text
1. Confirm bundle.install_hints.runtime_targets includes "generic" or is acceptable for this runtime.
2. Verify each inline file digest before staging bytes.
3. Ask the user/runtime policy whether to install.
4. Write only under the runtime's configured plugin directory.
5. Re-read local bytes and verify them against bundle.files[].digest.
```

The response still carries `authority.workspace_mutated: false`; any runtime install is a separate client action.

### 4. Metadata-only bundle handling

Request:

```json
{
  "jsonrpc": "2.0",
  "id": 13,
  "method": "tools/call",
  "params": {
    "name": "skill_bundle_get",
    "arguments": {
      "bundle_id": "skill:skill-a:revision:00000001"
    }
  }
}
```

Abbreviated response:

```json
{
  "result": {
    "structuredContent": {
      "data": {
        "bundle": {
          "files": [
            {
              "path": "SKILL.md",
              "digest": "sha256:3fc349d92cca36c2326da54076558a5d94b76d815cfe2bd1a0ce0ec284d53935",
              "install_path": "skill-a/SKILL.md",
              "content_included": false
            }
          ]
        },
        "content": {
          "mode": "metadata_only",
          "files_total": 1,
          "inline_files": 0,
          "metadata_only_files": 1
        },
        "verification": {
          "state": "unknown_local_state",
          "reason": "local_files_not_provided",
          "checked_files": 0
        },
        "authority": {
          "workspace_mutated": false
        }
      }
    }
  }
}
```

The client can display the bundle, provenance, digests, and install hints, but it should not claim that Body supplied a
complete installable file set. If the client already has local bytes, it can pass them as `local_files` for verification.

### 5. Local verification: matching installed files

Request:

```json
{
  "jsonrpc": "2.0",
  "id": 14,
  "method": "tools/call",
  "params": {
    "name": "skill_bundle_get",
    "arguments": {
      "skill_id": "skill-a",
      "revision_number": 1,
      "local_files": [
        {
          "install_path": "skill-a/SKILL.md",
          "content": "# Skill A\n",
          "encoding": "utf-8"
        }
      ]
    }
  }
}
```

Abbreviated verification response:

```json
{
  "verification": {
    "state": "verified_match",
    "reason": "verified_against_local_file_bytes",
    "source": "caller_supplied_local_file_bytes",
    "checked_files": 1,
    "files": [
      {
        "path": "SKILL.md",
        "install_path": "skill-a/SKILL.md",
        "expected_digest": "sha256:3fc349d92cca36c2326da54076558a5d94b76d815cfe2bd1a0ce0ec284d53935",
        "computed_digest": "sha256:3fc349d92cca36c2326da54076558a5d94b76d815cfe2bd1a0ce0ec284d53935",
        "state": "verified_match",
        "content_compared": true
      }
    ]
  }
}
```

### 6. Local verification: modified local copy

Request:

```json
{
  "jsonrpc": "2.0",
  "id": 15,
  "method": "tools/call",
  "params": {
    "name": "skill_bundle_get",
    "arguments": {
      "skill_id": "skill-a",
      "revision_number": 1,
      "local_files": [
        {
          "path": "SKILL.md",
          "content": "changed",
          "encoding": "utf-8"
        }
      ]
    }
  }
}
```

Abbreviated verification response:

```json
{
  "verification": {
    "state": "modified_local_copy",
    "reason": "local_copy_differs_from_bundle",
    "source": "caller_supplied_local_file_bytes",
    "checked_files": 1,
    "files": [
      {
        "path": "SKILL.md",
        "install_path": "skill-a/SKILL.md",
        "expected_digest": "sha256:3fc349d92cca36c2326da54076558a5d94b76d815cfe2bd1a0ce0ec284d53935",
        "computed_digest": "sha256:d67e2e944994496c8d8ec76eed0cf9f09679448d584b532bebf941852a37f5ed",
        "state": "modified_local_copy",
        "reason": "digest_mismatch",
        "content_compared": true
      }
    ]
  }
}
```

### 7. Local verification: explicit not installed

Request:

```json
{
  "jsonrpc": "2.0",
  "id": 16,
  "method": "tools/call",
  "params": {
    "name": "skill_bundle_get",
    "arguments": {
      "skill_id": "skill-a",
      "revision_number": 1,
      "local_files": []
    }
  }
}
```

Abbreviated verification response:

```json
{
  "verification": {
    "state": "not_installed",
    "reason": "no_local_files_reported",
    "source": "caller_supplied_local_file_bytes",
    "checked_files": 0,
    "files": [
      {
        "path": "SKILL.md",
        "install_path": "skill-a/SKILL.md",
        "expected_digest": "sha256:3fc349d92cca36c2326da54076558a5d94b76d815cfe2bd1a0ce0ec284d53935",
        "state": "not_installed",
        "reason": "local_file_not_observed"
      }
    ]
  }
}
```

## M4 closure readiness

After `lesser-body#136` lands, the Body-side M4 child work is complete from a repository/docs perspective:

- `lesser#703` / Lesser PR #954 provides the canonical catalog and bundle publication contract.
- `lesser-body#137` and `lesser-body#138` landed in Body PR #193.
- This document covers the client install flow, trust model, examples, content modes, and local verification vocabulary.

The parent tracker `lesser-body#129` can close after #136 lands if the project owner accepts docs completion as the final
M4 child. Stage deployment, probe evidence, and soak should remain part of the normal release / `deploy-body` workflow
rather than hidden inside this docs-only issue, unless the project owner explicitly makes deployment evidence a closure
gate for #129.
