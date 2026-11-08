package compiler

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

func TestEprintAndProcessBuiltins(t *testing.T) {
	requireCC(t)
	t.Setenv("KESTREL_TEST_VAR", "hello")
	src := `fn main() {
    print("out", 1);
    eprint("err", [1, 2], ok(3));
    print(getenv("KESTREL_TEST_VAR"), has_env("KESTREL_TEST_VAR"), has_env("KESTREL_SURELY_UNSET_X"), getenv("KESTREL_SURELY_UNSET_X") == "", getenv("") == "");
    let a = clock_ms();
    sleep_ms(30);
    sleep_ms(0);
    sleep_ms(-5);
    print(clock_ms() - a >= 25, time_ms() > 1700000000000, pid() > 0, shutdown_requested());
}`
	for _, sanitize := range []bool{false, true} {
		if sanitize {
			requireSanitizers(t)
		}
		r := runWith(t, src, "", sanitize)
		if r.code != 0 || r.stdout != "out 1\nhello true false true true\ntrue true true false\n" || r.stderr != "err [1, 2] ok(3)\n" {
			t.Errorf("sanitize %v: exit %d\nstdout %q\nstderr %q", sanitize, r.code, r.stdout, r.stderr)
		}
	}
}

func TestTCPPollTimeoutsAndPeers(t *testing.T) {
	requireCC(t)
	src := `fn main() {
    let a = tcp_listen_shared("127.0.0.1", 0);
    let p = tcp_port(a);
    let b = tcp_listen_shared("127.0.0.1", p);
    let c = tcp_listen("127.0.0.1", p);
    print(a >= 0, b >= 0, c);
    let t0 = clock_ms();
    print(len(tcp_poll([a], 50)), clock_ms() - t0 >= 40, len(tcp_poll([], 1)), len(tcp_poll([-1, 99999999999], 1)));
    let cl = tcp_connect("127.0.0.1", p);
    var ready = tcp_poll([a, b], 2000);
    print(len(ready));
    let sv = tcp_accept(ready[0]);
    print(starts_with(tcp_peer(sv), "127.0.0.1:"), tcp_peer(-1) == "", tcp_set_timeout(sv, 100), tcp_set_timeout(-1, 5));
    let t1 = clock_ms();
    print(tcp_read(sv, 10) == "", clock_ms() - t1 >= 80);
    tcp_write(cl, "ping");
    print(tcp_poll([sv], 2000) == [sv]);
    print(tcp_read(sv, 10));
    tcp_close(cl);
    print(tcp_poll([sv], 2000) == [sv], tcp_read(sv, 10) == "");
    tcp_close(sv);
    tcp_close(a);
    tcp_close(b);
}`
	// tcp_poll returns an array, which cannot be compared with ==; check the
	// type error, then run a version that compares lengths.
	expectError(t, src, "operator == is not defined for [int]")
	src = strings.ReplaceAll(src, "tcp_poll([sv], 2000) == [sv]", "len(tcp_poll([sv], 2000)) == 1")
	for _, sanitize := range []bool{false, true} {
		if sanitize {
			requireSanitizers(t)
		}
		r := runWith(t, src, "", sanitize)
		want := "true true -1\n0 true 0 0\n1\ntrue true true false\ntrue true\ntrue\nping\ntrue true\n"
		if r.code != 0 || r.stdout != want || r.stderr != "" {
			t.Errorf("sanitize %v: exit %d\nstdout %q\nwant   %q\nstderr %q", sanitize, r.code, r.stdout, want, r.stderr)
		}
	}
}

