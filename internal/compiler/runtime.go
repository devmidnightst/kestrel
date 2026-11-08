package compiler

import "strings"

const runtimeHead = `#define _POSIX_C_SOURCE 200809L
#if defined(__APPLE__)
#define _DARWIN_C_SOURCE 1
#else
#define _DEFAULT_SOURCE 1
#endif
#include <errno.h>
#include <fcntl.h>
#include <inttypes.h>
#include <math.h>
#include <netdb.h>
#include <signal.h>
#include <stdarg.h>
#include <stdbool.h>
#include <stddef.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <poll.h>
#include <sys/socket.h>
#include <sys/time.h>
#include <sys/wait.h>
#include <time.h>
#include <unistd.h>
#if defined(__unix__) || defined(__APPLE__)
#include <arpa/inet.h>
#include <netinet/in.h>
#include <sys/resource.h>
#define KS_HAVE_RLIMIT 1
#endif

#define KS_RT static inline __attribute__((unused))

typedef struct ks_obj ks_obj;
struct ks_obj {
	int64_t rc;
	void (*drop)(ks_obj *);
	ks_obj *(*clone)(ks_obj *);
	ks_obj *next;
};

typedef struct {
	ks_obj h;
	int64_t len;
	const char *data;
} ks_str;

#define KS_S(o) ((ks_str *)(o))

static int ks_argc;
static char **ks_argv;
static bool ks_eof_flag;
static uintptr_t ks_stack_base;
static uintptr_t ks_stack_limit;
static ks_obj *ks_pending;
static bool ks_draining;
static volatile sig_atomic_t ks_shutdown_flag;
static bool ks_shutdown_trapped;
static const char *ks_loc;

__attribute__((noreturn, unused)) static void ks_panic(const char *msg) {
	fflush(stdout);
	fprintf(stderr, "kestrel: runtime error: %s\n", msg);
	if (ks_loc != NULL) fprintf(stderr, "    at %s\n", ks_loc);
	fflush(stderr);
	_Exit(101);
}

__attribute__((noreturn, unused, format(printf, 1, 2))) static void ks_panicf(const char *fmt, ...) {
	char buf[512];
	va_list ap;
	va_start(ap, fmt);
	vsnprintf(buf, sizeof buf, fmt, ap);
	va_end(ap);
	ks_panic(buf);
}

KS_RT void ks_init_stack(uintptr_t base) {
	signal(SIGPIPE, SIG_IGN);
	size_t size = 1u << 20;
#ifdef KS_HAVE_RLIMIT
	struct rlimit rl;
	size = 8u << 20;
	if (getrlimit(RLIMIT_STACK, &rl) == 0) {
		if (rl.rlim_cur == RLIM_INFINITY || rl.rlim_cur > ((rlim_t)1 << 30)) {
			size = (size_t)1 << 30;
		} else {
			size = (size_t)rl.rlim_cur;
		}
	}
#endif
	size_t margin = size / 4;
	if (margin < (256u << 10)) margin = 256u << 10;
	if (margin >= size) margin = size / 2;
	ks_stack_base = base;
	ks_stack_limit = size - margin;
}

KS_RT void ks_stack_check(void) {
	uintptr_t here = (uintptr_t)__builtin_frame_address(0);
	uintptr_t used = here < ks_stack_base ? ks_stack_base - here : here - ks_stack_base;
	if (used > ks_stack_limit) ks_panic("stack overflow (recursion too deep)");
}

KS_RT void *ks_alloc(size_t n) {
	void *p = malloc(n == 0 ? 1 : n);
	if (p == NULL) ks_panic("out of memory");
	return p;
}

KS_RT void *ks_realloc(void *p, size_t n) {
	void *r = realloc(p, n == 0 ? 1 : n);
	if (r == NULL) ks_panic("out of memory");
	return r;
}

KS_RT ks_obj *ks_retain(ks_obj *o) {
	if (o->rc >= 0) {
		if (o->rc == INT64_MAX) ks_panic("reference count overflow");
		o->rc++;
	}
	return o;
}

KS_RT void ks_release(ks_obj *o) {
	if (o == NULL || o->rc < 0) return;
	if (--o->rc > 0) return;
	o->next = ks_pending;
	ks_pending = o;
	if (ks_draining) return;
	ks_draining = true;
	while (ks_pending != NULL) {
		ks_obj *p = ks_pending;
		ks_pending = p->next;
		p->drop(p);
	}
	ks_draining = false;
}

KS_RT ks_obj *ks_unique(ks_obj **slot) {
	ks_obj *o = *slot;
	if (o->rc != 1) {
		ks_obj *c = o->clone(o);
		ks_release(o);
		*slot = c;
		return c;
	}
	return o;
}

KS_RT void ks_init_obj(ks_obj *o, void (*drop)(ks_obj *), ks_obj *(*clone)(ks_obj *)) {
	o->rc = 1;
	o->drop = drop;
	o->clone = clone;
	o->next = NULL;
}

KS_RT int64_t ks_check_index(int64_t i, int64_t len) {
	if (i < 0 || i >= len) ks_panicf("index out of range: index %" PRId64 ", length %" PRId64, i, len);
	return i;
}

KS_RT int64_t ks_add(int64_t a, int64_t b) {
	int64_t r;
	if (__builtin_add_overflow(a, b, &r)) ks_panic("integer overflow");
	return r;
}

KS_RT int64_t ks_sub(int64_t a, int64_t b) {
	int64_t r;
	if (__builtin_sub_overflow(a, b, &r)) ks_panic("integer overflow");
	return r;
}

KS_RT int64_t ks_mul(int64_t a, int64_t b) {
	int64_t r;
	if (__builtin_mul_overflow(a, b, &r)) ks_panic("integer overflow");
	return r;
}

KS_RT int64_t ks_div(int64_t a, int64_t b) {
	if (b == 0) ks_panic("division by zero");
	if (a == INT64_MIN && b == -1) ks_panic("integer overflow");
	return a / b;
}

KS_RT int64_t ks_mod(int64_t a, int64_t b) {
	if (b == 0) ks_panic("division by zero");
	if (b == -1) return 0;
	return a % b;
}

KS_RT int64_t ks_neg(int64_t a) {
	if (a == INT64_MIN) ks_panic("integer overflow");
	return -a;
}

KS_RT int64_t ks_abs_i64(int64_t a) {
	if (a == INT64_MIN) ks_panic("integer overflow");
	return a < 0 ? -a : a;
}

KS_RT int64_t ks_min_i64(int64_t a, int64_t b) { return a < b ? a : b; }
KS_RT int64_t ks_max_i64(int64_t a, int64_t b) { return a > b ? a : b; }
KS_RT double ks_min_f64(double a, double b) { return a < b ? a : b; }
KS_RT double ks_max_f64(double a, double b) { return a > b ? a : b; }

`

const arrayTemplate = `
typedef struct {
	ks_obj h;
	int64_t len;
	int64_t cap;
	ELEM *data;
} ks_arr_KIND;

KS_RT void ks_arr_KIND_drop(ks_obj *o) {
	ks_arr_KIND *a = (ks_arr_KIND *)o;
	for (int64_t i = 0; i < a->len; i++) RELEASE(a->data[i]);
	free(a->data);
	free(a);
}

KS_RT ks_obj *ks_arr_KIND_clone(ks_obj *o);

KS_RT ks_obj *ks_arr_KIND_new(int64_t cap) {
	if (cap < 0) cap = 0;
	if ((uint64_t)cap > SIZE_MAX / sizeof(ELEM)) ks_panic("array too large");
	ks_arr_KIND *a = ks_alloc(sizeof *a);
	ks_init_obj(&a->h, ks_arr_KIND_drop, ks_arr_KIND_clone);
	a->len = 0;
	a->cap = cap;
	a->data = cap > 0 ? ks_alloc((size_t)cap * sizeof(ELEM)) : NULL;
	return &a->h;
}

KS_RT ks_obj *ks_arr_KIND_clone(ks_obj *o) {
	ks_arr_KIND *a = (ks_arr_KIND *)o;
	ks_obj *r = ks_arr_KIND_new(a->len);
	ks_arr_KIND *c = (ks_arr_KIND *)r;
	for (int64_t i = 0; i < a->len; i++) c->data[i] = RETAIN(a->data[i]);
	c->len = a->len;
	return r;
}

KS_RT int64_t ks_arr_KIND_len(ks_obj *o) {
	return ((ks_arr_KIND *)o)->len;
}

KS_RT void ks_arr_KIND_push(ks_obj *o, ELEM v) {
	ks_arr_KIND *a = (ks_arr_KIND *)o;
	if (a->len == a->cap) {
		int64_t nc = 4;
		if (a->cap >= 4) {
			if (a->cap > INT64_MAX / 2) ks_panic("array too large");
			nc = a->cap * 2;
		}
		if ((uint64_t)nc > SIZE_MAX / sizeof(ELEM)) ks_panic("array too large");
		a->data = ks_realloc(a->data, (size_t)nc * sizeof(ELEM));
		a->cap = nc;
	}
	a->data[a->len++] = v;
}

KS_RT ELEM ks_arr_KIND_pop(ks_obj *o) {
	ks_arr_KIND *a = (ks_arr_KIND *)o;
	if (a->len == 0) ks_panic("pop from an empty array");
	return a->data[--a->len];
}

KS_RT ELEM ks_arr_KIND_get(ks_obj *o, int64_t i) {
	ks_arr_KIND *a = (ks_arr_KIND *)o;
	return a->data[ks_check_index(i, a->len)];
}

KS_RT ELEM *ks_arr_KIND_slot(ks_obj *o, int64_t i) {
	ks_arr_KIND *a = (ks_arr_KIND *)o;
	return &a->data[ks_check_index(i, a->len)];
}

KS_RT ks_obj *ks_arr_KIND_fill(ELEM v, int64_t n) {
	if (n < 0) ks_panicf("fill: negative count %" PRId64, n);
	ks_obj *r = ks_arr_KIND_new(n);
	ks_arr_KIND *a = (ks_arr_KIND *)r;
	for (int64_t i = 0; i < n; i++) a->data[i] = i == 0 ? v : RETAIN(v);
	a->len = n;
	if (n == 0) RELEASE(v);
	return r;
}

KS_RT ks_obj *ks_arr_KIND_slice(ks_obj *o, int64_t start, int64_t count) {
	ks_arr_KIND *a = (ks_arr_KIND *)o;
	if (start < 0 || start > a->len || count < 0 || count > a->len - start) {
		ks_panicf("slice out of range: start %" PRId64 ", count %" PRId64 ", length %" PRId64, start, count, a->len);
	}
	ks_obj *r = ks_arr_KIND_new(count);
	ks_arr_KIND *c = (ks_arr_KIND *)r;
	for (int64_t i = 0; i < count; i++) c->data[i] = RETAIN(a->data[start + i]);
	c->len = count;
	return r;
}
`

