# Changelog

## Unreleased

- Result types for recoverable errors: `!T` and `!`, built with `ok(v)`, `ok()` and `err(message)`, and inspected with `is_ok`, `is_err`, `error`, `unwrap` and `unwrap_or`.
- `try f()` returns an error from the current function, releasing everything it holds. `fn main() -> !` prints `kestrel: error: <message>` and exits with status 1.
- `try_read_file`, `try_write_file`, `try_parse_int` and `try_parse_float` return results instead of stopping the program.
- `tcp_poll(fds, timeout_ms)` serves many connections from one thread. `tcp_set_timeout`, `tcp_peer` and `tcp_listen_shared` (SO_REUSEPORT) for services.
- `trap_shutdown()` and `shutdown_requested()` for graceful shutdown on SIGTERM and SIGINT.
- `getenv`, `has_env`, `time_ms`, `clock_ms`, `sleep_ms`, `pid`, and the `eprint` statement. Standard error is line buffered.
- `kestrel build --release` (stack protector, fortify, PIE and full RELRO on Linux, stripped), `--static`, `--cflags` and `--ldflags`. Builds are reproducible.
- `examples/http/service.ks`, a multi-client HTTP service with graceful shutdown.
- A Dockerfile for a builder image, `examples/deploy/Dockerfile` for a scratch service image, `install.sh` with checksum verification, `SHA256SUMS` on releases, and `docs/deploy.md`.
- `try` and `eprint` are now keywords.

- The project is MIT licensed.
- GitHub releases build Linux and macOS binaries for amd64 and arm64 when a `v*` tag is pushed.
- `for i in start..end`, `for i in start..=end`, and `for x in array`. `break` and `continue` work inside them.
- `fmt("{} {}", a, b)` for int, float, bool, and string. `print` accepts every value.
- Methods: `fn (self: item) cost() -> int` borrows the value, and `fn (var self: item) add()` updates the caller's place.
- `delete(m, key)` removes a map entry and returns whether it was present. `for k in m` walks keys.
