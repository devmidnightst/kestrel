// std:http: HTTP/1.1 request framing and parsing, and response building.
//
//   import "std:http";
//
//   let n = http.frame(buffer, 1048576);   // bytes in the first request, 0 = need more, -1 = reject
//   let req = try http.parse(substr(buffer, 0, n));
//   tcp_send(fd, http.text(200, "hello\n", req.keep_alive));
//
// Limits: request heads are at most 16 KiB, bodies need Content-Length
// (chunked request bodies are rejected), and there is no TLS.

pub struct request {
    method: string,
    target: string,
    path: string,
    query: string,
    version: string,
    headers: {string: string},
    body: string,
    keep_alive: bool,
}

fn max_head() -> int {
    return 16384;
}

// frame returns the length in bytes of the first complete request at the
// start of buf, 0 if more bytes are needed, or -1 if the request must be
// rejected: a head over 16 KiB, a body over max_body, a bad or conflicting
// Content-Length, or a Transfer-Encoding (chunked bodies are unsupported).
pub fn frame(buf: string, max_body: int) -> int {
    let end = find(buf, "\r\n\r\n");
    if end < 0 {
        if len(buf) > max_head() {
            return -1;
        }
        return 0;
    }
    if end > max_head() {
        return -1;
    }
    var body = 0;
    var seen = false;
    let lines = split(substr(buf, 0, end), "\r\n");
    for i in 1..len(lines) {
        let colon = find(lines[i], ":");
        if colon <= 0 {
            return -1;
        }
        let name = lower(substr(lines[i], 0, colon));
        let value = trim(substr(lines[i], colon + 1, len(lines[i]) - colon - 1));
        if name == "transfer-encoding" {
            return -1;
        }
        if name == "content-length" {
            if !is_int(value) || starts_with(value, "-") || starts_with(value, "+") || len(value) > 15 {
                return -1;
            }
            let n = parse_int(value);
            if seen && n != body {
                return -1;
            }
            seen = true;
            body = n;
        }
    }
    if body > max_body {
        return -1;
    }
    let total = end + 4 + body;
    if len(buf) < total {
        return 0;
    }
    return total;
}

fn is_token_char(b: int) -> bool {
    if (b >= 48 && b <= 57) || (b >= 65 && b <= 90) || (b >= 97 && b <= 122) {
        return true;
    }
    return contains("!#$%&'*+-.^_`|~", chr(b));
}

fn is_token(s: string) -> bool {
    if s == "" {
        return false;
    }
    for i in 0..len(s) {
        if !is_token_char(byte_at(s, i)) {
            return false;
        }
    }
    return true;
}

fn has_ctl(s: string) -> bool {
    for i in 0..len(s) {
        let b = byte_at(s, i);
        if (b < 32 && b != 9) || b == 127 {
            return true;
        }
    }
    return false;
}

// parse reads one complete request, as framed by frame. Header names are
// lowercased; repeated headers are joined with ", ".
pub fn parse(raw: string) -> !request {
    let end = find(raw, "\r\n\r\n");
    if end < 0 {
        return err("incomplete request head");
    }
    let lines = split(substr(raw, 0, end), "\r\n");
    let parts = split(lines[0], " ");
    if len(parts) != 3 {
        return err("malformed request line");
    }
    let method = parts[0];
    let target = parts[1];
    let version = parts[2];
    if !is_token(method) || upper(method) != method {
        return err("invalid method");
    }
    if version != "HTTP/1.1" && version != "HTTP/1.0" {
        return err("unsupported HTTP version");
    }
    if target == "" || has_ctl(target) || (!starts_with(target, "/") && target != "*") {
        return err("invalid request target");
    }
    var headers: {string: string} = {};
    for i in 1..len(lines) {
        let line = lines[i];
        if starts_with(line, " ") || starts_with(line, "\t") {
            return err("folded headers are not allowed");
        }
        let colon = find(line, ":");
        if colon <= 0 {
            return err("malformed header line");
        }
        let name = lower(substr(line, 0, colon));
        if !is_token(name) {
            return err("invalid header name");
        }
        let value = trim(substr(line, colon + 1, len(line) - colon - 1));
        if has_ctl(value) {
            return err("invalid header value");
        }
        if has(headers, name) {
            headers[name] = headers[name] + ", " + value;
        } else {
            headers[name] = value;
        }
    }
    if version == "HTTP/1.1" && !has(headers, "host") {
        return err("missing Host header");
    }
    var path = target;
    var query = "";
    let q = find(target, "?");
    if q >= 0 {
        path = substr(target, 0, q);
        query = substr(target, q + 1, len(target) - q - 1);
    }
    var conn = "";
    if has(headers, "connection") {
        conn = lower(headers["connection"]);
    }
    var keep = version == "HTTP/1.1";
    if contains(conn, "close") {
        keep = false;
    } else if contains(conn, "keep-alive") {
        keep = true;
    }
    let body = substr(raw, end + 4, len(raw) - end - 4);
    return ok(request {
        method: method, target: target, path: path, query: query, version: version,
        headers: headers, body: body, keep_alive: keep,
    });
}

