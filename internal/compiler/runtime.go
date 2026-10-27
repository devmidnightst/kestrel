package compiler

import "strings"

const runtimeHead = `#define _POSIX_C_SOURCE 200809L
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
#include <sys/socket.h>
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

__attribute__((noreturn, unused)) static void ks_panic(const char *msg) {
	fflush(stdout);
	fprintf(stderr, "kestrel: runtime error: %s\n", msg);
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

KS_RT void ks_print_i64(int64_t v) {
	printf("%" PRId64, v);
}

KS_RT void ks_print_bool(bool v) {
	fputs(v ? "true" : "false", stdout);
}

KS_RT void ks_print_f64(double v) {
	char buf[48];
	ks_fmt_float(v, buf, sizeof buf);
	fputs(buf, stdout);
}

KS_RT void ks_print_str(ks_obj *s) {
	if (KS_S(s)->len > 0) fwrite(KS_S(s)->data, 1, (size_t)KS_S(s)->len, stdout);
}

KS_RT void ks_print_str_quoted(ks_obj *o) {
	ks_str *s = KS_S(o);
	putchar('"');
	for (int64_t i = 0; i < s->len; i++) {
		char c = s->data[i];
		switch (c) {
		case '"': fputs("\\\"", stdout); break;
		case '\\': fputs("\\\\", stdout); break;
		case '\n': fputs("\\n", stdout); break;
		case '\t': fputs("\\t", stdout); break;
		case '\r': fputs("\\r", stdout); break;
		default: putchar(c);
		}
	}
	putchar('"');
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

KS_RT ks_obj *ks_read_file(ks_obj *path) {
	const char *p = ks_cpath(path, "read_file");
	FILE *f = fopen(p, "rb");
	if (f == NULL) ks_panicf("read_file: cannot open \"%.200s\": %s", p, strerror(errno));
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
		fclose(f);
		ks_panicf("read_file: error reading \"%.200s\"", p);
	}
	fclose(f);
	if (len > (size_t)INT64_MAX) ks_panic("read_file: file too large");
	ks_obj *r = ks_str_from(buf, (int64_t)len);
	free(buf);
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

KS_RT int64_t ks_tcp_open(ks_obj *host, int64_t port, bool listen_mode) {
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
			if (bind(fd, ai->ai_addr, ai->ai_addrlen) == 0 && listen(fd, 128) == 0) break;
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
	return ks_tcp_open(host, port, true);
}

KS_RT int64_t ks_tcp_connect(ks_obj *host, int64_t port) {
	return ks_tcp_open(host, port, false);
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

KS_RT int64_t ks_tcp_accept(int64_t fd) {
	if (fd < 0 || fd > INT32_MAX) return -1;
	int c = accept((int)fd, NULL, NULL);
	if (c < 0) return -1;
	return ks_tcp_cloexec(c);
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
KS_RT uint64_t ks_hash_i64(int64_t k) {
	uint64_t x = (uint64_t)k;
	x ^= x >> 30;
	x *= 0xbf58476d1ce4e5b9ULL;
	x ^= x >> 27;
	x *= 0x94d049bb133111ebULL;
	x ^= x >> 31;
	return x;
}

KS_RT uint64_t ks_hash_str(ks_obj *o) {
	ks_str *s = KS_S(o);
	uint64_t h = 14695981039346656037ULL;
	for (int64_t i = 0; i < s->len; i++) {
		h ^= (unsigned char)s->data[i];
		h *= 1099511628211ULL;
	}
	return h;
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
typedef struct {
	ks_obj h;
	int64_t len;
	int64_t cap;
	uint8_t *used;
	KEYC *keys;
	VALC *vals;
} NAME;

KS_RT void NAME_drop(ks_obj *o);
KS_RT ks_obj *NAME_clone(ks_obj *o);

KS_RT ks_obj *NAME_new(int64_t cap) {
	if (cap < 8) cap = 8;
	int64_t p2 = 8;
	while (p2 < cap) {
		if (p2 > INT64_MAX / 2) ks_panic("map too large");
		p2 *= 2;
	}
	NAME *m = ks_alloc(sizeof *m);
	ks_init_obj(&m->h, NAME_drop, NAME_clone);
	m->len = 0;
	m->cap = p2;
	m->used = ks_alloc((size_t)p2);
	memset(m->used, 0, (size_t)p2);
	m->keys = ks_alloc((size_t)p2 * sizeof(KEYC));
	m->vals = ks_alloc((size_t)p2 * sizeof(VALC));
	return &m->h;
}

KS_RT void NAME_drop(ks_obj *o) {
	NAME *m = (NAME *)o;
	for (int64_t i = 0; i < m->cap; i++) {
		if (m->used[i] != 1) continue;
		KEYC key = m->keys[i];
		KEYRELEASE;
		VALC v = m->vals[i];
		VALRELEASE;
	}
	free(m->used);
	free(m->keys);
	free(m->vals);
	free(o);
}

KS_RT ks_obj *NAME_clone(ks_obj *o) {
	NAME *m = (NAME *)o;
	ks_obj *r = NAME_new(m->cap);
	NAME *c = (NAME *)r;
	c->len = m->len;
	memcpy(c->used, m->used, (size_t)m->cap);
	memcpy(c->keys, m->keys, (size_t)m->cap * sizeof(KEYC));
	memcpy(c->vals, m->vals, (size_t)m->cap * sizeof(VALC));
	for (int64_t i = 0; i < m->cap; i++) {
		if (m->used[i] != 1) continue;
		KEYC key = c->keys[i];
		c->keys[i] = KEYRETAIN;
		VALC v = c->vals[i];
		c->vals[i] = VALRETAIN;
	}
	return r;
}

KS_RT int64_t NAME_len(ks_obj *o) {
	return ((NAME *)o)->len;
}

KS_RT int64_t NAME_find(NAME *m, KEYC key, bool *found) {
	uint64_t h = KEYHASH;
	int64_t mask = m->cap - 1;
	int64_t i = (int64_t)(h & (uint64_t)mask);
	int64_t tomb = -1;
	for (int64_t n = 0; n < m->cap; n++) {
		uint8_t u = m->used[i];
		if (u == 0) { *found = false; return tomb >= 0 ? tomb : i; }
		if (u == 2) {
			if (tomb < 0) tomb = i;
		} else {
			KEYC a = m->keys[i];
			KEYC b = key;
			if (KEYEQ) { *found = true; return i; }
		}
		i = (i + 1) & mask;
	}
	*found = false;
	if (tomb >= 0) return tomb;
	ks_panic("map too large");
	return 0;
}

KS_RT void NAME_grow(NAME *m) {
	int64_t ncap = m->cap == 0 ? 8 : m->cap * 2;
	if (m->cap > 0 && m->cap > INT64_MAX / 2) ks_panic("map too large");
	uint8_t *used = ks_alloc((size_t)ncap);
	memset(used, 0, (size_t)ncap);
	KEYC *keys = ks_alloc((size_t)ncap * sizeof(KEYC));
	VALC *vals = ks_alloc((size_t)ncap * sizeof(VALC));
	uint8_t *oldu = m->used;
	KEYC *oldk = m->keys;
	VALC *oldv = m->vals;
	int64_t ocap = m->cap;
	m->used = used;
	m->keys = keys;
	m->vals = vals;
	m->cap = ncap;
	m->len = 0;
	for (int64_t i = 0; i < ocap; i++) {
		if (oldu[i] != 1) continue;
		bool found = false;
		int64_t slot = NAME_find(m, oldk[i], &found);
		m->used[slot] = 1;
		m->keys[slot] = oldk[i];
		m->vals[slot] = oldv[i];
		m->len++;
	}
	free(oldu);
	free(oldk);
	free(oldv);
}

KS_RT void NAME_set(ks_obj *o, KEYC key, VALC val) {
	NAME *m = (NAME *)o;
	if (m->cap == 0 || (m->len + 1) * 4 > m->cap * 3) NAME_grow(m);
	bool found = false;
	int64_t i = NAME_find(m, key, &found);
	if (found) {
		VALC v = m->vals[i];
		VALRELEASE;
		m->vals[i] = val;
		return;
	}
	m->used[i] = 1;
	m->keys[i] = KEYRETAIN;
	m->vals[i] = val;
	m->len++;
}

KS_RT bool NAME_delete(ks_obj *o, KEYC key) {
	NAME *m = (NAME *)o;
	if (m->cap == 0) return false;
	bool found = false;
	int64_t i = NAME_find(m, key, &found);
	if (!found) return false;
	{
		KEYC key = m->keys[i];
		KEYRELEASE;
		VALC v = m->vals[i];
		VALRELEASE;
	}
	m->used[i] = 2;
	m->len--;
	return true;
}

KS_RT bool NAME_has(ks_obj *o, KEYC key) {
	NAME *m = (NAME *)o;
	if (m->cap == 0) return false;
	bool found = false;
	NAME_find(m, key, &found);
	return found;
}

KS_RT VALC NAME_get(ks_obj *o, KEYC key) {
	NAME *m = (NAME *)o;
	bool found = false;
	int64_t i = 0;
	if (m->cap != 0) i = NAME_find(m, key, &found);
	if (!found) ks_panic("map key not found");
	return m->vals[i];
}

KS_RT VALC *NAME_slot(ks_obj *o, KEYC key) {
	NAME *m = (NAME *)o;
	bool found = false;
	int64_t i = 0;
	if (m->cap != 0) i = NAME_find(m, key, &found);
	if (!found) ks_panic("map key not found");
	return &m->vals[i];
}

KS_RT ks_obj *NAME_keys(ks_obj *o) {
	NAME *m = (NAME *)o;
	ks_obj *a = ks_arr_KEYARR_new(m->len);
	for (int64_t i = 0; i < m->cap; i++) {
		if (m->used[i] != 1) continue;
		KEYC key = m->keys[i];
		ks_arr_KEYARR_push(a, KEYRETAIN);
	}
	return a;
}

KS_RT ks_obj *NAME_values(ks_obj *o) {
	NAME *m = (NAME *)o;
	ks_obj *a = ks_arr_VALARR_new(m->len);
	for (int64_t i = 0; i < m->cap; i++) {
		if (m->used[i] != 1) continue;
		VALC v = m->vals[i];
		ks_arr_VALARR_push(a, VALRETAIN);
	}
	return a;
}
`