func httpGet(t *testing.T, port, path string) string {
	t.Helper()
	conn, err := net.DialTimeout("tcp", "127.0.0.1:"+port, 5*time.Second)
	if err != nil {
		t.Error(err)
		return ""
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := io.WriteString(conn, "GET "+path+" HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n"); err != nil {
		t.Error(err)
		return ""
	}
	body, err := io.ReadAll(conn)
	if err != nil {
		t.Error(err)
	}
	return string(body)
}

// startService builds examples/http/service.ks with opts, starts it with
// env, and returns the port, a stop function that sends SIGTERM and waits,
// and the stderr buffer.
func startService(t *testing.T, opts BuildOptions, env ...string) (string, func() error, *lockedWriter) {
	t.Helper()
	exe := filepath.Join(t.TempDir(), "service")
	opts.Output = exe
	if err := Build(filepath.Join("..", "..", "examples", "http", "service.ks"), opts); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(exe)
	cmd.Env = append(os.Environ(), append(append(sanitizerEnv(), "PORT=0", "HOST=127.0.0.1"), env...)...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	stderr := &lockedWriter{w: &bytes.Buffer{}}
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	t.Cleanup(func() {
		if cmd.ProcessState == nil {
			_ = cmd.Process.Kill()
		}
	})
	if f, ok := stdout.(*os.File); ok {
		_ = f.SetDeadline(time.Now().Add(10 * time.Second))
	}
	line, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil {
		t.Fatalf("port: %v stderr %s", err, stderr.String())
	}
	stop := func() error {
		if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
			return err
		}
		select {
		case err := <-done:
			return err
		case <-time.After(15 * time.Second):
			return fmt.Errorf("service did not stop after SIGTERM")
		}
	}
	return strings.TrimSpace(line), stop, stderr
}

type lockedWriter struct {
	mu sync.Mutex
	w  *bytes.Buffer
}

func (l *lockedWriter) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.String()
}

func (l *lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}

// TestServiceExample holds one connection open with half a request, serves
// other clients concurrently, checks keep-alive, pipelining, the JSON echo
// endpoint and request rejection, then stops the service with SIGTERM.
func TestServiceExample(t *testing.T) {
	requireCC(t)
	if runtime.GOOS == "windows" {
		t.Skip("uses SIGTERM")
	}
	for _, mode := range []string{"release", "sanitize", "workers"} {
		t.Run(mode, func(t *testing.T) {
			opts := BuildOptions{Release: mode != "sanitize", Sanitize: mode == "sanitize"}
			if opts.Sanitize {
				requireSanitizers(t)
			}
			var env []string
			if mode == "workers" {
				env = append(env, "WORKERS=3")
			}
			port, stop, stderr := startService(t, opts, env...)

			slow, err := net.DialTimeout("tcp", "127.0.0.1:"+port, 5*time.Second)
			if err != nil {
				t.Fatal(err)
			}
			defer slow.Close()
			_ = slow.SetDeadline(time.Now().Add(10 * time.Second))
			if _, err := io.WriteString(slow, "GET /health HT"); err != nil {
				t.Fatal(err)
			}

			var wg sync.WaitGroup
			for i := 0; i < 8; i++ {
				wg.Add(1)
				go func(i int) {
					defer wg.Done()
					path, want := "/", "hello from kestrel\n"
					if i%2 == 1 {
						path, want = "/missing", "not found\n"
					}
					body := httpGet(t, port, path)
					if !strings.HasSuffix(body, want) {
						t.Errorf("GET %s: %q", path, body)
					}
				}(i)
			}
			wg.Wait()

			// Keep-alive with pipelining: three requests in one write.
			c, err := net.DialTimeout("tcp", "127.0.0.1:"+port, 5*time.Second)
			if err != nil {
				t.Fatal(err)
			}
			_ = c.SetDeadline(time.Now().Add(10 * time.Second))
			req := "GET /health HTTP/1.1\r\nHost: x\r\n\r\n" +
				"POST /echo HTTP/1.1\r\nHost: x\r\nContent-Type: text/plain\r\nContent-Length: 5\r\n\r\nhello" +
				"GET / HTTP/1.1\r\nHost: x\r\nConnection: close\r\n\r\n"
			if _, err := io.WriteString(c, req); err != nil {
				t.Fatal(err)
			}
			all, err := io.ReadAll(c)
			c.Close()
			got := string(all)
			if err != nil || strings.Count(got, "HTTP/1.1 200 OK") != 3 || !strings.Contains(got, "Connection: keep-alive") ||
				!strings.Contains(got, `{"bytes": 5, "sha256": "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824", "content_type": "text/plain"}`) ||
				!strings.HasSuffix(got, "hello from kestrel\n") {
				t.Errorf("pipelined: %v\n%s", err, got)
			}

			// Malformed and oversized requests get 400 and a closed connection.
			for _, bad := range []string{"BROKEN\r\n\r\n", "GET / HTTP/1.1\r\nHost: x\r\nContent-Length: 99999999999\r\n\r\n", "GET / HTTP/1.1\r\nHost: x\r\nTransfer-Encoding: chunked\r\n\r\n"} {
				b, err := net.DialTimeout("tcp", "127.0.0.1:"+port, 5*time.Second)
				if err != nil {
					t.Fatal(err)
				}
				_ = b.SetDeadline(time.Now().Add(10 * time.Second))
				_, _ = io.WriteString(b, bad)
				resp, _ := io.ReadAll(b)
				b.Close()
				if !strings.HasPrefix(string(resp), "HTTP/1.1 400 Bad Request") {
					t.Errorf("bad request %q: %q", bad, resp)
				}
			}

			if _, err := io.WriteString(slow, "TP/1.1\r\nHost: x\r\nConnection: close\r\n\r\n"); err != nil {
				t.Fatal(err)
			}
			body, err := io.ReadAll(slow)
			if err != nil || !strings.HasPrefix(string(body), "HTTP/1.1 200 OK") || !strings.HasSuffix(string(body), "\r\n\r\nok\n") {
				t.Fatalf("slow client: %q %v", body, err)
			}

			if err := stop(); err != nil {
				t.Fatalf("stop: %v\n%s", err, stderr.String())
			}
			want := 1
			if mode == "workers" {
				want = 3
			}
			if logs := stderr.String(); strings.Count(logs, "shut down") != want {
				t.Errorf("logs:\n%s", logs)
			}
		})
	}
}