const runtimeTail = `
KS_RT void ks_str_drop(ks_obj *o) {
	free(o);
}

KS_RT ks_obj *ks_str_alloc(int64_t len, char **out) {
	if (len < 0 || (uint64_t)len > SIZE_MAX - sizeof(ks_str) - 1) ks_panic("string too long");
	ks_str *s = ks_alloc(sizeof(ks_str) + (size_t)len + 1);
	ks_init_obj(&s->h, ks_str_drop, NULL);
	char *d = (char *)(s + 1);
	d[len] = 0;
	s->data = d;
	s->len = len;
	*out = d;
	return &s->h;
}

KS_RT ks_obj *ks_str_from(const char *p, int64_t len) {
	char *d;
	ks_obj *o = ks_str_alloc(len, &d);
	if (len > 0) memcpy(d, p, (size_t)len);
	return o;
}

KS_RT ks_obj *ks_str_cstr(const char *p) {
	return ks_str_from(p, (int64_t)strlen(p));
}

KS_RT int64_t ks_str_len(ks_obj *s) {
	return KS_S(s)->len;
}

KS_RT ks_obj *ks_str_concat(ks_obj *ao, ks_obj *bo) {
	ks_str *a = KS_S(ao), *b = KS_S(bo);
	if (a->len > INT64_MAX - b->len) ks_panic("string too long");
	char *d;
	ks_obj *r = ks_str_alloc(a->len + b->len, &d);
	if (a->len > 0) memcpy(d, a->data, (size_t)a->len);
	if (b->len > 0) memcpy(d + a->len, b->data, (size_t)b->len);
	return r;
}

KS_RT int ks_str_cmp(ks_obj *ao, ks_obj *bo) {
	ks_str *a = KS_S(ao), *b = KS_S(bo);
	int64_t n = a->len < b->len ? a->len : b->len;
	int c = n > 0 ? memcmp(a->data, b->data, (size_t)n) : 0;
	if (c != 0) return c < 0 ? -1 : 1;
	return a->len < b->len ? -1 : (a->len > b->len ? 1 : 0);
}

KS_RT bool ks_str_eq(ks_obj *a, ks_obj *b) {
	return KS_S(a)->len == KS_S(b)->len && ks_str_cmp(a, b) == 0;
}

KS_RT int ks_fmt_float(double v, char *buf, size_t n) {
	if (isnan(v)) return snprintf(buf, n, "nan");
	if (isinf(v)) return snprintf(buf, n, v > 0 ? "inf" : "-inf");
	if (v == 0) return snprintf(buf, n, signbit(v) ? "-0.0" : "0.0");
	int p = 1;
	for (; p < 17; p++) {
		snprintf(buf, n, "%.*e", p - 1, v);
		if (strtod(buf, NULL) == v) break;
	}
	snprintf(buf, n, "%.*e", p - 1, v);
	const char *e = strchr(buf, 'e');
	int x = e != NULL ? atoi(e + 1) : 0;
	int len;
	if (x >= -5 && x < 16) {
		int d = p - 1 - x;
		if (d < 0) d = 0;
		len = snprintf(buf, n, "%.*f", d, v);
	} else {
		len = snprintf(buf, n, "%.*e", p - 1, v);
	}
	if (strpbrk(buf, ".e") == NULL && (size_t)len + 3 <= n) {
		buf[len++] = '.';
		buf[len++] = '0';
		buf[len] = 0;
	}
	return len;
}

KS_RT int64_t ks_float_to_int(double f) {
	if (!(f >= -9223372036854775808.0 && f < 9223372036854775808.0)) {
		char buf[48];
		ks_fmt_float(f, buf, sizeof buf);
		ks_panicf("int: %s cannot be converted to int", buf);
	}
	return (int64_t)f;
}

KS_RT ks_obj *ks_str_int(int64_t v) {
	char buf[32];
	int n = snprintf(buf, sizeof buf, "%" PRId64, v);
	return ks_str_from(buf, n);
}

KS_RT ks_obj *ks_str_float(double v) {
	char buf[48];
	int n = ks_fmt_float(v, buf, sizeof buf);
	return ks_str_from(buf, n);
}

KS_RT ks_obj *ks_str_bool(bool v) {
	return ks_str_cstr(v ? "true" : "false");
}

KS_RT void ks_print_i64(int64_t v, FILE *out) {
	fprintf(out, "%" PRId64, v);
}

KS_RT void ks_print_bool(bool v, FILE *out) {
	fputs(v ? "true" : "false", out);
}

KS_RT void ks_print_f64(double v, FILE *out) {
	char buf[48];
	ks_fmt_float(v, buf, sizeof buf);
	fputs(buf, out);
}

KS_RT void ks_print_str(ks_obj *s, FILE *out) {
	if (KS_S(s)->len > 0) fwrite(KS_S(s)->data, 1, (size_t)KS_S(s)->len, out);
}

KS_RT void ks_print_str_quoted(ks_obj *o, FILE *out) {
	ks_str *s = KS_S(o);
	fputc('"', out);
	for (int64_t i = 0; i < s->len; i++) {
		char c = s->data[i];
		switch (c) {
		case '"': fputs("\\\"", out); break;
		case '\\': fputs("\\\\", out); break;
		case '\n': fputs("\\n", out); break;
		case '\t': fputs("\\t", out); break;
		case '\r': fputs("\\r", out); break;
		default: fputc(c, out);
		}
	}
	fputc('"', out);
}

KS_RT ks_obj *ks_substr(ks_obj *so, int64_t start, int64_t count) {
	ks_str *s = KS_S(so);
	if (start < 0 || start > s->len || count < 0 || count > s->len - start) {
		ks_panicf("substr out of range: start %" PRId64 ", count %" PRId64 ", length %" PRId64, start, count, s->len);
	}
	return ks_str_from(s->data + start, count);
}

KS_RT int64_t ks_find(ks_obj *so, ks_obj *subo) {
	ks_str *s = KS_S(so), *sub = KS_S(subo);
	if (sub->len == 0) return 0;
	for (int64_t i = 0; i + sub->len <= s->len; i++) {
		if (memcmp(s->data + i, sub->data, (size_t)sub->len) == 0) return i;
	}
	return -1;
}

KS_RT bool ks_contains(ks_obj *s, ks_obj *sub) {
	return ks_find(s, sub) >= 0;
}

KS_RT bool ks_starts_with(ks_obj *so, ks_obj *po) {
	ks_str *s = KS_S(so), *p = KS_S(po);
	return p->len <= s->len && (p->len == 0 || memcmp(s->data, p->data, (size_t)p->len) == 0);
}

KS_RT bool ks_ends_with(ks_obj *so, ks_obj *po) {
	ks_str *s = KS_S(so), *p = KS_S(po);
	return p->len <= s->len && (p->len == 0 || memcmp(s->data + s->len - p->len, p->data, (size_t)p->len) == 0);
}

KS_RT ks_obj *ks_split(ks_obj *so, ks_obj *sepo) {
	ks_str *s = KS_S(so), *sep = KS_S(sepo);
	if (sep->len == 0) ks_panic("split: empty separator");
	ks_obj *r = ks_arr_obj_new(0);
	int64_t start = 0;
	int64_t i = 0;
	while (i + sep->len <= s->len) {
		if (memcmp(s->data + i, sep->data, (size_t)sep->len) == 0) {
			ks_arr_obj_push(r, ks_str_from(s->data + start, i - start));
			i += sep->len;
			start = i;
		} else {
			i++;
		}
	}
	ks_arr_obj_push(r, ks_str_from(s->data + start, s->len - start));
	return r;
}

KS_RT ks_obj *ks_join(ks_obj *ao, ks_obj *sepo) {
	ks_arr_obj *a = (ks_arr_obj *)ao;
	ks_str *sep = KS_S(sepo);
	int64_t total = 0;
	for (int64_t i = 0; i < a->len; i++) {
		int64_t add = KS_S(a->data[i])->len + (i > 0 ? sep->len : 0);
		if (total > INT64_MAX - add) ks_panic("string too long");
		total += add;
	}
	char *d;
	ks_obj *r = ks_str_alloc(total, &d);
	for (int64_t i = 0; i < a->len; i++) {
		if (i > 0 && sep->len > 0) {
			memcpy(d, sep->data, (size_t)sep->len);
			d += sep->len;
		}
		ks_str *p = KS_S(a->data[i]);
		if (p->len > 0) memcpy(d, p->data, (size_t)p->len);
		d += p->len;
	}
	return r;
}

KS_RT bool ks_is_space(char c) {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\v' || c == '\f';
}

KS_RT ks_obj *ks_trim(ks_obj *so) {
	ks_str *s = KS_S(so);
	int64_t a = 0, b = s->len;
	while (a < b && ks_is_space(s->data[a])) a++;
	while (b > a && ks_is_space(s->data[b - 1])) b--;
	return ks_str_from(s->data + a, b - a);
}

KS_RT ks_obj *ks_case(ks_obj *so, bool up) {
	ks_str *s = KS_S(so);
	char *d;
	ks_obj *r = ks_str_alloc(s->len, &d);
	for (int64_t i = 0; i < s->len; i++) {
		char c = s->data[i];
		if (up && c >= 'a' && c <= 'z') c = (char)(c - 'a' + 'A');
		if (!up && c >= 'A' && c <= 'Z') c = (char)(c - 'A' + 'a');
		d[i] = c;
	}
	return r;
}

KS_RT ks_obj *ks_upper(ks_obj *s) { return ks_case(s, true); }
KS_RT ks_obj *ks_lower(ks_obj *s) { return ks_case(s, false); }

KS_RT int64_t ks_byte_at(ks_obj *so, int64_t i) {
	ks_str *s = KS_S(so);
	return (unsigned char)s->data[ks_check_index(i, s->len)];
}

KS_RT ks_obj *ks_chr(int64_t b) {
	if (b < 0 || b > 255) ks_panicf("chr: %" PRId64 " is not a byte value (0 to 255)", b);
	char c = (char)(unsigned char)b;
	return ks_str_from(&c, 1);
}

KS_RT bool ks_parse_int_raw(ks_obj *so, int64_t *out) {
	ks_str *s = KS_S(so);
	const char *p = s->data;
	int64_t n = s->len, i = 0;
	bool neg = false;
	if (n > 0 && (p[0] == '-' || p[0] == '+')) {
		neg = p[0] == '-';
		i = 1;
	}
	if (i == n) return false;
	uint64_t limit = neg ? (uint64_t)INT64_MAX + 1 : (uint64_t)INT64_MAX;
	uint64_t v = 0;
	for (; i < n; i++) {
		if (p[i] < '0' || p[i] > '9') return false;
		uint64_t d = (uint64_t)(p[i] - '0');
		if (v > (limit - d) / 10) return false;
		v = v * 10 + d;
	}
	if (!neg) *out = (int64_t)v;
	else if (v == (uint64_t)INT64_MAX + 1) *out = INT64_MIN;
	else *out = -(int64_t)v;
	return true;
}

KS_RT bool ks_is_int(ks_obj *s) {
	int64_t v;
	return ks_parse_int_raw(s, &v);
}

KS_RT int64_t ks_parse_int(ks_obj *s) {
	int64_t v;
	if (!ks_parse_int_raw(s, &v)) ks_panicf("parse_int: invalid integer \"%.100s\"", KS_S(s)->data);
	return v;
}

KS_RT bool ks_is_digit(char c) {
	return c >= '0' && c <= '9';
}

KS_RT bool ks_is_float(ks_obj *so) {
	ks_str *s = KS_S(so);
	const char *p = s->data;
	int64_t n = s->len, i = 0, digits = 0;
	if (i < n && (p[i] == '+' || p[i] == '-')) i++;
	while (i < n && ks_is_digit(p[i])) { i++; digits++; }
	if (i < n && p[i] == '.') {
		i++;
		while (i < n && ks_is_digit(p[i])) { i++; digits++; }
	}
	if (digits == 0) return false;
	if (i < n && (p[i] == 'e' || p[i] == 'E')) {
		i++;
		if (i < n && (p[i] == '+' || p[i] == '-')) i++;
		int64_t ed = 0;
		while (i < n && ks_is_digit(p[i])) { i++; ed++; }
		if (ed == 0) return false;
	}
	return i == n;
}

KS_RT double ks_parse_float(ks_obj *s) {
	if (!ks_is_float(s)) ks_panicf("parse_float: invalid number \"%.100s\"", KS_S(s)->data);
	return strtod(KS_S(s)->data, NULL);
}

KS_RT ks_obj *ks_read_line(void) {
	fflush(stdout);
	size_t cap = 128, len = 0;
	char *buf = ks_alloc(cap);
	bool any = false;
	int c;
	while ((c = fgetc(stdin)) != EOF) {
		any = true;
		if (c == '\n') break;
		if (len + 1 >= cap) {
			if (cap > SIZE_MAX / 2) ks_panic("line too long");
			cap *= 2;
			buf = ks_realloc(buf, cap);
		}
		buf[len++] = (char)c;
	}
	if (!any) ks_eof_flag = true;
	if (len > 0 && buf[len - 1] == '\r') len--;
	ks_obj *r = ks_str_from(buf, (int64_t)len);
	free(buf);
	return r;
}

KS_RT bool ks_eof(void) {
	return ks_eof_flag;
}

KS_RT ks_obj *ks_args(void) {
	ks_obj *r = ks_arr_obj_new(ks_argc > 1 ? ks_argc - 1 : 0);
	for (int i = 1; i < ks_argc; i++) ks_arr_obj_push(r, ks_str_cstr(ks_argv[i]));
	return r;
}

KS_RT const char *ks_cpath(ks_obj *p, const char *fn) {
	ks_str *s = KS_S(p);
	if (s->len == 0) ks_panicf("%s: empty path", fn);
	if (memchr(s->data, 0, (size_t)s->len) != NULL) ks_panicf("%s: path contains a NUL byte", fn);
	return s->data;
}

// ks_slurp reads all of f into a new string. It returns NULL on a read error.
KS_RT ks_obj *ks_slurp(FILE *f) {
	size_t cap = 4096, len = 0;
	char *buf = ks_alloc(cap);
	for (;;) {
		if (len == cap) {
			if (cap > SIZE_MAX / 2) ks_panic("read_file: file too large");
			cap *= 2;
			buf = ks_realloc(buf, cap);
		}
		size_t n = fread(buf + len, 1, cap - len, f);
		len += n;
		if (n == 0) break;
	}
	if (ferror(f)) {
		free(buf);
		return NULL;
	}
	if (len > (size_t)INT64_MAX) ks_panic("read_file: file too large");
	ks_obj *r = ks_str_from(buf, (int64_t)len);
	free(buf);
	return r;
}

KS_RT ks_obj *ks_read_file(ks_obj *path) {
	const char *p = ks_cpath(path, "read_file");
	FILE *f = fopen(p, "rb");
	if (f == NULL) ks_panicf("read_file: cannot open \"%.200s\": %s", p, strerror(errno));
	ks_obj *r = ks_slurp(f);
	fclose(f);
	if (r == NULL) ks_panicf("read_file: error reading \"%.200s\"", p);
	return r;
}

KS_RT bool ks_write_file(ks_obj *path, ks_obj *content) {
	const char *p = ks_cpath(path, "write_file");
	FILE *f = fopen(p, "wb");
	if (f == NULL) return false;
	ks_str *s = KS_S(content);
	bool ok = s->len == 0 || fwrite(s->data, 1, (size_t)s->len, f) == (size_t)s->len;
	if (fclose(f) != 0) ok = false;
	return ok;
}

KS_RT bool ks_file_exists(ks_obj *path) {
	const char *p = ks_cpath(path, "file_exists");
	FILE *f = fopen(p, "rb");
	if (f == NULL) return false;
	fclose(f);
	return true;
}

KS_RT int ks_tcp_cloexec(int fd) {
	int flags = fcntl(fd, F_GETFD);
	if (flags >= 0) fcntl(fd, F_SETFD, flags | FD_CLOEXEC);
	return fd;
}

// Listening sockets are non-blocking so that tcp_try_accept never hangs when
// another worker took the connection first; tcp_accept waits with poll.
KS_RT void ks_set_nonblock(int fd, bool on) {
	int fl = fcntl(fd, F_GETFL);
	if (fl < 0) return;
	fcntl(fd, F_SETFL, on ? (fl | O_NONBLOCK) : (fl & ~O_NONBLOCK));
}

KS_RT int64_t ks_tcp_open(ks_obj *host, int64_t port, bool listen_mode, bool shared) {
	if (port < 0 || port > 65535) return -1;
	ks_str *h = KS_S(host);
	if (h->len == 0 || h->len > 255 || memchr(h->data, 0, (size_t)h->len) != NULL) return -1;
	char hostbuf[256];
	memcpy(hostbuf, h->data, (size_t)h->len);
	hostbuf[h->len] = 0;
	char portbuf[16];
	snprintf(portbuf, sizeof portbuf, "%" PRId64, port);
	struct addrinfo hints;
	memset(&hints, 0, sizeof hints);
	hints.ai_family = AF_UNSPEC;
	hints.ai_socktype = SOCK_STREAM;
	if (listen_mode) hints.ai_flags = AI_PASSIVE;
	struct addrinfo *res = NULL;
	if (getaddrinfo(hostbuf, portbuf, &hints, &res) != 0) return -1;
	int fd = -1;
	for (struct addrinfo *ai = res; ai != NULL; ai = ai->ai_next) {
		fd = socket(ai->ai_family, ai->ai_socktype, ai->ai_protocol);
		if (fd < 0) continue;
		ks_tcp_cloexec(fd);
		if (listen_mode) {
			int one = 1;
			setsockopt(fd, SOL_SOCKET, SO_REUSEADDR, &one, sizeof one);
			if (shared) {
#ifdef SO_REUSEPORT
				if (setsockopt(fd, SOL_SOCKET, SO_REUSEPORT, &one, sizeof one) != 0) {
					close(fd);
					fd = -1;
					continue;
				}
#else
				close(fd);
				fd = -1;
				continue;
#endif
			}
			if (bind(fd, ai->ai_addr, ai->ai_addrlen) == 0 && listen(fd, 1024) == 0) {
				ks_set_nonblock(fd, true);
				break;
			}
		} else if (connect(fd, ai->ai_addr, ai->ai_addrlen) == 0) {
			break;
		}
		close(fd);
		fd = -1;
	}
	freeaddrinfo(res);
	return fd;
}

KS_RT int64_t ks_tcp_listen(ks_obj *host, int64_t port) {
	return ks_tcp_open(host, port, true, false);
}

KS_RT int64_t ks_tcp_listen_shared(ks_obj *host, int64_t port) {
	return ks_tcp_open(host, port, true, true);
}

KS_RT int64_t ks_tcp_connect(ks_obj *host, int64_t port) {
	return ks_tcp_open(host, port, false, false);
}

KS_RT int64_t ks_tcp_port(int64_t fd) {
	if (fd < 0 || fd > INT32_MAX) return -1;
	struct sockaddr_storage ss;
	socklen_t len = sizeof ss;
	if (getsockname((int)fd, (struct sockaddr *)&ss, &len) != 0) return -1;
	if (ss.ss_family == AF_INET) return ntohs(((struct sockaddr_in *)&ss)->sin_port);
	if (ss.ss_family == AF_INET6) return ntohs(((struct sockaddr_in6 *)&ss)->sin6_port);
	return -1;
}

KS_RT int64_t ks_accept_one(int fd) {
	int c = accept(fd, NULL, NULL);
	if (c < 0) return -1;
	ks_set_nonblock(c, false);
	return ks_tcp_cloexec(c);
}

KS_RT int64_t ks_tcp_accept(int64_t fd) {
	if (fd < 0 || fd > INT32_MAX) return -1;
	for (;;) {
		int64_t c = ks_accept_one((int)fd);
		if (c >= 0) return c;
		if (errno != EAGAIN && errno != EWOULDBLOCK && errno != ECONNABORTED) return -1;
		struct pollfd p = {(int)fd, POLLIN, 0};
		if (poll(&p, 1, -1) < 0) return -1;
	}
}

KS_RT int64_t ks_tcp_try_accept(int64_t fd) {
	if (fd < 0 || fd > INT32_MAX) return -1;
	return ks_accept_one((int)fd);
}

KS_RT int64_t ks_tcp_connect_timeout(ks_obj *host, int64_t port, int64_t ms) {
	if (port < 0 || port > 65535) return -1;
	ks_str *h = KS_S(host);
	if (h->len == 0 || h->len > 255 || memchr(h->data, 0, (size_t)h->len) != NULL) return -1;
	char portbuf[16];
	snprintf(portbuf, sizeof portbuf, "%" PRId64, port);
	struct addrinfo hints;
	memset(&hints, 0, sizeof hints);
	hints.ai_family = AF_UNSPEC;
	hints.ai_socktype = SOCK_STREAM;
	struct addrinfo *res = NULL;
	if (getaddrinfo(h->data, portbuf, &hints, &res) != 0) return -1;
	int fd = -1;
	int t = ms < 0 ? -1 : (ms > INT32_MAX ? INT32_MAX : (int)ms);
	for (struct addrinfo *ai = res; ai != NULL; ai = ai->ai_next) {
		fd = socket(ai->ai_family, ai->ai_socktype, ai->ai_protocol);
		if (fd < 0) continue;
		ks_tcp_cloexec(fd);
		ks_set_nonblock(fd, true);
		int rc = connect(fd, ai->ai_addr, ai->ai_addrlen);
		if (rc != 0 && errno == EINPROGRESS) {
			struct pollfd p = {fd, POLLOUT, 0};
			int err = 0;
			socklen_t len = sizeof err;
			if (poll(&p, 1, t) == 1 && getsockopt(fd, SOL_SOCKET, SO_ERROR, &err, &len) == 0 && err == 0) rc = 0;
		}
		if (rc == 0) {
			ks_set_nonblock(fd, false);
			break;
		}
		close(fd);
		fd = -1;
	}
	freeaddrinfo(res);
	return fd;
}

KS_RT int64_t ks_tcp_send(int64_t fd, ks_obj *data) {
	if (fd < 0 || fd > INT32_MAX) return -1;
	ks_str *s = KS_S(data);
	if (s->len == 0) return 0;
	int flags = MSG_DONTWAIT;
#ifdef MSG_NOSIGNAL
	flags |= MSG_NOSIGNAL;
#endif
	ssize_t n = send((int)fd, s->data, (size_t)s->len, flags);
	if (n < 0) return (errno == EAGAIN || errno == EWOULDBLOCK || errno == EINTR) ? 0 : -1;
	return (int64_t)n;
}

KS_RT ks_obj *ks_tcp_read(int64_t fd, int64_t maxn) {
	if (fd < 0 || fd > INT32_MAX || maxn <= 0) return ks_str_from("", 0);
	if (maxn > (1 << 20)) maxn = 1 << 20;
	char *buf = ks_alloc((size_t)maxn);
	ssize_t n = read((int)fd, buf, (size_t)maxn);
	if (n < 0) n = 0;
	ks_obj *r = ks_str_from(buf, (int64_t)n);
	free(buf);
	return r;
}

KS_RT int64_t ks_tcp_write(int64_t fd, ks_obj *data) {
	if (fd < 0 || fd > INT32_MAX) return -1;
	ks_str *s = KS_S(data);
	int64_t off = 0;
	while (off < s->len) {
		ssize_t n = write((int)fd, s->data + off, (size_t)(s->len - off));
		if (n < 0) {
			if (errno == EINTR) continue;
			return -1;
		}
		off += n;
	}
	return off;
}

KS_RT void ks_tcp_close(int64_t fd) {
	if (fd < 0 || fd > INT32_MAX) return;
	close((int)fd);
}

KS_RT void ks_exit(int64_t code) {
	fflush(stdout);
	fflush(stderr);
	_Exit((int)code);
}

KS_RT void ks_assert(bool cond, ks_obj *msg) {
	if (!cond) ks_panicf("assertion failed: %.400s", KS_S(msg)->data);
}

KS_RT int64_t ks_fmt_holes(ks_str *fmt) {
	int64_t n = 0;
	for (int64_t i = 0; i < fmt->len; i++) {
		char c = fmt->data[i];
		if (c == '{') {
			if (i + 1 < fmt->len && fmt->data[i + 1] == '{') { i++; continue; }
			if (i + 1 < fmt->len && fmt->data[i + 1] == '}') { n++; i++; continue; }
			ks_panic("fmt: unclosed or unsupported '{' (only {} is a placeholder)");
		}
		if (c == '}') {
			if (i + 1 < fmt->len && fmt->data[i + 1] == '}') { i++; continue; }
			ks_panic("fmt: lone '}' (write }} for a literal brace)");
		}
	}
	return n;
}

KS_RT ks_obj *ks_fmt(ks_obj *fmto, int64_t n, ks_obj **args) {
	ks_str *fmt = KS_S(fmto);
	int64_t holes = ks_fmt_holes(fmt);
	if (holes != n) ks_panicf("fmt: format has %" PRId64 " {} placeholders, got %" PRId64 " values", holes, n);
	int64_t outlen = 0;
	int64_t ai = 0;
	for (int64_t i = 0; i < fmt->len; i++) {
		char c = fmt->data[i];
		if (c == '{' && fmt->data[i + 1] == '{') { outlen++; i++; continue; }
		if (c == '}' && fmt->data[i + 1] == '}') { outlen++; i++; continue; }
		if (c == '{') {
			ks_str *a = KS_S(args[ai++]);
			if (a->len > INT64_MAX - outlen) ks_panic("string too long");
			outlen += a->len;
			i++;
			continue;
		}
		if (outlen == INT64_MAX) ks_panic("string too long");
		outlen++;
	}
	char *d;
	ks_obj *r = ks_str_alloc(outlen, &d);
	int64_t w = 0;
	ai = 0;
	for (int64_t i = 0; i < fmt->len; i++) {
		char c = fmt->data[i];
		if (c == '{' && fmt->data[i + 1] == '{') { d[w++] = '{'; i++; continue; }
		if (c == '}' && fmt->data[i + 1] == '}') { d[w++] = '}'; i++; continue; }
		if (c == '{') {
			ks_str *a = KS_S(args[ai++]);
			if (a->len > 0) memcpy(d + w, a->data, (size_t)a->len);
			w += a->len;
			i++;
			continue;
		}
		d[w++] = c;
	}
	return r;
}

KS_RT ks_obj *ks_tcp_poll(ks_obj *fdso, int64_t timeout) {
	ks_arr_i64 *a = (ks_arr_i64 *)fdso;
	ks_obj *r = ks_arr_i64_new(0);
	if ((uint64_t)a->len > SIZE_MAX / sizeof(struct pollfd) || a->len > (int64_t)INT32_MAX) ks_panic("tcp_poll: too many sockets");
	struct pollfd *pfd = ks_alloc((size_t)a->len * sizeof(struct pollfd));
	for (int64_t i = 0; i < a->len; i++) {
		int64_t fd = a->data[i];
		pfd[i].fd = fd < 0 || fd > INT32_MAX ? -1 : (int)fd;
		pfd[i].events = POLLIN;
		pfd[i].revents = 0;
	}
	int t = timeout < 0 ? -1 : (timeout > INT32_MAX ? INT32_MAX : (int)timeout);
	int n = poll(pfd, (nfds_t)a->len, t);
	if (n > 0) {
		for (int64_t i = 0; i < a->len; i++) {
			if (pfd[i].fd >= 0 && (pfd[i].revents & (POLLIN | POLLHUP | POLLERR | POLLNVAL))) ks_arr_i64_push(r, a->data[i]);
		}
	}
	free(pfd);
	return r;
}

KS_RT ks_obj *ks_tcp_wait(ks_obj *ro, ks_obj *wo, int64_t timeout) {
	ks_arr_i64 *r = (ks_arr_i64 *)ro, *w = (ks_arr_i64 *)wo;
	if (r->len > (int64_t)INT32_MAX / 2 || w->len > (int64_t)INT32_MAX / 2) ks_panic("tcp_wait: too many sockets");
	int64_t n = r->len + w->len;
	struct pollfd *pfd = ks_alloc((size_t)n * sizeof(struct pollfd));
	for (int64_t i = 0; i < n; i++) {
		int64_t fd = i < r->len ? r->data[i] : w->data[i - r->len];
		pfd[i].fd = fd < 0 || fd > INT32_MAX ? -1 : (int)fd;
		pfd[i].events = i < r->len ? POLLIN : POLLOUT;
		pfd[i].revents = 0;
	}
	int t = timeout < 0 ? -1 : (timeout > INT32_MAX ? INT32_MAX : (int)timeout);
	int got = poll(pfd, (nfds_t)n, t);
	ks_obj *readable = ks_arr_i64_new(0), *writable = ks_arr_i64_new(0);
	if (got > 0) {
		for (int64_t i = 0; i < n; i++) {
			if (pfd[i].fd < 0) continue;
			short want = i < r->len ? POLLIN : POLLOUT;
			if (!(pfd[i].revents & (want | POLLHUP | POLLERR | POLLNVAL))) continue;
			if (i < r->len) ks_arr_i64_push(readable, r->data[i]);
			else ks_arr_i64_push(writable, w->data[i - r->len]);
		}
	}
	free(pfd);
	ks_obj *out = ks_arr_obj_new(2);
	ks_arr_obj_push(out, readable);
	ks_arr_obj_push(out, writable);
	return out;
}

KS_RT bool ks_tcp_set_timeout(int64_t fd, int64_t ms) {
	if (fd < 0 || fd > INT32_MAX) return false;
	if (ms < 0) ms = 0;
	struct timeval tv;
	tv.tv_sec = (time_t)(ms / 1000);
	tv.tv_usec = (int)((ms % 1000) * 1000);
	return setsockopt((int)fd, SOL_SOCKET, SO_RCVTIMEO, &tv, sizeof tv) == 0 &&
		setsockopt((int)fd, SOL_SOCKET, SO_SNDTIMEO, &tv, sizeof tv) == 0;
}

KS_RT ks_obj *ks_tcp_peer(int64_t fd) {
	if (fd < 0 || fd > INT32_MAX) return ks_str_from("", 0);
	struct sockaddr_storage ss;
	socklen_t len = sizeof ss;
	if (getpeername((int)fd, (struct sockaddr *)&ss, &len) != 0) return ks_str_from("", 0);
	char host[INET6_ADDRSTRLEN];
	char buf[INET6_ADDRSTRLEN + 16];
	int n;
	if (ss.ss_family == AF_INET) {
		struct sockaddr_in *sin = (struct sockaddr_in *)&ss;
		if (inet_ntop(AF_INET, &sin->sin_addr, host, sizeof host) == NULL) return ks_str_from("", 0);
		n = snprintf(buf, sizeof buf, "%s:%u", host, (unsigned)ntohs(sin->sin_port));
	} else if (ss.ss_family == AF_INET6) {
		struct sockaddr_in6 *sin6 = (struct sockaddr_in6 *)&ss;
		if (inet_ntop(AF_INET6, &sin6->sin6_addr, host, sizeof host) == NULL) return ks_str_from("", 0);
		n = snprintf(buf, sizeof buf, "[%s]:%u", host, (unsigned)ntohs(sin6->sin6_port));
	} else {
		return ks_str_from("", 0);
	}
	return ks_str_from(buf, n);
}

KS_RT ks_obj *ks_getenv(ks_obj *name) {
	ks_str *s = KS_S(name);
	if (s->len == 0 || memchr(s->data, 0, (size_t)s->len) != NULL) return ks_str_from("", 0);
	const char *v = getenv(s->data);
	return ks_str_cstr(v != NULL ? v : "");
}

KS_RT bool ks_has_env(ks_obj *name) {
	ks_str *s = KS_S(name);
	if (s->len == 0 || memchr(s->data, 0, (size_t)s->len) != NULL) return false;
	return getenv(s->data) != NULL;
}

KS_RT int64_t ks_clock_read(clockid_t id) {
	struct timespec ts;
	if (clock_gettime(id, &ts) != 0) return 0;
	return (int64_t)ts.tv_sec * 1000 + (int64_t)(ts.tv_nsec / 1000000);
}

KS_RT int64_t ks_time_ms(void) {
	return ks_clock_read(CLOCK_REALTIME);
}

KS_RT int64_t ks_clock_ms(void) {
	return ks_clock_read(CLOCK_MONOTONIC);
}

KS_RT void ks_sleep_ms(int64_t ms) {
	if (ms <= 0) return;
	struct timespec ts, rem;
	ts.tv_sec = (time_t)(ms / 1000);
	ts.tv_nsec = (long)((ms % 1000) * 1000000);
	fflush(stdout);
	while (nanosleep(&ts, &rem) != 0 && errno == EINTR && !ks_shutdown_flag) ts = rem;
}

KS_RT int64_t ks_cpu_count(void) {
	long n = sysconf(_SC_NPROCESSORS_ONLN);
	return n > 0 ? (int64_t)n : 1;
}

KS_RT int64_t ks_pid(void) {
	return (int64_t)getpid();
}

static void ks_on_shutdown(int sig) {
	(void)sig;
	ks_shutdown_flag = 1;
}

KS_RT void ks_trap_shutdown(void) {
	ks_shutdown_trapped = true;
	struct sigaction sa;
	memset(&sa, 0, sizeof sa);
	sa.sa_handler = ks_on_shutdown;
	sigemptyset(&sa.sa_mask);
	sa.sa_flags = 0;
	sigaction(SIGTERM, &sa, NULL);
	sigaction(SIGINT, &sa, NULL);
}

KS_RT bool ks_shutdown_requested(void) {
	return ks_shutdown_flag != 0;
}

static volatile sig_atomic_t ks_sup_stop;

static void ks_sup_on_signal(int sig) {
	(void)sig;
	ks_sup_stop = 1;
}

KS_RT void ks_sup_signals(void (*handler)(int)) {
	struct sigaction sa;
	memset(&sa, 0, sizeof sa);
	sa.sa_handler = handler;
	sigemptyset(&sa.sa_mask);
	sigaction(SIGTERM, &sa, NULL);
	sigaction(SIGINT, &sa, NULL);
}

// ks_fork_workers runs n worker processes and supervises them. Each worker
// returns from fork_workers with its index; the supervisor never returns.
// A worker that crashes or exits non-zero is replaced (after a pause if it
// died within a second of starting). SIGTERM or SIGINT to the supervisor
// is forwarded to every worker; workers get 30 seconds to exit before
// SIGKILL, then the supervisor exits 0.
KS_RT int64_t ks_fork_workers(int64_t n) {
	if (n < 1 || n > 1024) ks_panicf("fork_workers: worker count %" PRId64 " must be 1 to 1024", n);
	fflush(stdout);
	fflush(stderr);
	pid_t *pids = ks_alloc((size_t)n * sizeof(pid_t));
	int64_t *started = ks_alloc((size_t)n * sizeof(int64_t));
	ks_sup_signals(ks_sup_on_signal);
	int64_t live = 0;
	for (int64_t i = 0; i <= n; i++) {
		if (i == n) break;
		pid_t p = fork();
		if (p < 0) ks_panicf("fork_workers: fork failed: %s", strerror(errno));
		if (p == 0) {
			free(pids);
			free(started);
			if (ks_shutdown_trapped) ks_trap_shutdown();
			else ks_sup_signals(SIG_DFL);
			return i;
		}
		pids[i] = p;
		started[i] = ks_clock_ms();
		live++;
	}
	int64_t stop_at = -1;
	while (live > 0) {
		if (ks_sup_stop && stop_at < 0) {
			stop_at = ks_clock_ms();
			for (int64_t i = 0; i < n; i++) if (pids[i] > 0) kill(pids[i], SIGTERM);
		}
		if (stop_at >= 0 && ks_clock_ms() - stop_at > 30000) {
			for (int64_t i = 0; i < n; i++) if (pids[i] > 0) kill(pids[i], SIGKILL);
		}
		int status = 0;
		pid_t p = waitpid(-1, &status, WNOHANG);
		if (p < 0 && errno == ECHILD) break;
		if (p <= 0) {
			struct timespec ts = {0, 50 * 1000000L};
			nanosleep(&ts, NULL);
			continue;
		}
		int64_t i = 0;
		while (i < n && pids[i] != p) i++;
		if (i == n) continue;
		pids[i] = 0;
		live--;
		bool clean = WIFEXITED(status) && WEXITSTATUS(status) == 0;
		if (stop_at >= 0 || clean) continue;
		if (WIFSIGNALED(status)) {
			fprintf(stderr, "kestrel: worker %" PRId64 " (pid %d) killed by signal %d; restarting\n", i, (int)p, WTERMSIG(status));
		} else {
			fprintf(stderr, "kestrel: worker %" PRId64 " (pid %d) exited with status %d; restarting\n", i, (int)p, WEXITSTATUS(status));
		}
		fflush(stderr);
		if (ks_clock_ms() - started[i] < 1000) {
			struct timespec ts = {1, 0};
			nanosleep(&ts, NULL);
			if (ks_sup_stop) continue;
		}
		pid_t c = fork();
		if (c < 0) {
			fprintf(stderr, "kestrel: fork_workers: fork failed: %s\n", strerror(errno));
			continue;
		}
		if (c == 0) {
			free(pids);
			free(started);
			if (ks_shutdown_trapped) ks_trap_shutdown();
			else ks_sup_signals(SIG_DFL);
			return i;
		}
		pids[i] = c;
		started[i] = ks_clock_ms();
		live++;
	}
	fflush(stdout);
	fflush(stderr);
	_Exit(0);
}

// ks_os_random fills buf from the kernel's CSPRNG and stops the program if
// it cannot: a program that asked for secure random bytes must never get
// predictable ones.
KS_RT void ks_os_random(unsigned char *buf, size_t n) {
	static int fd = -1;
	if (fd < 0) {
		fd = open("/dev/urandom", O_RDONLY | O_CLOEXEC);
		if (fd < 0) ks_panicf("random: cannot open /dev/urandom: %s", strerror(errno));
	}
	size_t off = 0;
	while (off < n) {
		ssize_t r = read(fd, buf + off, n - off);
		if (r < 0 && errno == EINTR) continue;
		if (r <= 0) ks_panic("random: cannot read /dev/urandom");
		off += (size_t)r;
	}
}

KS_RT ks_obj *ks_random_bytes(int64_t n) {
	if (n < 0 || n > (1 << 30)) ks_panicf("random_bytes: count %" PRId64 " must be 0 to 1073741824", n);
	char *d;
	ks_obj *r = ks_str_alloc(n, &d);
	ks_os_random((unsigned char *)d, (size_t)n);
	return r;
}

KS_RT int64_t ks_random_int(int64_t lo, int64_t hi) {
	if (lo > hi) ks_panicf("random_int: low %" PRId64 " is greater than high %" PRId64, lo, hi);
	uint64_t span = (uint64_t)hi - (uint64_t)lo;
	uint64_t x;
	if (span == UINT64_MAX) {
		ks_os_random((unsigned char *)&x, sizeof x);
		return (int64_t)x;
	}
	uint64_t range = span + 1;
	uint64_t limit = UINT64_MAX - (UINT64_MAX % range);
	do {
		ks_os_random((unsigned char *)&x, sizeof x);
	} while (x >= limit);
	return (int64_t)((uint64_t)lo + x % range);
}

typedef struct {
	uint32_t h[8];
	uint64_t len;
	unsigned char buf[64];
	size_t fill;
} ks_sha256;

static const uint32_t ks_sha256_k[64] = {
	0x428a2f98, 0x71374491, 0xb5c0fbcf, 0xe9b5dba5, 0x3956c25b, 0x59f111f1, 0x923f82a4, 0xab1c5ed5,
	0xd807aa98, 0x12835b01, 0x243185be, 0x550c7dc3, 0x72be5d74, 0x80deb1fe, 0x9bdc06a7, 0xc19bf174,
	0xe49b69c1, 0xefbe4786, 0x0fc19dc6, 0x240ca1cc, 0x2de92c6f, 0x4a7484aa, 0x5cb0a9dc, 0x76f988da,
	0x983e5152, 0xa831c66d, 0xb00327c8, 0xbf597fc7, 0xc6e00bf3, 0xd5a79147, 0x06ca6351, 0x14292967,
	0x27b70a85, 0x2e1b2138, 0x4d2c6dfc, 0x53380d13, 0x650a7354, 0x766a0abb, 0x81c2c92e, 0x92722c85,
	0xa2bfe8a1, 0xa81a664b, 0xc24b8b70, 0xc76c51a3, 0xd192e819, 0xd6990624, 0xf40e3585, 0x106aa070,
	0x19a4c116, 0x1e376c08, 0x2748774c, 0x34b0bcb5, 0x391c0cb3, 0x4ed8aa4a, 0x5b9cca4f, 0x682e6ff3,
	0x748f82ee, 0x78a5636f, 0x84c87814, 0x8cc70208, 0x90befffa, 0xa4506ceb, 0xbef9a3f7, 0xc67178f2,
};

#define KS_ROR32(x, n) (((x) >> (n)) | ((x) << (32 - (n))))

KS_RT void ks_sha256_block(ks_sha256 *c, const unsigned char *p) {
	uint32_t w[64];
	for (int i = 0; i < 16; i++) {
		w[i] = (uint32_t)p[4 * i] << 24 | (uint32_t)p[4 * i + 1] << 16 | (uint32_t)p[4 * i + 2] << 8 | (uint32_t)p[4 * i + 3];
	}
	for (int i = 16; i < 64; i++) {
		uint32_t s0 = KS_ROR32(w[i - 15], 7) ^ KS_ROR32(w[i - 15], 18) ^ (w[i - 15] >> 3);
		uint32_t s1 = KS_ROR32(w[i - 2], 17) ^ KS_ROR32(w[i - 2], 19) ^ (w[i - 2] >> 10);
		w[i] = w[i - 16] + s0 + w[i - 7] + s1;
	}
	uint32_t a = c->h[0], b = c->h[1], cc = c->h[2], d = c->h[3], e = c->h[4], f = c->h[5], g = c->h[6], h = c->h[7];
	for (int i = 0; i < 64; i++) {
		uint32_t S1 = KS_ROR32(e, 6) ^ KS_ROR32(e, 11) ^ KS_ROR32(e, 25);
		uint32_t ch = (e & f) ^ (~e & g);
		uint32_t t1 = h + S1 + ch + ks_sha256_k[i] + w[i];
		uint32_t S0 = KS_ROR32(a, 2) ^ KS_ROR32(a, 13) ^ KS_ROR32(a, 22);
		uint32_t maj = (a & b) ^ (a & cc) ^ (b & cc);
		uint32_t t2 = S0 + maj;
		h = g; g = f; f = e; e = d + t1; d = cc; cc = b; b = a; a = t1 + t2;
	}
	c->h[0] += a; c->h[1] += b; c->h[2] += cc; c->h[3] += d;
	c->h[4] += e; c->h[5] += f; c->h[6] += g; c->h[7] += h;
}

KS_RT void ks_sha256_init(ks_sha256 *c) {
	static const uint32_t iv[8] = {0x6a09e667, 0xbb67ae85, 0x3c6ef372, 0xa54ff53a, 0x510e527f, 0x9b05688c, 0x1f83d9ab, 0x5be0cd19};
	memcpy(c->h, iv, sizeof iv);
	c->len = 0;
	c->fill = 0;
}

KS_RT void ks_sha256_update(ks_sha256 *c, const unsigned char *p, size_t n) {
	c->len += (uint64_t)n;
	while (n > 0) {
		size_t take = 64 - c->fill;
		if (take > n) take = n;
		memcpy(c->buf + c->fill, p, take);
		c->fill += take;
		p += take;
		n -= take;
		if (c->fill == 64) {
			ks_sha256_block(c, c->buf);
			c->fill = 0;
		}
	}
}

KS_RT void ks_sha256_final(ks_sha256 *c, unsigned char out[32]) {
	uint64_t bits = c->len * 8;
	unsigned char pad = 0x80;
	ks_sha256_update(c, &pad, 1);
	unsigned char zero = 0;
	while (c->fill != 56) ks_sha256_update(c, &zero, 1);
	unsigned char lenb[8];
	for (int i = 0; i < 8; i++) lenb[i] = (unsigned char)(bits >> (56 - 8 * i));
	ks_sha256_update(c, lenb, 8);
	for (int i = 0; i < 8; i++) {
		out[4 * i] = (unsigned char)(c->h[i] >> 24);
		out[4 * i + 1] = (unsigned char)(c->h[i] >> 16);
		out[4 * i + 2] = (unsigned char)(c->h[i] >> 8);
		out[4 * i + 3] = (unsigned char)c->h[i];
	}
}

KS_RT ks_obj *ks_sha256_str(ks_obj *so) {
	ks_str *s = KS_S(so);
	ks_sha256 c;
	unsigned char out[32];
	ks_sha256_init(&c);
	ks_sha256_update(&c, (const unsigned char *)s->data, (size_t)s->len);
	ks_sha256_final(&c, out);
	return ks_str_from((const char *)out, 32);
}

KS_RT void ks_hmac_raw(const unsigned char *key, size_t klen, const unsigned char *msg, size_t mlen, unsigned char out[32]) {
	unsigned char k[64];
	memset(k, 0, sizeof k);
	if (klen > 64) {
		ks_sha256 c;
		ks_sha256_init(&c);
		ks_sha256_update(&c, key, klen);
		ks_sha256_final(&c, k);
	} else if (klen > 0) {
		memcpy(k, key, klen);
	}
	unsigned char ipad[64], opad[64], inner[32];
	for (int i = 0; i < 64; i++) {
		ipad[i] = k[i] ^ 0x36;
		opad[i] = k[i] ^ 0x5c;
	}
	ks_sha256 c;
	ks_sha256_init(&c);
	ks_sha256_update(&c, ipad, 64);
	ks_sha256_update(&c, msg, mlen);
	ks_sha256_final(&c, inner);
	ks_sha256_init(&c);
	ks_sha256_update(&c, opad, 64);
	ks_sha256_update(&c, inner, 32);
	ks_sha256_final(&c, out);
}

KS_RT ks_obj *ks_hmac_sha256(ks_obj *ko, ks_obj *mo) {
	ks_str *k = KS_S(ko), *m = KS_S(mo);
	unsigned char out[32];
	ks_hmac_raw((const unsigned char *)k->data, (size_t)k->len, (const unsigned char *)m->data, (size_t)m->len, out);
	return ks_str_from((const char *)out, 32);
}

KS_RT ks_obj *ks_pbkdf2_sha256(ks_obj *po, ks_obj *so, int64_t iters, int64_t len) {
	if (iters < 1 || iters > 100000000) ks_panicf("pbkdf2_sha256: iterations %" PRId64 " must be 1 to 100000000", iters);
	if (len < 1 || len > 1024) ks_panicf("pbkdf2_sha256: length %" PRId64 " must be 1 to 1024", len);
	ks_str *p = KS_S(po), *s = KS_S(so);
	if (s->len > (1 << 20)) ks_panic("pbkdf2_sha256: salt too long");
	char *out;
	ks_obj *r = ks_str_alloc(len, &out);
	unsigned char *msg = ks_alloc((size_t)s->len + 4);
	if (s->len > 0) memcpy(msg, s->data, (size_t)s->len);
	int64_t done = 0;
	for (uint32_t block = 1; done < len; block++) {
		msg[s->len] = (unsigned char)(block >> 24);
		msg[s->len + 1] = (unsigned char)(block >> 16);
		msg[s->len + 2] = (unsigned char)(block >> 8);
		msg[s->len + 3] = (unsigned char)block;
		unsigned char u[32], t[32];
		ks_hmac_raw((const unsigned char *)p->data, (size_t)p->len, msg, (size_t)s->len + 4, u);
		memcpy(t, u, 32);
		for (int64_t i = 1; i < iters; i++) {
			ks_hmac_raw((const unsigned char *)p->data, (size_t)p->len, u, 32, u);
			for (int j = 0; j < 32; j++) t[j] ^= u[j];
		}
		int64_t take = len - done < 32 ? len - done : 32;
		memcpy(out + done, t, (size_t)take);
		done += take;
	}
	free(msg);
	return r;
}

KS_RT bool ks_constant_time_eq(ks_obj *ao, ks_obj *bo) {
	ks_str *a = KS_S(ao), *b = KS_S(bo);
	if (a->len != b->len) return false;
	volatile unsigned char diff = 0;
	for (int64_t i = 0; i < a->len; i++) diff |= (unsigned char)(a->data[i] ^ b->data[i]);
	return diff == 0;
}

KS_RT ks_obj *ks_hex(ks_obj *so) {
	static const char digits[] = "0123456789abcdef";
	ks_str *s = KS_S(so);
	if (s->len > INT64_MAX / 2) ks_panic("string too long");
	char *d;
	ks_obj *r = ks_str_alloc(s->len * 2, &d);
	for (int64_t i = 0; i < s->len; i++) {
		unsigned char c = (unsigned char)s->data[i];
		d[2 * i] = digits[c >> 4];
		d[2 * i + 1] = digits[c & 15];
	}
	return r;
}

KS_RT int ks_hexval(char c) {
	if (c >= '0' && c <= '9') return c - '0';
	if (c >= 'a' && c <= 'f') return c - 'a' + 10;
	if (c >= 'A' && c <= 'F') return c - 'A' + 10;
	return -1;
}

KS_RT ks_obj *ks_res_ok_obj(ks_obj *v);
__attribute__((unused, format(printf, 1, 2))) static ks_obj *ks_res_errf(const char *fmt, ...);

KS_RT ks_obj *ks_unhex(ks_obj *so) {
	ks_str *s = KS_S(so);
	if (s->len % 2 != 0) return ks_res_errf("unhex: odd length %" PRId64, s->len);
	char *d;
	ks_obj *r = ks_str_alloc(s->len / 2, &d);
	for (int64_t i = 0; i < s->len; i += 2) {
		int hi = ks_hexval(s->data[i]), lo = ks_hexval(s->data[i + 1]);
		if (hi < 0 || lo < 0) {
			ks_release(r);
			return ks_res_errf("unhex: invalid hex digit at byte %" PRId64, hi < 0 ? i : i + 1);
		}
		d[i / 2] = (char)(hi << 4 | lo);
	}
	return ks_res_ok_obj(r);
}

KS_RT ks_obj *ks_base64_enc(ks_obj *so, bool url) {
	const char *alpha = url ? "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_"
	                        : "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/";
	ks_str *s = KS_S(so);
	if (s->len > INT64_MAX / 4 * 3 - 3) ks_panic("string too long");
	int64_t full = (s->len + 2) / 3 * 4;
	int64_t outlen = url ? (s->len / 3 * 4 + (s->len % 3 == 0 ? 0 : s->len % 3 + 1)) : full;
	char *d;
	ks_obj *r = ks_str_alloc(outlen, &d);
	const unsigned char *p = (const unsigned char *)s->data;
	int64_t w = 0;
	for (int64_t i = 0; i < s->len; i += 3) {
		uint32_t v = (uint32_t)p[i] << 16;
		int64_t rem = s->len - i;
		if (rem > 1) v |= (uint32_t)p[i + 1] << 8;
		if (rem > 2) v |= p[i + 2];
		d[w++] = alpha[(v >> 18) & 63];
		d[w++] = alpha[(v >> 12) & 63];
		if (rem > 1) d[w++] = alpha[(v >> 6) & 63];
		else if (!url) d[w++] = '=';
		if (rem > 2) d[w++] = alpha[v & 63];
		else if (!url) d[w++] = '=';
	}
	return r;
}

KS_RT ks_obj *ks_base64(ks_obj *s) { return ks_base64_enc(s, false); }
KS_RT ks_obj *ks_base64url(ks_obj *s) { return ks_base64_enc(s, true); }

KS_RT int ks_b64val(char c) {
	if (c >= 'A' && c <= 'Z') return c - 'A';
	if (c >= 'a' && c <= 'z') return c - 'a' + 26;
	if (c >= '0' && c <= '9') return c - '0' + 52;
	if (c == '+' || c == '-') return 62;
	if (c == '/' || c == '_') return 63;
	return -1;
}

// ks_unbase64 accepts the standard and URL-safe alphabets, with or without
// padding. Whitespace and any other byte are errors.
KS_RT ks_obj *ks_unbase64(ks_obj *so) {
	ks_str *s = KS_S(so);
	int64_t n = s->len;
	while (n > 0 && s->data[n - 1] == '=' && s->len - n < 2) n--;
	if (n % 4 == 1) return ks_res_errf("unbase64: invalid length");
	if (s->len != n && s->len % 4 != 0) return ks_res_errf("unbase64: invalid padding");
	char *d;
	ks_obj *r = ks_str_alloc(n / 4 * 3 + (n % 4 == 0 ? 0 : n % 4 - 1), &d);
	int64_t w = 0;
	uint32_t acc = 0;
	int bits = 0;
	for (int64_t i = 0; i < n; i++) {
		int v = ks_b64val(s->data[i]);
		if (v < 0) {
			ks_release(r);
			return ks_res_errf("unbase64: invalid character at byte %" PRId64, i);
		}
		acc = acc << 6 | (uint32_t)v;
		bits += 6;
		if (bits >= 8) {
			bits -= 8;
			d[w++] = (char)(acc >> bits);
			acc &= (1u << bits) - 1;
		}
	}
	if (acc != 0) {
		ks_release(r);
		return ks_res_errf("unbase64: non-zero trailing bits");
	}
	return ks_res_ok_obj(r);
}

KS_RT ks_obj *ks_url_encode(ks_obj *so) {
	static const char digits[] = "0123456789ABCDEF";
	ks_str *s = KS_S(so);
	if (s->len > INT64_MAX / 3) ks_panic("string too long");
	char *buf = ks_alloc((size_t)s->len * 3 + 1);
	int64_t w = 0;
	for (int64_t i = 0; i < s->len; i++) {
		unsigned char c = (unsigned char)s->data[i];
		if ((c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-' || c == '_' || c == '.' || c == '~') {
			buf[w++] = (char)c;
		} else {
			buf[w++] = '%';
			buf[w++] = digits[c >> 4];
			buf[w++] = digits[c & 15];
		}
	}
	ks_obj *r = ks_str_from(buf, w);
	free(buf);
	return r;
}

// ks_url_decode decodes %XX escapes and turns + into a space, as in query
// strings and form bodies.
KS_RT ks_obj *ks_url_decode(ks_obj *so) {
	ks_str *s = KS_S(so);
	char *buf = ks_alloc((size_t)s->len + 1);
	int64_t w = 0;
	for (int64_t i = 0; i < s->len; i++) {
		char c = s->data[i];
		if (c == '+') {
			buf[w++] = ' ';
		} else if (c == '%') {
			int hi = -1, lo = -1;
			if (i + 2 < s->len) {
				hi = ks_hexval(s->data[i + 1]);
				lo = ks_hexval(s->data[i + 2]);
			}
			if (hi < 0 || lo < 0) {
				free(buf);
				return ks_res_errf("url_decode: invalid escape at byte %" PRId64, i);
			}
			buf[w++] = (char)(hi << 4 | lo);
			i += 2;
		} else {
			buf[w++] = c;
		}
	}
	ks_obj *r = ks_str_from(buf, w);
	free(buf);
	return ks_res_ok_obj(r);
}

KS_RT ks_obj *ks_res_ok_i64(int64_t v);
KS_RT ks_obj *ks_res_ok_f64(double v);
KS_RT ks_obj *ks_res_ok_bool(bool v);
// JSON: documents stay strings. Each call validates the whole document and
// walks a dotted path ("user.tags.0") to one value. Object keys are matched
// exactly (the first of duplicate keys wins); array steps are decimal indexes.
typedef struct {
	const char *p;
	const char *start;
	const char *end;
	int depth;
} ks_jp;

KS_RT void ks_jws(ks_jp *j) {
	while (j->p < j->end && (*j->p == ' ' || *j->p == '\t' || *j->p == '\n' || *j->p == '\r')) j->p++;
}

// ks_jstring parses a string at j->p. If out is not NULL it receives the
// decoded bytes (out must have room for the raw length).
KS_RT bool ks_jstring(ks_jp *j, char *out, int64_t *outlen) {
	if (j->p >= j->end || *j->p != '"') return false;
	j->p++;
	int64_t w = 0;
	while (j->p < j->end) {
		unsigned char c = (unsigned char)*j->p++;
		if (c == '"') {
			if (outlen) *outlen = w;
			return true;
		}
		if (c < 0x20) return false;
		if (c != '\\') {
			if (out) out[w] = (char)c;
			w++;
			continue;
		}
		if (j->p >= j->end) return false;
		char e = *j->p++;
		char ch;
		switch (e) {
		case '"': ch = '"'; break;
		case '\\': ch = '\\'; break;
		case '/': ch = '/'; break;
		case 'b': ch = '\b'; break;
		case 'f': ch = '\f'; break;
		case 'n': ch = '\n'; break;
		case 'r': ch = '\r'; break;
		case 't': ch = '\t'; break;
		case 'u': {
			uint32_t cp = 0;
			for (int k = 0; k < 4; k++) {
				if (j->p >= j->end) return false;
				int v = ks_hexval(*j->p++);
				if (v < 0) return false;
				cp = cp << 4 | (uint32_t)v;
			}
			if (cp >= 0xd800 && cp <= 0xdbff && j->end - j->p >= 6 && j->p[0] == '\\' && j->p[1] == 'u') {
				uint32_t lo = 0;
				bool okhex = true;
				for (int k = 0; k < 4; k++) {
					int v = ks_hexval(j->p[2 + k]);
					if (v < 0) okhex = false;
					lo = lo << 4 | (uint32_t)(v < 0 ? 0 : v);
				}
				if (okhex && lo >= 0xdc00 && lo <= 0xdfff) {
					j->p += 6;
					cp = 0x10000 + ((cp - 0xd800) << 10) + (lo - 0xdc00);
				}
			}
			if (cp >= 0xd800 && cp <= 0xdfff) cp = 0xfffd;
			unsigned char buf[4];
			int n;
			if (cp < 0x80) { buf[0] = (unsigned char)cp; n = 1; }
			else if (cp < 0x800) { buf[0] = (unsigned char)(0xc0 | cp >> 6); buf[1] = (unsigned char)(0x80 | (cp & 0x3f)); n = 2; }
			else if (cp < 0x10000) { buf[0] = (unsigned char)(0xe0 | cp >> 12); buf[1] = (unsigned char)(0x80 | ((cp >> 6) & 0x3f)); buf[2] = (unsigned char)(0x80 | (cp & 0x3f)); n = 3; }
			else { buf[0] = (unsigned char)(0xf0 | cp >> 18); buf[1] = (unsigned char)(0x80 | ((cp >> 12) & 0x3f)); buf[2] = (unsigned char)(0x80 | ((cp >> 6) & 0x3f)); buf[3] = (unsigned char)(0x80 | (cp & 0x3f)); n = 4; }
			for (int k = 0; k < n; k++) {
				if (out) out[w] = (char)buf[k];
				w++;
			}
			continue;
		}
		default: return false;
		}
		if (out) out[w] = ch;
		w++;
	}
	return false;
}

KS_RT bool ks_jnumber(ks_jp *j, bool *integer) {
	const char *p = j->p;
	*integer = true;
	if (p < j->end && *p == '-') p++;
	if (p >= j->end) return false;
	if (*p == '0') p++;
	else if (*p >= '1' && *p <= '9') { while (p < j->end && ks_is_digit(*p)) p++; }
	else return false;
	if (p < j->end && *p == '.') {
		*integer = false;
		p++;
		if (p >= j->end || !ks_is_digit(*p)) return false;
		while (p < j->end && ks_is_digit(*p)) p++;
	}
	if (p < j->end && (*p == 'e' || *p == 'E')) {
		*integer = false;
		p++;
		if (p < j->end && (*p == '+' || *p == '-')) p++;
		if (p >= j->end || !ks_is_digit(*p)) return false;
		while (p < j->end && ks_is_digit(*p)) p++;
	}
	j->p = p;
	return true;
}

KS_RT bool ks_jlit(ks_jp *j, const char *lit) {
	size_t n = strlen(lit);
	if ((size_t)(j->end - j->p) < n || memcmp(j->p, lit, n) != 0) return false;
	j->p += n;
	return true;
}

KS_RT bool ks_jskip(ks_jp *j) {
	ks_jws(j);
	if (j->p >= j->end) return false;
	char c = *j->p;
	if (c == '"') return ks_jstring(j, NULL, NULL);
	if (c == 't') return ks_jlit(j, "true");
	if (c == 'f') return ks_jlit(j, "false");
	if (c == 'n') return ks_jlit(j, "null");
	if (c == '{' || c == '[') {
		if (++j->depth > 512) return false;
		char close = c == '{' ? '}' : ']';
		j->p++;
		ks_jws(j);
		if (j->p < j->end && *j->p == close) {
			j->p++;
			j->depth--;
			return true;
		}
		for (;;) {
			if (c == '{') {
				ks_jws(j);
				if (!ks_jstring(j, NULL, NULL)) return false;
				ks_jws(j);
				if (j->p >= j->end || *j->p != ':') return false;
				j->p++;
			}
			if (!ks_jskip(j)) return false;
			ks_jws(j);
			if (j->p >= j->end) return false;
			if (*j->p == ',') { j->p++; continue; }
			if (*j->p == close) { j->p++; j->depth--; return true; }
			return false;
		}
	}
	bool integer;
	return ks_jnumber(j, &integer);
}

KS_RT bool ks_jvalid_raw(ks_str *s, int64_t *errpos) {
	ks_jp j = {s->data, s->data, s->data + s->len, 0};
	bool ok = ks_jskip(&j);
	if (ok) {
		ks_jws(&j);
		ok = j.p == j.end;
	}
	if (!ok && errpos) *errpos = (int64_t)(j.p - j.start);
	return ok;
}

KS_RT bool ks_json_valid(ks_obj *doc) {
	return ks_jvalid_raw(KS_S(doc), NULL);
}

// ks_jfind validates doc and positions *out at the value for path. On
// failure it returns an error result; on success NULL.
KS_RT ks_obj *ks_jfind(ks_obj *doco, ks_obj *patho, ks_jp *out, bool single_key) {
	ks_str *doc = KS_S(doco), *path = KS_S(patho);
	int64_t bad = 0;
	if (!ks_jvalid_raw(doc, &bad)) return ks_res_errf("json: invalid document at byte %" PRId64, bad);
	ks_jp j = {doc->data, doc->data, doc->data + doc->len, 0};
	ks_jws(&j);
	int64_t i = 0;
	while ((single_key || path->len > 0) && i <= path->len) {
		int64_t segend = i;
		if (single_key) segend = path->len;
		else while (segend < path->len && path->data[segend] != '.') segend++;
		const char *seg = path->data + i;
		int64_t seglen = segend - i;
		ks_jws(&j);
		if (*j.p == '{') {
			j.p++;
			bool found = false;
			ks_jws(&j);
			while (*j.p != '}') {
				ks_jws(&j);
				const char *ks = j.p;
				int64_t klen = 0;
				char *key = ks_alloc((size_t)(j.end - ks) + 1);
				ks_jstring(&j, key, &klen);
				bool match = klen == seglen && (seglen == 0 || memcmp(key, seg, (size_t)seglen) == 0);
				free(key);
				ks_jws(&j);
				j.p++;
				ks_jws(&j);
				if (match) {
					found = true;
					break;
				}
				ks_jskip(&j);
				ks_jws(&j);
				if (*j.p == ',') j.p++;
				ks_jws(&j);
			}
			if (!found) return ks_res_errf("json: no key \"%.*s\" at \"%.*s\"", (int)(seglen > 100 ? 100 : seglen), seg, (int)(i > 0 ? i - 1 : 0), path->data);
		} else if (*j.p == '[') {
			int64_t idx = 0;
			if (seglen == 0 || seglen > 18) return ks_res_errf("json: \"%.*s\" is not an array index", (int)(seglen > 100 ? 100 : seglen), seg);
			for (int64_t k = 0; k < seglen; k++) {
				if (!ks_is_digit(seg[k])) return ks_res_errf("json: \"%.*s\" is not an array index", (int)(seglen > 100 ? 100 : seglen), seg);
				idx = idx * 10 + (seg[k] - '0');
			}
			j.p++;
			ks_jws(&j);
			int64_t at = 0;
			bool found = false;
			while (*j.p != ']') {
				if (at == idx) {
					found = true;
					break;
				}
				ks_jskip(&j);
				ks_jws(&j);
				if (*j.p == ',') j.p++;
				ks_jws(&j);
				at++;
			}
			if (!found) return ks_res_errf("json: index %" PRId64 " out of range (length %" PRId64 ")", idx, at);
		} else {
			return ks_res_errf("json: cannot look up \"%.*s\" in a value that is not an object or array", (int)(seglen > 100 ? 100 : seglen), seg);
		}
		if (single_key) break;
		i = segend + 1;
	}
	ks_jws(&j);
	*out = j;
	return NULL;
}

KS_RT const char *ks_jkind(char c) {
	switch (c) {
	case '{': return "object";
	case '[': return "array";
	case '"': return "string";
	case 't': case 'f': return "bool";
	case 'n': return "null";
	}
	return "number";
}

KS_RT ks_obj *ks_json_type(ks_obj *doc, ks_obj *path) {
	ks_jp j;
	ks_obj *e = ks_jfind(doc, path, &j, false);
	if (e != NULL) {
		ks_release(e);
		return ks_str_from("", 0);
	}
	return ks_str_cstr(ks_jkind(*j.p));
}

KS_RT ks_obj *ks_jwrong(ks_obj *path, ks_jp *j, const char *want) {
	return ks_res_errf("json: \"%.100s\" is %s, not %s", KS_S(path)->data, ks_jkind(*j->p), want);
}

KS_RT ks_obj *ks_json_string(ks_obj *doc, ks_obj *path) {
	ks_jp j;
	ks_obj *e = ks_jfind(doc, path, &j, false);
	if (e != NULL) return e;
	if (*j.p != '"') return ks_jwrong(path, &j, "a string");
	char *buf = ks_alloc((size_t)(j.end - j.p) + 1);
	int64_t n = 0;
	ks_jstring(&j, buf, &n);
	ks_obj *r = ks_str_from(buf, n);
	free(buf);
	return ks_res_ok_obj(r);
}

KS_RT ks_obj *ks_json_int(ks_obj *doc, ks_obj *path) {
	ks_jp j;
	ks_obj *e = ks_jfind(doc, path, &j, false);
	if (e != NULL) return e;
	const char *start = j.p;
	bool integer = false;
	if (strcmp(ks_jkind(*j.p), "number") != 0 || !ks_jnumber(&j, &integer)) return ks_jwrong(path, &j, "an integer");
	if (!integer) return ks_res_errf("json: \"%.100s\" is not an integer", KS_S(path)->data);
	ks_obj *num = ks_str_from(start, (int64_t)(j.p - start));
	int64_t v;
	bool ok = ks_parse_int_raw(num, &v);
	ks_release(num);
	if (!ok) return ks_res_errf("json: \"%.100s\" does not fit in an int", KS_S(path)->data);
	return ks_res_ok_i64(v);
}

KS_RT ks_obj *ks_json_float(ks_obj *doc, ks_obj *path) {
	ks_jp j;
	ks_obj *e = ks_jfind(doc, path, &j, false);
	if (e != NULL) return e;
	const char *start = j.p;
	bool integer;
	if (strcmp(ks_jkind(*j.p), "number") != 0 || !ks_jnumber(&j, &integer)) return ks_jwrong(path, &j, "a number");
	ks_obj *num = ks_str_from(start, (int64_t)(j.p - start));
	double v = strtod(KS_S(num)->data, NULL);
	ks_release(num);
	return ks_res_ok_f64(v);
}

KS_RT ks_obj *ks_json_bool(ks_obj *doc, ks_obj *path) {
	ks_jp j;
	ks_obj *e = ks_jfind(doc, path, &j, false);
	if (e != NULL) return e;
	if (*j.p == 't') return ks_res_ok_bool(true);
	if (*j.p == 'f') return ks_res_ok_bool(false);
	return ks_jwrong(path, &j, "a bool");
}

KS_RT ks_obj *ks_jraw_at(ks_jp *j) {
	const char *start = j->p;
	ks_jskip(j);
	return ks_res_ok_obj(ks_str_from(start, (int64_t)(j->p - start)));
}

KS_RT ks_obj *ks_json_raw(ks_obj *doc, ks_obj *path) {
	ks_jp j;
	ks_obj *e = ks_jfind(doc, path, &j, false);
	if (e != NULL) return e;
	return ks_jraw_at(&j);
}

KS_RT ks_obj *ks_json_member(ks_obj *doc, ks_obj *key) {
	ks_jp j;
	ks_obj *e = ks_jfind(doc, key, &j, true);
	if (e != NULL) return e;
	return ks_jraw_at(&j);
}

// ks_jitems walks the array or object at j and returns its length, pushing
// decoded keys onto keys when it is not NULL.
KS_RT int64_t ks_jitems(ks_jp *j, ks_obj *keys) {
	char open = *j->p;
	char close = open == '{' ? '}' : ']';
	j->p++;
	ks_jws(j);
	int64_t n = 0;
	while (*j->p != close) {
		if (open == '{') {
			char *buf = ks_alloc((size_t)(j->end - j->p) + 1);
			int64_t klen = 0;
			ks_jstring(j, buf, &klen);
			if (keys) ks_arr_obj_push(keys, ks_str_from(buf, klen));
			free(buf);
			ks_jws(j);
			j->p++;
		}
		ks_jskip(j);
		ks_jws(j);
		if (*j->p == ',') j->p++;
		ks_jws(j);
		n++;
	}
	return n;
}

KS_RT ks_obj *ks_json_len(ks_obj *doc, ks_obj *path) {
	ks_jp j;
	ks_obj *e = ks_jfind(doc, path, &j, false);
	if (e != NULL) return e;
	if (*j.p != '{' && *j.p != '[') return ks_jwrong(path, &j, "an array or object");
	return ks_res_ok_i64(ks_jitems(&j, NULL));
}

KS_RT ks_obj *ks_json_keys(ks_obj *doc, ks_obj *path) {
	ks_jp j;
	ks_obj *e = ks_jfind(doc, path, &j, false);
	if (e != NULL) return e;
	if (*j.p != '{') return ks_jwrong(path, &j, "an object");
	ks_obj *keys = ks_arr_obj_new(0);
	ks_jitems(&j, keys);
	return ks_res_ok_obj(keys);
}

KS_RT ks_obj *ks_json_quote(ks_obj *so) {
	static const char digits[] = "0123456789abcdef";
	ks_str *s = KS_S(so);
	if (s->len > (INT64_MAX - 2) / 6) ks_panic("string too long");
	char *buf = ks_alloc((size_t)s->len * 6 + 3);
	int64_t w = 0;
	buf[w++] = '"';
	for (int64_t i = 0; i < s->len; i++) {
		unsigned char c = (unsigned char)s->data[i];
		switch (c) {
		case '"': buf[w++] = '\\'; buf[w++] = '"'; break;
		case '\\': buf[w++] = '\\'; buf[w++] = '\\'; break;
		case '\n': buf[w++] = '\\'; buf[w++] = 'n'; break;
		case '\r': buf[w++] = '\\'; buf[w++] = 'r'; break;
		case '\t': buf[w++] = '\\'; buf[w++] = 't'; break;
		case '\b': buf[w++] = '\\'; buf[w++] = 'b'; break;
		case '\f': buf[w++] = '\\'; buf[w++] = 'f'; break;
		default:
			if (c < 0x20 || c == 0x7f) {
				buf[w++] = '\\'; buf[w++] = 'u'; buf[w++] = '0'; buf[w++] = '0';
				buf[w++] = digits[c >> 4]; buf[w++] = digits[c & 15];
			} else {
				buf[w++] = (char)c;
			}
		}
	}
	buf[w++] = '"';
	ks_obj *r = ks_str_from(buf, w);
	free(buf);
	return r;
}

typedef struct {
	ks_obj h;
	bool ok;
	bool managed;
	union {
		int64_t i;
		double f;
		bool b;
		ks_obj *o;
	} v;
	ks_obj *msg;
} ks_res;

KS_RT void ks_res_drop(ks_obj *o) {
	ks_res *r = (ks_res *)o;
	if (r->managed) ks_release(r->v.o);
	if (r->msg != NULL) ks_release(r->msg);
	free(o);
}

KS_RT ks_obj *ks_res_clone(ks_obj *o);

KS_RT ks_res *ks_res_new(bool ok) {
	ks_res *r = ks_alloc(sizeof *r);
	ks_init_obj(&r->h, ks_res_drop, ks_res_clone);
	r->ok = ok;
	r->managed = false;
	r->v.o = NULL;
	r->msg = NULL;
	return r;
}

KS_RT ks_obj *ks_res_clone(ks_obj *o) {
	ks_res *a = (ks_res *)o;
	ks_res *c = ks_res_new(a->ok);
	c->managed = a->managed;
	c->v = a->v;
	if (c->managed) ks_retain(c->v.o);
	if (a->msg != NULL) c->msg = ks_retain(a->msg);
	return &c->h;
}

KS_RT ks_obj *ks_res_ok_i64(int64_t v) { ks_res *r = ks_res_new(true); r->v.i = v; return &r->h; }
KS_RT ks_obj *ks_res_ok_f64(double v) { ks_res *r = ks_res_new(true); r->v.f = v; return &r->h; }
KS_RT ks_obj *ks_res_ok_bool(bool v) { ks_res *r = ks_res_new(true); r->v.b = v; return &r->h; }
KS_RT ks_obj *ks_res_ok_void(void) { return &ks_res_new(true)->h; }

// ks_res_ok_obj takes ownership of v.
KS_RT ks_obj *ks_res_ok_obj(ks_obj *v) {
	ks_res *r = ks_res_new(true);
	r->managed = true;
	r->v.o = v;
	return &r->h;
}

KS_RT ks_obj *ks_res_err(ks_obj *msg) {
	ks_res *r = ks_res_new(false);
	r->msg = ks_retain(msg);
	return &r->h;
}

__attribute__((unused, format(printf, 1, 2))) static ks_obj *ks_res_errf(const char *fmt, ...) {
	char buf[512];
	va_list ap;
	va_start(ap, fmt);
	vsnprintf(buf, sizeof buf, fmt, ap);
	va_end(ap);
	ks_res *r = ks_res_new(false);
	r->msg = ks_str_cstr(buf);
	return &r->h;
}

KS_RT ks_obj *ks_res_error(ks_obj *o) {
	ks_res *r = (ks_res *)o;
	if (r->ok) return ks_str_from("", 0);
	return ks_retain(r->msg);
}

KS_RT void ks_res_check(ks_obj *o) {
	ks_res *r = (ks_res *)o;
	if (!r->ok) ks_panicf("unwrap of an error: %.400s", KS_S(r->msg)->data);
}

KS_RT int ks_main_result(ks_obj *o) {
	ks_res *r = (ks_res *)o;
	int code = 0;
	if (!r->ok) {
		fputs("kestrel: error: ", stderr);
		ks_print_str(r->msg, stderr);
		fputc('\n', stderr);
		fflush(stderr);
		code = 1;
	}
	ks_release(o);
	return code;
}

KS_RT ks_obj *ks_try_read_file(ks_obj *path) {
	ks_str *s = KS_S(path);
	if (s->len == 0) return ks_res_errf("read_file: empty path");
	if (memchr(s->data, 0, (size_t)s->len) != NULL) return ks_res_errf("read_file: path contains a NUL byte");
	FILE *f = fopen(s->data, "rb");
	if (f == NULL) return ks_res_errf("cannot open \"%.200s\": %s", s->data, strerror(errno));
	ks_obj *data = ks_slurp(f);
	int e = errno;
	fclose(f);
	if (data == NULL) return ks_res_errf("error reading \"%.200s\": %s", s->data, strerror(e));
	return ks_res_ok_obj(data);
}

KS_RT ks_obj *ks_try_write_file(ks_obj *path, ks_obj *content) {
	ks_str *s = KS_S(path);
	if (s->len == 0) return ks_res_errf("write_file: empty path");
	if (memchr(s->data, 0, (size_t)s->len) != NULL) return ks_res_errf("write_file: path contains a NUL byte");
	FILE *f = fopen(s->data, "wb");
	if (f == NULL) return ks_res_errf("cannot create \"%.200s\": %s", s->data, strerror(errno));
	ks_str *c = KS_S(content);
	bool ok = c->len == 0 || fwrite(c->data, 1, (size_t)c->len, f) == (size_t)c->len;
	int e = errno;
	if (fclose(f) != 0) {
		ok = false;
		e = errno;
	}
	if (!ok) return ks_res_errf("error writing \"%.200s\": %s", s->data, strerror(e));
	return ks_res_ok_void();
}

KS_RT ks_obj *ks_try_parse_int(ks_obj *s) {
	int64_t v;
	if (!ks_parse_int_raw(s, &v)) return ks_res_errf("invalid integer \"%.100s\"", KS_S(s)->data);
	return ks_res_ok_i64(v);
}

KS_RT ks_obj *ks_try_parse_float(ks_obj *s) {
	if (!ks_is_float(s)) return ks_res_errf("invalid number \"%.100s\"", KS_S(s)->data);
	return ks_res_ok_f64(strtod(KS_S(s)->data, NULL));
}
`

