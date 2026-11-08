package compiler

import (
	"bufio"
	"bytes"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
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
	if _, err := io.WriteString(conn, "GET "+path+" HTTP/1.1\r\nHost: localhost\r\n\r\n"); err != nil {
		t.Error(err)
		return ""
	}
	body, err := io.ReadAll(conn)
	if err != nil {
		t.Error(err)
	}
	return string(body)
}

// TestServiceExample runs examples/http/service.ks, holds one connection
// open with half a request, serves other clients concurrently, finishes the
// first request, then stops the service with SIGTERM.
func TestServiceExample(t *testing.T) {
	requireCC(t)
	if runtime.GOOS == "windows" {
		t.Skip("uses SIGTERM")
	}
	for _, mode := range []string{"release", "sanitize"} {
		t.Run(mode, func(t *testing.T) {
			opts := BuildOptions{Release: mode == "release", Sanitize: mode == "sanitize"}
			if opts.Sanitize {
				requireSanitizers(t)
			}
			exe := filepath.Join(t.TempDir(), "service")
			opts.Output = exe
			if err := Build(filepath.Join("..", "..", "examples", "http", "service.ks"), opts); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(exe)
			cmd.Env = append(os.Environ(), append(sanitizerEnv(), "PORT=0", "HOST=127.0.0.1")...)
			stdout, err := cmd.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() { done <- cmd.Wait() }()
			defer func() {
				if cmd.ProcessState == nil {
					_ = cmd.Process.Kill()
				}
			}()
			if f, ok := stdout.(*os.File); ok {
				_ = f.SetDeadline(time.Now().Add(10 * time.Second))
			}
			line, err := bufio.NewReader(stdout).ReadString('\n')
			if err != nil {
				t.Fatalf("port: %v stderr %s", err, stderr.String())
			}
			port := strings.TrimSpace(line)

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

			if _, err := io.WriteString(slow, "TP/1.1\r\n\r\n"); err != nil {
				t.Fatal(err)
			}
			body, err := io.ReadAll(slow)
			if err != nil || !strings.HasPrefix(string(body), "HTTP/1.1 200 OK") || !strings.HasSuffix(string(body), "\r\n\r\nok\n") {
				t.Fatalf("slow client: %q %v", body, err)
			}

			if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-done:
				if err != nil {
					t.Fatalf("service exited with %v\n%s", err, stderr.String())
				}
			case <-time.After(10 * time.Second):
				t.Fatalf("service did not stop after SIGTERM\n%s", stderr.String())
			}
			logs := stderr.String()
			if strings.Count(logs, " served 127.0.0.1:") != 9 || !strings.Contains(logs, "shut down") {
				t.Errorf("logs:\n%s", logs)
			}
		})
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
