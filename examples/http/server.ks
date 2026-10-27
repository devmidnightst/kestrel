// One HTTP request, then exit. This is the shape a Kestrel server can have
// today: listen, accept, read, write, close. There is no second client until
// the language grows concurrency.
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
