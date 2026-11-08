// A small HTTP service that serves many clients at once from one thread.
// tcp_poll waits until any socket has data, so a slow client never blocks
// the others. SIGTERM or Ctrl-C stops it cleanly after the current loop.
//
//   HOST=0.0.0.0 PORT=8080 kestrel run examples/http/service.ks
//
// It prints the bound port on standard output (useful with PORT=0) and logs
// to standard error. Run several copies on one port with tcp_listen_shared
// to use more than one core.

fn env_or(name: string, fallback: string) -> string {
    if has_env(name) {
        return getenv(name);
    }
    return fallback;
}

fn config_port() -> !int {
    let raw = env_or("PORT", "8080");
    let port = try try_parse_int(raw);
    if port < 0 || port > 65535 {
        return err(fmt("PORT must be 0 to 65535, got {}", raw));
    }
    return ok(port);
}

fn respond(status: string, body: string) -> string {
    return fmt("HTTP/1.1 {}\r\nContent-Type: text/plain\r\nContent-Length: {}\r\nConnection: close\r\n\r\n{}", status, len(body), body);
}

fn route(request: string) -> string {
    let parts = split(split(request, "\r\n")[0], " ");
    if len(parts) < 3 {
        return respond("400 Bad Request", "bad request\n");
    }
    if parts[0] != "GET" {
        return respond("405 Method Not Allowed", "method not allowed\n");
    }
    if parts[1] == "/" {
        return respond("200 OK", "hello from kestrel\n");
    }
    if parts[1] == "/health" {
        return respond("200 OK", "ok\n");
    }
    return respond("404 Not Found", "not found\n");
}

fn log(msg: string) {
    eprint(fmt("ts={} pid={} {}", time_ms(), pid(), msg));
}

fn main() -> ! {
    let host = env_or("HOST", "127.0.0.1");
    let port = try config_port();
    let server = tcp_listen(host, port);
    if server < 0 {
        return err(fmt("cannot listen on {}:{}", host, port));
    }
    trap_shutdown();
    print(tcp_port(server));
    log(fmt("listening on {}:{}", host, tcp_port(server)));

    // Bytes received so far, and when each connection was accepted.
    var pending: {int: string} = {};
    var since: {int: int} = {};
    while !shutdown_requested() {
        var watch = [server];
        for fd in pending {
            push(watch, fd);
        }
        for fd in tcp_poll(watch, 1000) {
            if fd == server {
                let client = tcp_accept(server);
                if client >= 0 {
                    tcp_set_timeout(client, 5000);
                    pending[client] = "";
                    since[client] = clock_ms();
                }
                continue;
            }
            let chunk = tcp_read(fd, 4096);
            if chunk == "" {
                tcp_close(fd);
                delete(pending, fd);
                delete(since, fd);
                continue;
            }
            pending[fd] = pending[fd] + chunk;
            var reply = "";
            if contains(pending[fd], "\r\n\r\n") {
                reply = route(pending[fd]);
            } else if len(pending[fd]) > 16384 {
                reply = respond("431 Request Header Fields Too Large", "too large\n");
            }
            if reply != "" {
                tcp_write(fd, reply);
                log(fmt("served {} {}", tcp_peer(fd), split(reply, "\r\n")[0]));
                tcp_close(fd);
                delete(pending, fd);
                delete(since, fd);
            }
        }
        // Drop connections that never finished their request.
        let now = clock_ms();
        for fd in since {
            if now - since[fd] > 10000 {
                tcp_close(fd);
                delete(pending, fd);
                delete(since, fd);
            }
        }
    }
    for fd in pending {
        tcp_close(fd);
    }
    tcp_close(server);
    log("shut down");
    return ok();
}