func runtimeC() string {
	var sb strings.Builder
	sb.WriteString(runtimeHead)
	for _, k := range []struct{ kind, elem, retain, release string }{
		{"i64", "int64_t", "", ""},
		{"bool", "bool", "", ""},
		{"f64", "double", "", ""},
		{"obj", "ks_obj *", "ks_retain", "ks_release"},
	} {
		src := strings.ReplaceAll(arrayTemplate, "ks_arr_KIND", "ks_arr_"+k.kind)
		src = strings.ReplaceAll(src, "ELEM", k.elem)
		src = strings.ReplaceAll(src, "RETAIN", k.retain)
		if k.release == "" {
			src = strings.ReplaceAll(src, "\tfor (int64_t i = 0; i < a->len; i++) RELEASE(a->data[i]);\n", "")
			src = strings.ReplaceAll(src, "\tif (n == 0) RELEASE(v);\n", "")
		} else {
			src = strings.ReplaceAll(src, "RELEASE", k.release)
		}
		sb.WriteString(src)
	}
	sb.WriteString(runtimeTail)
	sb.WriteString(mapHelpers)
	for _, inst := range mapInsts {
		sb.WriteString(emitMap(inst))
	}
	return sb.String()
}

type mapInst struct {
	name, keyC, valC      string
	keyHash, keyEq        string
	keyRetain, keyRelease string
	valRetain, valRelease string
	keyArr, valArr        string
}