// TestServiceLoad sends 2000 keep-alive requests over 50 connections to a
// release build with 4 workers and requires every one to succeed.
func TestServiceLoad(t *testing.T) {
	requireCC(t)
	if testing.Short() {
		t.Skip("load test")
	}
	port, stop, stderr := startService(t, BuildOptions{Release: true}, "WORKERS=4")
	const conns, perConn = 50, 40
	start := time.Now()
	var wg sync.WaitGroup
	var mu sync.Mutex
	failures := 0
	for i := 0; i < conns; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c, err := net.DialTimeout("tcp", "127.0.0.1:"+port, 5*time.Second)
			if err != nil {
				mu.Lock()
				failures += perConn
				mu.Unlock()
				return
			}
			defer c.Close()
			_ = c.SetDeadline(time.Now().Add(30 * time.Second))
			r := bufio.NewReader(c)
			for j := 0; j < perConn; j++ {
				if _, err := io.WriteString(c, "GET /health HTTP/1.1\r\nHost: x\r\n\r\n"); err != nil {
					mu.Lock()
					failures++
					mu.Unlock()
					return
				}
				resp, err := readResponse(r)
				if err != nil || !strings.HasPrefix(resp, "HTTP/1.1 200 OK") || !strings.HasSuffix(resp, "ok\n") {
					mu.Lock()
					failures++
					mu.Unlock()
					return
				}
			}
		}()
	}
	wg.Wait()
	elapsed := time.Since(start)
	if failures > 0 {
		t.Errorf("%d of %d requests failed\n%s", failures, conns*perConn, stderr.String())
	}
	t.Logf("%d requests over %d connections in %v (%.0f req/s)", conns*perConn, conns, elapsed, float64(conns*perConn)/elapsed.Seconds())
	if err := stop(); err != nil {
		t.Fatalf("stop: %v", err)
	}
}

