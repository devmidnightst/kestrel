// Run with: kestrel test examples/testing
// Each fn test_name() runs in its own process. Use assert, or return an
// error from a test declared -> !.
import "std:http";

fn test_frame_waits_for_the_body() {
    assert(http.frame("POST / HTTP/1.1\r\nHost: x\r\nContent-Length: 4\r\n\r\nab", 100) == 0, "partial body");
    assert(http.frame("POST / HTTP/1.1\r\nHost: x\r\nContent-Length: 4\r\n\r\nabcd", 100) > 0, "full body");
}

fn test_frame_rejects_large_bodies() {
    assert(http.frame("POST / HTTP/1.1\r\nHost: x\r\nContent-Length: 101\r\n\r\n", 100) == -1, "over max_body");
}

fn test_parse() -> ! {
    let req = try http.parse("GET /search?q=kestrel+lang HTTP/1.1\r\nHost: example.com\r\n\r\n");
    assert(req.method == "GET", "method");
    assert(req.path == "/search", "path");
    assert(http.query_param(req.query, "q") == "kestrel lang", "query");
    assert(http.header(req, "HOST") == "example.com", "header lookup ignores case");
    return ok();
}

fn test_json_round_trip() -> ! {
    let doc = "{\"name\": " + json_quote("a \"quoted\" name") + ", \"tags\": [1, 2, 3]}";
    let name = try json_string(doc, "name");
    let count = try json_len(doc, "tags");
    assert(name == "a \"quoted\" name", "name");
    assert(count == 3, "tags");
    return ok();
}

fn test_password_hash() {
    let salt = random_bytes(16);
    let stored = pbkdf2_sha256("hunter2", salt, 10000, 32);
    assert(constant_time_eq(stored, pbkdf2_sha256("hunter2", salt, 10000, 32)), "same password");
    assert(!constant_time_eq(stored, pbkdf2_sha256("hunter3", salt, 10000, 32)), "different password");
}
