# S3F CLI

S3F CLI is an object-storage shell that feels close to Linux, without pretending to be a POSIX file system.

The product definition is intentionally narrow:

- It exposes Linux-shaped commands such as `cd`, `ls`, `cat`, `cp`, and `mv`.
- It keeps a virtual working directory on the client.
- It translates user intent into S3 APIs through a dedicated virtual file system layer.
- It does not promise inode semantics, atomic rename, in-place editing, or complete POSIX compatibility.

## Positioning

S3F CLI should be described as:

> A terminal for browsing and operating S3 objects with Linux-like ergonomics.

It should not be described as:

> Turning S3 into Linux file system storage.

That distinction drives the entire implementation:

- Directories are virtual and may be backed by marker objects.
- `cd` changes session state, not a server-side working directory.
- `mv` is non-atomic `copy + delete`.
- `vi` is download-edit-upload, not in-place mutation.

## Project Layout

- `cmd/s3f`: thin CLI entrypoint
- `docs/command-semantics.md`: command semantics and product boundary
- `internal/cli`: command contracts and shell-oriented command implementations
- `internal/model`: shared types, session model, and structured errors
- `internal/vfs`: virtual path resolution and VFS interfaces
- `internal/store`: object-store abstraction for AWS S3 and compatible endpoints
- `internal/transfer`: transfer policy and multipart strategy
- `internal/editor`: edit-session interfaces for phase 3

## Delivery Phases

### Phase 1: MVP

- `pwd`
- `cd`
- `ls`
- `ll`
- `cat`
- `cp`
- `mv`
- `mkdir -p`

### Phase 2

- `find`
- `rm`
- `head`
- `tail`
- shell glob support
- completion

### Phase 3

- `vi`
- local cache
- optimistic concurrency protection
- batch progress
- richer metadata display

## Current Status

This repository contains:

- the product semantics document
- a compilable Go architecture scaffold
- path resolution and session handling
- tests for normalization, session updates, and strict `cd` semantics

The S3 backend implementation is intentionally left behind interfaces so AWS S3 and compatible object stores can be added without leaking vendor-specific behavior into the command layer.

## Testing

Run the full test suite with:

```bash
go test ./...
```

The repository now includes:

- unit tests for path normalization, session updates, command behavior, and object-VFS semantics
- MinIO-backed integration tests under `./integration`

Integration tests automatically:

- start a temporary MinIO container with Docker
- create an isolated test bucket
- verify store CRUD, range reads, multipart upload/copy, marker directories, recursive copy, move, and `find`

If the Docker daemon is unavailable, the integration tests are skipped rather than failing the whole suite.

## CI and Releases

This repository includes GitHub Actions for both CI and tagged releases:

- CI runs on push and pull request
- release publishing runs when you push a tag like `v0.1.0`

The release workflow uses GoReleaser to:

- build `s3f` for Linux, macOS, and Windows
- package artifacts as `.tar.gz` on Unix-like platforms and `.zip` on Windows
- attach archives and a checksums file to the GitHub Release

Example release flow:

```bash
git tag v0.1.0
git push origin v0.1.0
```

After the tag is pushed, GitHub Actions will create the release and upload the generated packages automatically.