var mapInsts = []mapInst{
	{name: "i64_i64", keyC: "int64_t", valC: "int64_t", keyHash: "ks_hash_i64(key)", keyEq: "(a)==(b)", keyRetain: "(key)", keyRelease: "(void)(key)", valRetain: "(v)", valRelease: "(void)(v)", keyArr: "i64", valArr: "i64"},
	{name: "i64_bool", keyC: "int64_t", valC: "bool", keyHash: "ks_hash_i64(key)", keyEq: "(a)==(b)", keyRetain: "(key)", keyRelease: "(void)(key)", valRetain: "(v)", valRelease: "(void)(v)", keyArr: "i64", valArr: "bool"},
	{name: "i64_f64", keyC: "int64_t", valC: "double", keyHash: "ks_hash_i64(key)", keyEq: "(a)==(b)", keyRetain: "(key)", keyRelease: "(void)(key)", valRetain: "(v)", valRelease: "(void)(v)", keyArr: "i64", valArr: "f64"},
	{name: "i64_obj", keyC: "int64_t", valC: "ks_obj *", keyHash: "ks_hash_i64(key)", keyEq: "(a)==(b)", keyRetain: "(key)", keyRelease: "(void)(key)", valRetain: "ks_retain(v)", valRelease: "ks_release(v)", keyArr: "i64", valArr: "obj"},
	{name: "str_i64", keyC: "ks_obj *", valC: "int64_t", keyHash: "ks_hash_str(key)", keyEq: "ks_str_eq(a, b)", keyRetain: "ks_retain(key)", keyRelease: "ks_release(key)", valRetain: "(v)", valRelease: "(void)(v)", keyArr: "obj", valArr: "i64"},
	{name: "str_bool", keyC: "ks_obj *", valC: "bool", keyHash: "ks_hash_str(key)", keyEq: "ks_str_eq(a, b)", keyRetain: "ks_retain(key)", keyRelease: "ks_release(key)", valRetain: "(v)", valRelease: "(void)(v)", keyArr: "obj", valArr: "bool"},
	{name: "str_f64", keyC: "ks_obj *", valC: "double", keyHash: "ks_hash_str(key)", keyEq: "ks_str_eq(a, b)", keyRetain: "ks_retain(key)", keyRelease: "ks_release(key)", valRetain: "(v)", valRelease: "(void)(v)", keyArr: "obj", valArr: "f64"},
	{name: "str_obj", keyC: "ks_obj *", valC: "ks_obj *", keyHash: "ks_hash_str(key)", keyEq: "ks_str_eq(a, b)", keyRetain: "ks_retain(key)", keyRelease: "ks_release(key)", valRetain: "ks_retain(v)", valRelease: "ks_release(v)", keyArr: "obj", valArr: "obj"},
}

