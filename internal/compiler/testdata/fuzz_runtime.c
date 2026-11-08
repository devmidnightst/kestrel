// Mutation fuzzer for the runtime's parsers (JSON, URL, base64, hex).
// TestRuntimeParserFuzz compiles it against a generated program's C
// (rt.c) with AddressSanitizer and UndefinedBehaviorSanitizer.
#define main ks_unused_main
#include "rt.c"
#undef main
static int fuzz_one(const uint8_t *data, size_t size) {
	ks_init_seed();
	ks_obj *doc = ks_str_from((const char *)data, (int64_t)size);
	static const char *paths[] = {"", "a", "a.b", "0", "0.1.x", "a.0.b.1", ".", "a..b"};
	ks_release(ks_res_ok_bool(ks_json_valid(doc)));
	for (size_t i = 0; i < sizeof paths / sizeof *paths; i++) {
		ks_obj *p = ks_str_cstr(paths[i]);
		ks_release(ks_json_string(doc, p));
		ks_release(ks_json_int(doc, p));
		ks_release(ks_json_float(doc, p));
		ks_release(ks_json_bool(doc, p));
		ks_release(ks_json_len(doc, p));
		ks_release(ks_json_keys(doc, p));
		ks_release(ks_json_raw(doc, p));
		ks_release(ks_json_member(doc, p));
		ks_release(ks_json_type(doc, p));
		ks_release(p);
	}
	ks_release(ks_json_quote(doc));
	ks_release(ks_url_decode(doc));
	ks_release(ks_unbase64(doc));
	ks_release(ks_unhex(doc));
	ks_release(ks_base64(doc));
	ks_release(ks_base64url(doc));
	ks_release(ks_url_encode(doc));
	ks_release(doc);
	return 0;
}

static uint64_t rng = 88172645463325252ULL;
static uint64_t nx(void) { rng ^= rng << 13; rng ^= rng >> 7; rng ^= rng << 17; return rng; }
int main(int argc, char **argv) {
	static const char *seeds[] = {"{\"a\":{\"b\":[1,\"x\",true]},\"0\":null}", "[[1,2],{\"x\":\"\\u00e9\\ud83d\\ude00\"}]", "{\"a\":[{\"b\":[0,{\"1\":2}]}]}", "\"\\\\\\\"\"", "-0.5e+10", "[]", "{}", "%41+%zz", "YWJj==", "ff00"};
	static const char *toks[] = {"{", "}", "[", "]", ",", ":", "\"", "\\", "\\u", "\\ud800", "\\udc00", "e", "E", "-", "+", ".", "0", "1", "9", "true", "false", "null", " ", "\n", "%", "=", "a", "b", "\x80", "\xff", "\x00"};
	long iters = argc > 1 ? atol(argv[1]) : 100000;
	unsigned char buf[8192];
	for (long it = 0; it < iters; it++) {
		const char *seed = seeds[nx() % (sizeof seeds / sizeof *seeds)];
		size_t n = strlen(seed);
		memcpy(buf, seed, n);
		int muts = 1 + (int)(nx() % 8);
		for (int m = 0; m < muts; m++) {
			int kind = (int)(nx() % 4);
			size_t pos = n ? nx() % (n + 1) : 0;
			if (kind == 0 && n > 0 && pos < n) buf[pos] = (unsigned char)nx();
			else if (kind == 1 && n > 0 && pos < n) { memmove(buf + pos, buf + pos + 1, n - pos - 1); n--; }
			else {
				const char *t = toks[nx() % (sizeof toks / sizeof *toks)];
				size_t tl = t[0] ? strlen(t) : 1;
				if (n + tl < sizeof buf - 1) { memmove(buf + pos + tl, buf + pos, n - pos); memcpy(buf + pos, t, tl); n += tl; }
			}
		}
		if (nx() % 50 == 0) { size_t d = nx() % 700; n = 0; for (size_t i = 0; i < d && n < sizeof buf - 2; i++) buf[n++] = '['; }
		fuzz_one(buf, n);
	}
	puts("done");
	return 0;
}