// readResponse reads one response with a Content-Length body.
func readResponse(r *bufio.Reader) (string, error) {
	var head strings.Builder
	length := 0
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return head.String(), err
		}
		head.WriteString(line)
		if line == "\r\n" {
			break
		}
		if v, ok := strings.CutPrefix(line, "Content-Length: "); ok {
			length, _ = strconv.Atoi(strings.TrimSpace(v))
		}
	}
	body := make([]byte, length)
	if _, err := io.ReadFull(r, body); err != nil {
		return head.String(), err
	}
	return head.String() + string(body), nil
}

// TestWorkerCrashIsolation crashes one worker with a runtime error and
// checks that the other workers keep serving and the crashed one is
// replaced.
func TestWorkerCrashIsolation(t *testing.T) {
	requireCC(t)
	src := `fn main() {
    let s = tcp_listen("127.0.0.1", 0);
    trap_shutdown();
    print(tcp_port(s));
    let w = fork_workers(2);
    while !shutdown_requested() {
        let ready = tcp_wait([s], [], 200);
        if len(ready[0]) == 0 { continue; }
        let c = tcp_try_accept(s);
        if c < 0 { continue; }
        let req = tcp_read(c, 4096);
        if starts_with(req, "crash") {
            let empty: [int] = [];
            print(empty[0]);
        }
        tcp_write(c, "pid " + str(pid()));
        tcp_close(c);
    }
}`
	dir := writeFiles(t, map[string]string{"main.ks": src})
	exe := filepath.Join(dir, "prog")
	if err := Build(filepath.Join(dir, "main.ks"), BuildOptions{Output: exe}); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(exe)
	stdout, _ := cmd.StdoutPipe()
	stderr := &lockedWriter{w: &bytes.Buffer{}}
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill() }()
	line, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	port := strings.TrimSpace(line)
	ask := func(msg string) string {
		c, err := net.DialTimeout("tcp", "127.0.0.1:"+port, 5*time.Second)
		if err != nil {
			return ""
		}
		defer c.Close()
		_ = c.SetDeadline(time.Now().Add(5 * time.Second))
		_, _ = io.WriteString(c, msg)
		b, _ := io.ReadAll(c)
		return string(b)
	}
	if got := ask("crash"); got != "" {
		t.Errorf("crash request got %q", got)
	}
	for i := 0; i < 20; i++ {
		if got := ask("hello"); !strings.HasPrefix(got, "pid ") {
			t.Fatalf("request %d after crash: %q", i, got)
		}
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && !strings.Contains(stderr.String(), "restarting") {
		time.Sleep(50 * time.Millisecond)
	}
	logs := stderr.String()
	if !strings.Contains(logs, "index out of range") || !strings.Contains(logs, "exited with status 101; restarting") {
		t.Errorf("logs: %s", logs)
	}
	_ = cmd.Process.Signal(syscall.SIGTERM)
	waited := make(chan error, 1)
	go func() { waited <- cmd.Wait() }()
	select {
	case err := <-waited:
		if err != nil {
			t.Errorf("supervisor exit: %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Error("supervisor did not stop")
	}
}

func TestServiceRejectsBadConfig(t *testing.T) {
	requireCC(t)
	exe := filepath.Join(t.TempDir(), "service")
	if err := Build(filepath.Join("..", "..", "examples", "http", "service.ks"), BuildOptions{Output: exe}); err != nil {
		t.Fatal(err)
	}
	for env, want := range map[string]string{
		"PORT=http":  "kestrel: error: invalid integer \"http\"\n",
		"PORT=70000": "kestrel: error: PORT must be 0 to 65535, got 70000\n",
	} {
		cmd := exec.Command(exe)
		cmd.Env = append(os.Environ(), env)
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		err := cmd.Run()
		exit, ok := err.(*exec.ExitError)
		if !ok || exit.ExitCode() != 1 || stderr.String() != want {
			t.Errorf("%s: %v, stderr %q", env, err, stderr.String())
		}
	}
}