const mapHelpers = `
static uint64_t ks_seed0, ks_seed1;

// ks_init_seed picks the per-process hash key, so nobody outside the
// process can choose map keys that collide.
KS_RT void ks_init_seed(void) {
	uint64_t k[2] = {0, 0};
	int fd = open("/dev/urandom", O_RDONLY | O_CLOEXEC);
	bool ok = false;
	if (fd >= 0) {
		ok = read(fd, k, sizeof k) == (ssize_t)sizeof k;
		close(fd);
	}
	if (!ok) {
		struct timespec ts;
		clock_gettime(CLOCK_REALTIME, &ts);
		k[0] = (uint64_t)ts.tv_nsec ^ ((uint64_t)ts.tv_sec << 20) ^ (uint64_t)(uintptr_t)&ts;
		k[1] = ((uint64_t)getpid() << 32) ^ (uint64_t)(uintptr_t)ks_init_seed;
	}
	ks_seed0 = k[0];
	ks_seed1 = k[1];
}

KS_RT uint64_t ks_hash_i64(int64_t k) {
	uint64_t x = (uint64_t)k ^ ks_seed0;
	x ^= x >> 30;
	x *= 0xbf58476d1ce4e5b9ULL;
	x ^= x >> 27;
	x *= 0x94d049bb133111ebULL;
	x ^= x >> 31;
	return x;
}

#define KS_ROTL(x, b) (uint64_t)(((x) << (b)) | ((x) >> (64 - (b))))
#define KS_SIPROUND \
	do { \
		v0 += v1; v1 = KS_ROTL(v1, 13); v1 ^= v0; v0 = KS_ROTL(v0, 32); \
		v2 += v3; v3 = KS_ROTL(v3, 16); v3 ^= v2; \
		v0 += v3; v3 = KS_ROTL(v3, 21); v3 ^= v0; \
		v2 += v1; v1 = KS_ROTL(v1, 17); v1 ^= v2; v2 = KS_ROTL(v2, 32); \
	} while (0)

// SipHash-1-3 keyed with the per-process seed.
KS_RT uint64_t ks_hash_str(ks_obj *o) {
	ks_str *s = KS_S(o);
	const unsigned char *in = (const unsigned char *)s->data;
	size_t len = (size_t)s->len;
	uint64_t v0 = 0x736f6d6570736575ULL ^ ks_seed0;
	uint64_t v1 = 0x646f72616e646f6dULL ^ ks_seed1;
	uint64_t v2 = 0x6c7967656e657261ULL ^ ks_seed0;
	uint64_t v3 = 0x7465646279746573ULL ^ ks_seed1;
	size_t whole = len - (len % 8);
	for (size_t i = 0; i < whole; i += 8) {
		uint64_t m;
		memcpy(&m, in + i, 8);
		v3 ^= m;
		KS_SIPROUND;
		v0 ^= m;
	}
	uint64_t b = ((uint64_t)len) << 56;
	for (size_t i = 0; i < len % 8; i++) b |= ((uint64_t)in[whole + i]) << (8 * i);
	v3 ^= b;
	KS_SIPROUND;
	v0 ^= b;
	v2 ^= 0xff;
	KS_SIPROUND;
	KS_SIPROUND;
	KS_SIPROUND;
	return v0 ^ v1 ^ v2 ^ v3;
}

KS_RT size_t ks_array_bytes(int64_t count, size_t size) {
	if (count < 0 || (uint64_t)count > SIZE_MAX / size) ks_panic("map too large");
	return (size_t)count * size;
}
`