// header returns a request header by case-insensitive name, or "".
pub fn header(req: request, name: string) -> string {
    let key = lower(name);
    if has(req.headers, key) {
        return req.headers[key];
    }
    return "";
}

// decode_path percent-decodes a path. Unlike url_decode it keeps "+".
pub fn decode_path(path: string) -> !string {
    return url_decode(join(split(path, "+"), "%2B"));
}

// query_params decodes a query string or form body into a map. The first
// value of a repeated name wins; undecodable pairs are skipped.
pub fn query_params(query: string) -> {string: string} {
    var out: {string: string} = {};
    if query == "" {
        return out;
    }
    for pair in split(query, "&") {
        if pair == "" {
            continue;
        }
        let eq = find(pair, "=");
        var k = pair;
        var v = "";
        if eq >= 0 {
            k = substr(pair, 0, eq);
            v = substr(pair, eq + 1, len(pair) - eq - 1);
        }
        let dk = url_decode(k);
        let dv = url_decode(v);
        if is_ok(dk) && is_ok(dv) && !has(out, unwrap(dk)) {
            out[unwrap(dk)] = unwrap(dv);
        }
    }
    return out;
}

// query_param returns one decoded query parameter, or "".
pub fn query_param(query: string, name: string) -> string {
    let params = query_params(query);
    if has(params, name) {
        return params[name];
    }
    return "";
}

pub fn status_text(code: int) -> string {
    let texts = {
        100: "Continue", 101: "Switching Protocols",
        200: "OK", 201: "Created", 202: "Accepted", 204: "No Content",
        301: "Moved Permanently", 302: "Found", 303: "See Other", 304: "Not Modified",
        307: "Temporary Redirect", 308: "Permanent Redirect",
        400: "Bad Request", 401: "Unauthorized", 403: "Forbidden", 404: "Not Found",
        405: "Method Not Allowed", 408: "Request Timeout", 409: "Conflict", 410: "Gone",
        411: "Length Required", 413: "Content Too Large", 414: "URI Too Long",
        415: "Unsupported Media Type", 422: "Unprocessable Content", 429: "Too Many Requests",
        431: "Request Header Fields Too Large",
        500: "Internal Server Error", 501: "Not Implemented", 502: "Bad Gateway",
        503: "Service Unavailable", 504: "Gateway Timeout",
    };
    if has(texts, code) {
        return texts[code];
    }
    return "Unknown";
}

fn clean(s: string) -> string {
    return join(split(join(split(s, "\r"), " "), "\n"), " ");
}

// response builds a full response. CR and LF in header names and values
// are replaced with spaces, so request data echoed into a header cannot
// split the response. Content-Length and Connection are always set here.
pub fn response(code: int, headers: {string: string}, body: string, keep_alive: bool) -> string {
    var parts: [string] = [fmt("HTTP/1.1 {} {}\r\n", code, status_text(code))];
    for name in headers {
        let lname = lower(name);
        if lname != "content-length" && lname != "connection" {
            push(parts, clean(name) + ": " + clean(headers[name]) + "\r\n");
        }
    }
    push(parts, fmt("Content-Length: {}\r\n", len(body)));
    if keep_alive {
        push(parts, "Connection: keep-alive\r\n\r\n");
    } else {
        push(parts, "Connection: close\r\n\r\n");
    }
    push(parts, body);
    return join(parts, "");
}

pub fn text(code: int, body: string, keep_alive: bool) -> string {
    return response(code, {"Content-Type": "text/plain; charset=utf-8"}, body, keep_alive);
}

pub fn json(code: int, body: string, keep_alive: bool) -> string {
    return response(code, {"Content-Type": "application/json"}, body, keep_alive);
}
