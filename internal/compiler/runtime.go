package compiler

const runtimeC = `#include <inttypes.h>
#include <stdbool.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

static inline __attribute__((unused)) void ks_panic(const char *msg) {
	fflush(stdout);
	fprintf(stderr, "kestrel: runtime error: %s\n", msg);
	exit(101);
}

static inline __attribute__((unused)) int64_t ks_add(int64_t a, int64_t b) {
	int64_t r;
	if (__builtin_add_overflow(a, b, &r)) ks_panic("integer overflow");
	return r;
}

static inline __attribute__((unused)) int64_t ks_sub(int64_t a, int64_t b) {
	int64_t r;
	if (__builtin_sub_overflow(a, b, &r)) ks_panic("integer overflow");
	return r;
}

static inline __attribute__((unused)) int64_t ks_mul(int64_t a, int64_t b) {
	int64_t r;
	if (__builtin_mul_overflow(a, b, &r)) ks_panic("integer overflow");
	return r;
}

static inline __attribute__((unused)) int64_t ks_div(int64_t a, int64_t b) {
	if (b == 0) ks_panic("division by zero");
	if (a == INT64_MIN && b == -1) ks_panic("integer overflow");
	return a / b;
}

static inline __attribute__((unused)) int64_t ks_mod(int64_t a, int64_t b) {
	if (b == 0) ks_panic("division by zero");
	if (b == -1) return 0;
	return a % b;
}

static inline __attribute__((unused)) int64_t ks_neg(int64_t a) {
	if (a == INT64_MIN) ks_panic("integer overflow");
	return -a;
}

static inline __attribute__((unused)) void *ks_alloc(size_t n) {
	void *p = malloc(n);
	if (p == NULL) ks_panic("out of memory");
	return p;
}

static inline __attribute__((unused)) const char *ks_concat(const char *a, const char *b) {
	size_t la = strlen(a);
	size_t lb = strlen(b);
	if (la > SIZE_MAX - lb - 1) ks_panic("string too long");
	char *r = ks_alloc(la + lb + 1);
	memcpy(r, a, la);
	memcpy(r + la, b, lb + 1);
	return r;
}

static inline __attribute__((unused)) bool ks_streq(const char *a, const char *b) {
	return strcmp(a, b) == 0;
}

static inline __attribute__((unused)) int64_t ks_len(const char *s) {
	return (int64_t)strlen(s);
}

static inline __attribute__((unused)) const char *ks_str(int64_t v) {
	char buf[32];
	int n = snprintf(buf, sizeof buf, "%" PRId64, v);
	char *r = ks_alloc((size_t)n + 1);
	memcpy(r, buf, (size_t)n + 1);
	return r;
}

static inline __attribute__((unused)) void ks_print_int(int64_t v) {
	printf("%" PRId64, v);
}

static inline __attribute__((unused)) void ks_print_bool(bool v) {
	fputs(v ? "true" : "false", stdout);
}

static inline __attribute__((unused)) void ks_print_string(const char *s) {
	fputs(s, stdout);
}
`
