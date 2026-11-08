// One HTTP request, then exit: listen, accept, read, write, close. This is
// the smallest possible server. service.ks next to it serves many clients at
// once with tcp_poll and shuts down cleanly on SIGTERM.
fn reply(body: string) -> string {
    return "HTTP/1.1 200 OK\r\nContent-Length: " + str(len(body)) + "\r\nConnection: close\r\n\r\n" + body;
}

fn main() {
    let server = tcp_listen("127.0.0.1", 0);
    if server < 0 {
        print("listen failed");
        exit(1);
    }
    print(tcp_port(server));
    let client = tcp_accept(server);
    if client < 0 {
        print("accept failed");
        exit(1);
    }
    let _req = tcp_read(client, 4096);
    tcp_write(client, reply("ok"));
    tcp_close(client);
    tcp_close(server);
}