func emitMap(m mapInst) string {
	n := "ks_map_" + m.name
	return strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(mapTemplate,
		"NAME", n),
		"KEYC", m.keyC),
		"VALC", m.valC),
		"KEYHASH", m.keyHash),
		"KEYEQ", m.keyEq),
		"KEYRETAIN", m.keyRetain),
		"KEYRELEASE", m.keyRelease),
		"VALRETAIN", m.valRetain),
		"VALRELEASE", m.valRelease),
		"KEYARR", m.keyArr),
		"VALARR", m.valArr)
}

const mapTemplate = `
// Maps keep insertion order: entries live in arrays in the order they were
// added, and a power-of-two index table maps hashes to entry numbers.
// Deleting marks an entry dead; its index slot keeps pointing at it as a
// tombstone until the next rebuild compacts the entries.
typedef struct {
	ks_obj h;
	int64_t len;
	int64_t n;
	int64_t ecap;
	uint8_t *alive;
	KEYC *keys;
	VALC *vals;
	int64_t icap;
	int64_t *index;
} NAME;

KS_RT void NAME_drop(ks_obj *o);
KS_RT ks_obj *NAME_clone(ks_obj *o);

KS_RT ks_obj *NAME_new(int64_t cap) {
	if (cap < 4) cap = 4;
	int64_t icap = 8;
	while (icap < cap * 2) {
		if (icap > INT64_MAX / 4) ks_panic("map too large");
		icap *= 2;
	}
	NAME *m = ks_alloc(sizeof *m);
	ks_init_obj(&m->h, NAME_drop, NAME_clone);
	m->len = 0;
	m->n = 0;
	m->ecap = cap;
	m->alive = ks_alloc(ks_array_bytes(cap, 1));
	m->keys = ks_alloc(ks_array_bytes(cap, sizeof(KEYC)));
	m->vals = ks_alloc(ks_array_bytes(cap, sizeof(VALC)));
	m->icap = icap;
	m->index = ks_alloc(ks_array_bytes(icap, sizeof(int64_t)));
	memset(m->index, 0, (size_t)icap * sizeof(int64_t));
	return &m->h;
}

KS_RT void NAME_drop(ks_obj *o) {
	NAME *m = (NAME *)o;
	for (int64_t i = 0; i < m->n; i++) {
		if (!m->alive[i]) continue;
		KEYC key = m->keys[i];
		KEYRELEASE;
		VALC v = m->vals[i];
		VALRELEASE;
	}
	free(m->alive);
	free(m->keys);
	free(m->vals);
	free(m->index);
	free(o);
}

KS_RT void NAME_place(NAME *m, int64_t e) {
	KEYC key = m->keys[e];
	int64_t mask = m->icap - 1;
	int64_t i = (int64_t)(KEYHASH & (uint64_t)mask);
	while (m->index[i] != 0) i = (i + 1) & mask;
	m->index[i] = e + 1;
}

KS_RT ks_obj *NAME_clone(ks_obj *o) {
	NAME *m = (NAME *)o;
	ks_obj *r = NAME_new(m->len);
	NAME *c = (NAME *)r;
	for (int64_t i = 0; i < m->n; i++) {
		if (!m->alive[i]) continue;
		KEYC key = m->keys[i];
		VALC v = m->vals[i];
		int64_t e = c->n++;
		c->keys[e] = KEYRETAIN;
		c->vals[e] = VALRETAIN;
		c->alive[e] = 1;
		NAME_place(c, e);
	}
	c->len = c->n;
	return r;
}

KS_RT int64_t NAME_len(ks_obj *o) {
	return ((NAME *)o)->len;
}

KS_RT int64_t NAME_find(NAME *m, KEYC key) {
	int64_t mask = m->icap - 1;
	int64_t i = (int64_t)(KEYHASH & (uint64_t)mask);
	for (;;) {
		int64_t e = m->index[i];
		if (e == 0) return -1;
		e--;
		if (m->alive[e]) {
			KEYC a = m->keys[e];
			KEYC b = key;
			if (KEYEQ) return e;
		}
		i = (i + 1) & mask;
	}
}

// NAME_rebuild drops dead entries and rehashes into icap index slots.
KS_RT void NAME_rebuild(NAME *m, int64_t icap) {
	int64_t j = 0;
	for (int64_t i = 0; i < m->n; i++) {
		if (!m->alive[i]) continue;
		m->keys[j] = m->keys[i];
		m->vals[j] = m->vals[i];
		m->alive[j] = 1;
		j++;
	}
	m->n = j;
	free(m->index);
	m->icap = icap;
	m->index = ks_alloc(ks_array_bytes(icap, sizeof(int64_t)));
	memset(m->index, 0, (size_t)icap * sizeof(int64_t));
	for (int64_t e = 0; e < m->n; e++) NAME_place(m, e);
}

KS_RT void NAME_set(ks_obj *o, KEYC key, VALC val) {
	NAME *m = (NAME *)o;
	int64_t e = NAME_find(m, key);
	if (e >= 0) {
		VALC v = m->vals[e];
		VALRELEASE;
		m->vals[e] = val;
		return;
	}
	if ((m->n + 1) * 4 > m->icap * 3) {
		int64_t icap = m->icap;
		while ((m->len + 1) * 2 > icap) {
			if (icap > INT64_MAX / 4) ks_panic("map too large");
			icap *= 2;
		}
		NAME_rebuild(m, icap);
	}
	if (m->n == m->ecap) {
		if (m->ecap > INT64_MAX / 2) ks_panic("map too large");
		int64_t ncap = m->ecap * 2;
		m->alive = ks_realloc(m->alive, ks_array_bytes(ncap, 1));
		m->keys = ks_realloc(m->keys, ks_array_bytes(ncap, sizeof(KEYC)));
		m->vals = ks_realloc(m->vals, ks_array_bytes(ncap, sizeof(VALC)));
		m->ecap = ncap;
	}
	e = m->n++;
	m->keys[e] = KEYRETAIN;
	m->vals[e] = val;
	m->alive[e] = 1;
	m->len++;
	NAME_place(m, e);
}

KS_RT bool NAME_delete(ks_obj *o, KEYC key) {
	NAME *m = (NAME *)o;
	int64_t e = NAME_find(m, key);
	if (e < 0) return false;
	{
		KEYC key = m->keys[e];
		KEYRELEASE;
		VALC v = m->vals[e];
		VALRELEASE;
	}
	m->alive[e] = 0;
	m->len--;
	if (m->len == 0) {
		m->n = 0;
		memset(m->index, 0, (size_t)m->icap * sizeof(int64_t));
	}
	return true;
}

KS_RT bool NAME_has(ks_obj *o, KEYC key) {
	return NAME_find((NAME *)o, key) >= 0;
}

KS_RT VALC NAME_get(ks_obj *o, KEYC key) {
	NAME *m = (NAME *)o;
	int64_t e = NAME_find(m, key);
	if (e < 0) ks_panic("map key not found");
	return m->vals[e];
}

KS_RT VALC *NAME_slot(ks_obj *o, KEYC key) {
	NAME *m = (NAME *)o;
	int64_t e = NAME_find(m, key);
	if (e < 0) ks_panic("map key not found");
	return &m->vals[e];
}

KS_RT ks_obj *NAME_keys(ks_obj *o) {
	NAME *m = (NAME *)o;
	ks_obj *a = ks_arr_KEYARR_new(m->len);
	for (int64_t i = 0; i < m->n; i++) {
		if (!m->alive[i]) continue;
		KEYC key = m->keys[i];
		ks_arr_KEYARR_push(a, KEYRETAIN);
	}
	return a;
}

KS_RT ks_obj *NAME_values(ks_obj *o) {
	NAME *m = (NAME *)o;
	ks_obj *a = ks_arr_VALARR_new(m->len);
	for (int64_t i = 0; i < m->n; i++) {
		if (!m->alive[i]) continue;
		VALC v = m->vals[i];
		ks_arr_VALARR_push(a, VALRETAIN);
	}
	return a;
}
`
