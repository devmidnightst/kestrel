// An HTTP service built for production use with std:http.
//
//   HOST=0.0.0.0 PORT=8080 WORKERS=4 kestrel run --release examples/http/service.ks
//
// - Many connections per process: tcp_wait reports which sockets can be
//   read or written, so a slow client never blocks the others. Responses
//   are buffered and written with tcp_send, which never blocks.
// - Keep-alive and pipelined requests on one connection.
// - WORKERS processes share the listening socket under fork_workers, which
//   restarts any worker that crashes.
// - SIGTERM or Ctrl-C finishes the current loop, closes every socket and
//   exits 0. Idle connections are dropped after IDLE_MS.
//
// Routes: GET / and GET /health return text; POST /echo returns a JSON
// summary of the request body.
import "std:http";

struct conn {
    inbuf: string,
    outbuf: string,
    last: int,
    closing: bool,
}

struct server {
    listener: int,
    worker: int,
    max_body: int,
    conns: {int: conn},
}

fn env_or(name: string, fallback: string) -> string {
    if has_env(name) {
        return getenv(name);
    }
    return fallback;
}

fn env_int(name: string, fallback: int, low: int, high: int) -> !int {
    let raw = env_or(name, str(fallback));
    let n = try try_parse_int(raw);
    if n < low || n > high {
        return err(fmt("{} must be {} to {}, got {}", name, low, high, raw));
    }
    return ok(n);
}

fn log(worker: int, msg: string) {
    eprint(fmt("ts={} worker={} {}", time_ms(), worker, msg));
}

fn route(req: http.request) -> string {
    let keep = req.keep_alive;
    if req.path == "/" || req.path == "/health" {
        if req.method != "GET" && req.method != "HEAD" {
            return http.response(405, {"Allow": "GET, HEAD", "Content-Type": "text/plain"}, "method not allowed\n", keep);
        }
        var body = "hello from kestrel\n";
        if req.path == "/health" {
            body = "ok\n";
        }
        return http.text(200, body, keep);
    }
    if req.path == "/echo" {
        if req.method != "POST" {
            return http.response(405, {"Allow": "POST", "Content-Type": "text/plain"}, "method not allowed\n", keep);
        }
        let body = fmt("{{\"bytes\": {}, \"sha256\": {}, \"content_type\": {}}}\n",
            len(req.body), json_quote(hex(sha256(req.body))), json_quote(http.header(req, "content-type")));
        return http.json(200, body, keep);
    }
    return http.text(404, "not found\n", keep);
}

fn (var self: server) close(fd: int) {
    tcp_close(fd);
    delete(self.conns, fd);
}

// process answers every complete request buffered on fd, in order.
fn (var self: server) process(fd: int) {
    while !self.conns[fd].closing {
        let n = http.frame(self.conns[fd].inbuf, self.max_body);
        if n == 0 {
            return;
        }
        if n < 0 {
            self.conns[fd].outbuf = self.conns[fd].outbuf + http.text(400, "bad request\n", false);
            self.conns[fd].closing = true;
            return;
        }
        let raw = substr(self.conns[fd].inbuf, 0, n);
        self.conns[fd].inbuf = substr(self.conns[fd].inbuf, n, len(self.conns[fd].inbuf) - n);
        let parsed = http.parse(raw);
        var reply = "";
        if is_err(parsed) {
            reply = http.text(400, "bad request: " + error(parsed) + "\n", false);
            self.conns[fd].closing = true;
        } else {
            let req = unwrap(parsed);
            reply = route(req);
            if !req.keep_alive {
                self.conns[fd].closing = true;
            }
        }
        self.conns[fd].outbuf = self.conns[fd].outbuf + reply;
    }
}

fn (var self: server) run(idle_ms: int) {
    while !shutdown_requested() {
        var reads = [self.listener];
        var writes: [int] = [];
        for fd in self.conns {
            if self.conns[fd].outbuf != "" {
                push(writes, fd);
            } else if !self.conns[fd].closing {
                push(reads, fd);
            }
        }
        let ready = tcp_wait(reads, writes, 1000);
        let now = clock_ms();
        for fd in ready[0] {
            if fd == self.listener {
                var c = tcp_try_accept(self.listener);
                while c >= 0 {
                    tcp_set_timeout(c, 5000);
                    self.conns[c] = conn { inbuf: "", outbuf: "", last: now, closing: false };
                    c = tcp_try_accept(self.listener);
                }
                continue;
            }
            if !has(self.conns, fd) {
                continue;
            }
            let chunk = tcp_read(fd, 65536);
            if chunk == "" {
                self.close(fd);
                continue;
            }
            self.conns[fd].inbuf = self.conns[fd].inbuf + chunk;
            self.conns[fd].last = now;
            self.process(fd);
        }
        for fd in ready[1] {
            if !has(self.conns, fd) {
                continue;
            }
            let out = self.conns[fd].outbuf;
            let sent = tcp_send(fd, out);
            if sent < 0 {
                self.close(fd);
                continue;
            }
            self.conns[fd].outbuf = substr(out, sent, len(out) - sent);
            self.conns[fd].last = now;
            if self.conns[fd].outbuf == "" {
                if self.conns[fd].closing {
                    self.close(fd);
                } else {
                    self.process(fd);
                }
            }
        }
        for fd in self.conns {
            if now - self.conns[fd].last > idle_ms {
                self.close(fd);
            }
        }
    }
    for fd in self.conns {
        tcp_close(fd);
    }
}

fn main() -> ! {
    let host = env_or("HOST", "127.0.0.1");
    let port = try env_int("PORT", 8080, 0, 65535);
    let workers = try env_int("WORKERS", 1, 1, 256);
    let idle = try env_int("IDLE_MS", 15000, 100, 3600000);
    let max_body = try env_int("MAX_BODY", 1048576, 0, 1073741824);
    let listener = tcp_listen(host, port);
    if listener < 0 {
        return err(fmt("cannot listen on {}:{}", host, port));
    }
    trap_shutdown();
    print(tcp_port(listener));
    log(-1, fmt("listening on {}:{} with {} worker(s)", host, tcp_port(listener), workers));
    var worker = 0;
    if workers > 1 {
        worker = fork_workers(workers);
    }
    var s = server { listener: listener, worker: worker, max_body: max_body, conns: {} };
    s.run(idle);
    tcp_close(listener);
    log(worker, "shut down");
    return ok();
}
