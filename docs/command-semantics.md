# S3F CLI Command Semantics

## Semantic Boundary

S3F CLI provides an object-storage shell experience. It uses Linux-like commands, but it does not claim Linux file system semantics.

### Naturally fits S3

- `ls`
- `cat`
- `cp`
- `find`

### Simulated, not native

- `cd`
- `mkdir -p`
- `ll`
- `mv`

### Approximate only

- `vi`

## Core Model

### Virtual working directory

The client maintains session state:

- current endpoint
- current bucket
- current prefix
- normalized cwd string

`cd` mutates only this local session state.

### Path forms

The virtual file system layer accepts:

- `s3://bucket/path/to/object`
- `/bucket/path/to/object`
- relative paths such as `foo/bar`
- `.` and `..`

All remote paths normalize to:

- `bucket`
- `key`
- optional directory hint

### Directory model

Directories are virtual. They may exist because:

- there are objects under a prefix
- there is a marker object such as `foo/bar/`

S3F CLI should treat marker objects as first-class directory evidence so `mkdir -p`, empty directories, and strict `cd` all behave predictably.

## Command Contracts

Every command section below uses the same template:

- Linux similarity
- S3-specific difference
- Backend mapping
- failure semantics
- product warning

### `pwd`

Linux similarity:

- Prints the current working path.

S3-specific difference:

- The path is a virtual remote cwd such as `/bucket-a/foo/bar/`.

Backend mapping:

- No API call required.

Failure semantics:

- Only fails if session state is invalid.

Product warning:

- `pwd` reflects local shell state, not server-side state.

### `cd`

Linux similarity:

- Supports absolute paths, relative paths, `.` and `..`.

S3-specific difference:

- Changes the client's virtual cwd.

Backend mapping:

- Resolve path locally.
- Strict mode validates directory existence through `ListObjectsV2` and optionally `HeadObject` for marker objects.

Failure semantics:

- Reject paths that normalize outside the current bucket root.
- Reject non-existent prefixes in strict mode.
- Reject object keys when a directory is required.

Product warning:

- `cd` does not create directories.

### `ls`

Linux similarity:

- Lists the current directory or a requested target.

S3-specific difference:

- Uses S3 prefix listing and synthetic directories.

Backend mapping:

- `ListObjectsV2(prefix=<dir>, delimiter=/)`

Failure semantics:

- Missing path returns a path-not-found error.
- Empty directories return no rows, not an error.

Product warning:

- Results depend on marker objects and object prefixes, not true directory entries.

### `ll`

Linux similarity:

- Long-form directory listing.

S3-specific difference:

- Permissions, owner, and link count are synthetic display fields.

Backend mapping:

- `ListObjectsV2`
- `HeadObject` when additional metadata is needed for a single object

Failure semantics:

- Same as `ls`.

Product warning:

- Mode bits, `uid`, `gid`, inode count, and hard-link count are display shims only.

### `cat`

Linux similarity:

- Streams object content to stdout.

S3-specific difference:

- Reads object payloads through HTTP-backed object retrieval.

Backend mapping:

- `GetObject`
- `Range` reads for `head`, `tail`, or future pagination helpers

Failure semantics:

- Missing object returns object-not-found.
- Directories are rejected.

Product warning:

- Large objects should be streamed; the client should avoid full downloads unless needed.

### `cp`

Linux similarity:

- Supports object copy between sources and targets and recursive mode for directories.

S3-specific difference:

- The implementation chooses among upload, download, server-side copy, or multipart strategies.

Backend mapping:

- local -> S3: `PutObject` or multipart upload
- S3 -> local: `GetObject`
- S3 -> S3: `CopyObject` or multipart copy
- cross-endpoint S3 copies may fall back to download-upload

Failure semantics:

- Recursive copies may partially succeed.
- Metadata preservation may require explicit handling.

Product warning:

- Multipart thresholds and endpoint compatibility affect performance and behavior.

### `mv`

Linux similarity:

- Moves or renames files and directories.

S3-specific difference:

- There is no native rename. Every move is copy plus delete.

Backend mapping:

- single object: `CopyObject` -> verify -> `DeleteObject`
- prefix move: list all objects, then copy/delete each one

Failure semantics:

- Partial success must be surfaced as a first-class error.
- The system may leave both source and destination copies if deletion fails.

Product warning:

- `mv` is not atomic and must never be documented as safe like a POSIX rename.

### `mkdir -p`

Linux similarity:

- Creates nested directories and succeeds when they already exist.

S3-specific difference:

- Creates marker objects to make empty directories visible and strict `cd` possible.

Backend mapping:

- `PutObject(key="a/")`
- `PutObject(key="a/b/")`
- `PutObject(key="a/b/c/")`

Failure semantics:

- Existing marker objects or existing child content are treated as success.

Product warning:

- External tools may not agree on marker-object interpretation.

### `find`

Linux similarity:

- Traverses a directory tree and supports basic filters.

S3-specific difference:

- Traversal cost is driven by repeated object listing, not filesystem tree walking.

Backend mapping:

- recursive `ListObjectsV2`
- client-side filtering for unsupported predicates

Failure semantics:

- Large scans may exceed warning thresholds or take a long time.

Product warning:

- Phase 2 should support only `--max-depth`, `--name`, and `--type f|d`.
- Full GNU `find` parity is intentionally out of scope.

### `vi`

Linux similarity:

- Opens a target in an editor and writes changes back on save.

S3-specific difference:

- The edit flow is download-edit-upload against a temporary file.

Backend mapping:

- `GetObject` to temp file
- launch editor
- compare changes
- `PutObject` on save
- validate `ETag` or `VersionId` before upload

Failure semantics:

- Remote changes after open should block save unless the user forces overwrite.

Product warning:

- `vi` is an approximation of file editing, not true in-place mutation.

## Error Model

The command layer should emit structured error categories:

- path not found
- object not found
- empty directory
- non-atomic move / partial success
- remote changed
- invalid path
- unsupported operation

The human-readable message should always explain the S3-specific reason behind the failure.

## Performance Guardrails

- `find` should default to the current prefix, never the whole endpoint.
- Recursive operations should show progress and warnings once implemented.
- Large transfers should switch to multipart strategies automatically.
- `cat` and future paging helpers should support range reads.

## Compatibility Rules

- The command layer must depend on the `ObjectStore` abstraction, not the AWS SDK directly.
- The abstraction should support AWS S3 and compatible object stores.
- Versioning-aware features should degrade gracefully to ETag checks when version IDs are unavailable.
