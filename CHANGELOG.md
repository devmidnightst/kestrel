# Changelog

## Unreleased

- Runtime errors print where they happened: `    at app.ks:12 in main` (methods as `Type.name`, paths relative to the project root).
- `tcp_wait` waits for readable and writable sockets together; `tcp_send` writes without blocking; `tcp_try_accept` never hangs; `tcp_connect_timeout`. Listening sockets are non-blocking and `tcp_accept` waits with poll.
- `fork_workers(n)` runs worker processes under a supervisor that restarts crashed workers and forwards shutdown signals. `cpu_count()`.
- Maps keep insertion order, and their hashes are keyed per process (SipHash-1-3 for strings), so clients cannot force collisions. `keys`, `values`, `for` and `print` follow insertion order.
- `random_bytes`, `random_int`, `sha256`, `hmac_sha256`, `pbkdf2_sha256`, `constant_time_eq`, `hex`/`unhex`, `base64`/`base64url`/`unbase64`, `url_encode`/`url_decode`, `format_time`.
- JSON by path: `json_valid`, `json_type`, `json_string`, `json_int`, `json_float`, `json_bool`, `json_len`, `json_keys`, `json_raw`, `json_member`, `json_quote`.
- A standard library embedded in the compiler: `import "std:http";` frames, parses and builds HTTP/1.1 messages.
- `kestrel test` runs `fn test_...()` functions from `*_test.ks` files, each in its own process.
- `examples/http/service.ks` uses std:http, keep-alive, non-blocking writes and `WORKERS`. A load test, a worker crash test and a fuzzer for the runtime's parsers run in CI.

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
