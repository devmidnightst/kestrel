# Changelog

## Unreleased

- The project is MIT licensed.
- GitHub releases build Linux and macOS binaries for amd64 and arm64 when a `v*` tag is pushed.
- `for i in start..end`, `for i in start..=end`, and `for x in array`. `break` and `continue` work inside them.
- `fmt("{} {}", a, b)` for int, float, bool, and string. `print` accepts every value.
- Methods: `fn (self: item) cost() -> int` borrows the value, and `fn (var self: item) add()` updates the caller's place.
- `delete(m, key)` removes a map entry and returns whether it was present. `for k in m` walks keys.
