# Deploying Kestrel programs

A Kestrel program compiles to one native executable with no runtime to
install. This guide covers building it for production and running it under
Docker or systemd. `examples/http/service.ks` is the reference service used
below: an HTTP server that handles many clients from one thread, reads
`HOST` and `PORT`, logs to standard error, and shuts down cleanly on
`SIGTERM`.

Be honest with yourself about what Kestrel is before you rely on it: a young
language with heavy testing and no production track record. There is no TLS
and no HTTP library; terminate TLS in a reverse proxy.

## 1. Build a release binary

```sh
kestrel build --release app.ks -o app
```

`--release` turns on the stack protector, fortified libc calls, and on Linux
a position independent executable with full RELRO, and strips the binary.
The same source, compiler version and flags give the same bytes, so you can
rebuild and compare.

For a binary with no shared library dependencies, add `--static`. It works
best with musl, for example in an Alpine container (below). With glibc the
link succeeds but host name lookups still need glibc's shared libraries at
run time, so prefer numeric addresses like `0.0.0.0` or use musl.

Extra C flags go through `--cflags` and `--ldflags`, for example
`--cflags "-march=x86-64-v3"`.

## 2. Write the program like a service

The checklist the reference service follows:

- **Configuration from the environment.** `getenv` and `has_env`, validated
  with result types so a bad value stops the program with a clear message:

  ```
  fn config_port() -> !int {
      let port = try try_parse_int(getenv("PORT"));
      if port < 0 || port > 65535 { return err("PORT out of range"); }
      return ok(port);
  }
  fn main() -> ! {
      let port = try config_port();   // prints "kestrel: error: ..." and exits 1
      ...
  }
  ```

- **Recoverable errors are results.** Use `try_read_file`, `try_write_file`,
  `try_parse_int` and `try_parse_float` instead of the versions that stop the
  program, and propagate with `try`.
- **Logs on standard error.** `eprint(...)` is line buffered, so each line
  is written whole. systemd's journal and `docker logs` collect it.
- **Serve clients concurrently.** Use `tcp_poll` to wait on the listening
  socket and every open connection at once. Never call `tcp_read` on a
  socket `tcp_poll` did not report ready.
- **Bound everything.** `tcp_set_timeout` on each connection so a client
  that stops reading cannot block `tcp_write` forever; cap request sizes;
  close connections that never finish their request.
- **Graceful shutdown.** Call `trap_shutdown()` at start, loop on
  `!shutdown_requested()`, and give `tcp_poll` a timeout (the reference uses
  1000 ms). On `SIGTERM` the blocking call returns early, the loop ends, and
  the program closes its sockets and returns. This also matters in
  containers: a process running as PID 1 ignores `SIGTERM` unless it handles
  it, so without `trap_shutdown` Docker waits and then kills it.
- **More than one core.** Run several processes and have each call
  `tcp_listen_shared(host, port)`; the kernel spreads connections across
  them.

## 3. Docker

The repository's `Dockerfile` builds a builder image with the compiler, gcc
and musl:

```sh
docker build -t kestrel .
```

`examples/deploy/Dockerfile` is a two-stage image for a service: compile a
static release binary in the builder image, then copy only the binary into
an empty `scratch` image that runs as an unprivileged user:

```dockerfile
FROM kestrel AS build
COPY . /app
RUN kestrel build --release --static service.ks -o /app/server

FROM scratch
COPY --from=build /app/server /server
ENV HOST=0.0.0.0 PORT=8080
EXPOSE 8080
USER 65534:65534
ENTRYPOINT ["/server"]
```

```sh
docker build -t hello-service -f examples/deploy/Dockerfile examples/http
docker run --rm -p 8080:8080 hello-service
curl localhost:8080/health
```

The resulting image is a little over a megabyte. `docker stop` sends
`SIGTERM`, and the service exits with status 0 well inside the default
10 second grace period. `scratch` has no `/etc/hosts` or resolver
configuration, so listen on and connect to numeric addresses, or base the
final stage on `alpine` if the program resolves names.

## 4. systemd

```ini
# /etc/systemd/system/hello.service
[Unit]
Description=hello kestrel service
After=network-online.target
Wants=network-online.target

[Service]
ExecStart=/usr/local/bin/hello
Environment=HOST=127.0.0.1 PORT=8080
Restart=on-failure
RestartSec=2
KillSignal=SIGTERM
TimeoutStopSec=15

# Sandboxing: the service needs nothing but its port.
DynamicUser=yes
NoNewPrivileges=yes
ProtectSystem=strict
ProtectHome=yes
PrivateTmp=yes
PrivateDevices=yes
RestrictAddressFamilies=AF_UNIX AF_INET AF_INET6
LimitNOFILE=65536

[Install]
WantedBy=multi-user.target
```

```sh
sudo install -m 0755 hello /usr/local/bin/hello
sudo systemctl daemon-reload
sudo systemctl enable --now hello
journalctl -u hello -f
```

To use several cores, make it a template unit (`hello@.service`), have the
program listen with `tcp_listen_shared`, and start `hello@1`, `hello@2`, and
so on. Each process has its own memory, so there is nothing to share or lock.

pm2 also works (`pm2 start /usr/local/bin/hello --name hello`); pm2's
cluster mode is for Node.js only, so for several processes start several
named instances on the same port with `tcp_listen_shared`.

## 5. TLS and the edge

Kestrel has no TLS. Bind the service to `127.0.0.1` and put a reverse proxy
in front that terminates TLS, for example Caddy:

```
example.com {
    reverse_proxy 127.0.0.1:8080
}
```

The proxy also gives you HTTP/2, request size limits, and keep-alive to
clients while the Kestrel service handles one request per connection.

## 6. Check before you ship

```sh
kestrel check app.ks                 # type check
kestrel run --sanitize app.ks        # AddressSanitizer + UBSan, leak checks on Linux
kestrel build --release app.ks -o app
```

Run your load and shutdown tests against the sanitized build too: a clean
exit after `SIGTERM` lets the leak checker see everything the program
allocated.
