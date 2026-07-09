// Cross-language drift guard for the phase byte constants. They are duplicated
// in firmware/src/phase.h (C, as PHASE_<NAME>) and in
// pi/digitsd/internal/phone/serial.go (Go, as Phase<Name>). The firmware and
// digitsd exchange these bytes over UART (PHASE? / STATE:SET), so a silent
// divergence would break phase reporting on real hardware. This test parses
// both files at their canonical repo paths and asserts the name/value sets
// match. It reads the pi/ source but never writes it.
//
// REPO_ROOT is baked in at configure time (see test/CMakeLists.txt), so the
// test works regardless of the directory the binary runs from.

#include "test_harness.h"

#include <ctype.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#ifndef REPO_ROOT
#error "REPO_ROOT must be defined by the build (see test/CMakeLists.txt)"
#endif

#define PHASE_H_PATH   REPO_ROOT "/firmware/src/phase.h"
#define SERIAL_GO_PATH REPO_ROOT "/pi/digitsd/internal/phone/serial.go"

#define MAX_PHASES 16

typedef struct {
    char name[32];  // logical name, uppercased, prefix stripped
    long value;
} phase_entry_t;

// Uppercase in place and drop underscores. The C names are snake_case
// (PHASE_ON_HOOK) while the Go names are CamelCase with no separators
// (PhaseOnHook). Collapsing both to underscore-free uppercase makes a
// hypothetical two-word phase compare equal instead of spuriously failing
// (ON_HOOK vs ONHOOK). Single-word phases are unaffected.
static void normalize(char *s) {
    char *w = s;
    for (const char *r = s; *r != '\0'; ++r) {
        if (*r == '_') {
            continue;
        }
        *w++ = (char)toupper((unsigned char)*r);
    }
    *w = '\0';
}

// Copy the next whitespace-delimited token from *p into out (bounded) and
// advance *p past it. Returns 0 when no token remains.
static int next_token(const char **p, char *out, size_t out_sz) {
    const char *s = *p;
    while (*s != '\0' && isspace((unsigned char)*s)) {
        s++;
    }
    if (*s == '\0') {
        *p = s;
        return 0;
    }
    size_t n = 0;
    while (*s != '\0' && !isspace((unsigned char)*s)) {
        if (n + 1 < out_sz) {
            out[n++] = *s;
        }
        s++;
    }
    out[n] = '\0';
    *p = s;
    return 1;
}

// Parse `#define PHASE_<NAME> <value>` lines. Phase bytes are single-byte
// values, so defines whose value falls outside [0, 0xFF] are skipped: this
// naturally excludes the PHASE_FLASH_OFFSET / PHASE_FLASH_ADDR address macros
// that share the PHASE_ prefix. Returns the entry count, or -1 if the file
// could not be opened.
static int parse_c_defines(const char *path, phase_entry_t *out) {
    FILE *f = fopen(path, "r");
    if (f == NULL) {
        return -1;
    }
    char line[256];
    int n = 0;
    while (fgets(line, sizeof(line), f) != NULL) {
        const char *p = strstr(line, "#define");
        if (p == NULL) {
            continue;
        }
        p += strlen("#define");
        char name_tok[64];
        if (!next_token(&p, name_tok, sizeof(name_tok))) {
            continue;
        }
        if (strncmp(name_tok, "PHASE_", 6) != 0) {
            continue;
        }
        char val_tok[64];
        if (!next_token(&p, val_tok, sizeof(val_tok))) {
            continue;
        }
        char *end = NULL;
        long v = strtol(val_tok, &end, 0);
        if (end == val_tok || v < 0 || v > 0xFF) {
            continue;
        }
        if (n < MAX_PHASES) {
            snprintf(out[n].name, sizeof(out[n].name), "%s", name_tok + 6);
            normalize(out[n].name);
            out[n].value = v;
            n++;
        }
    }
    fclose(f);
    return n;
}

// Parse `Phase<Name> [uint8] = <value>` const lines. The type keyword is not
// required: Go lets a const block declare the type on the first line only and
// omit it on the rest, so keying on "uint8" would silently drop those phases
// and the failure would misread as "Go has fewer phases". We instead match any
// Phase<Uppercase> identifier assigned a byte literal. Bounding the value to
// [0, 0xFF] (as the C parser does) keeps a stray non-phase numeric assignment
// like `PhaseTimeout = 5000` from being mistaken for a phase. Returns the entry
// count, or -1 if the file could not be opened.
static int parse_go_consts(const char *path, phase_entry_t *out) {
    FILE *f = fopen(path, "r");
    if (f == NULL) {
        return -1;
    }
    char line[256];
    int n = 0;
    while (fgets(line, sizeof(line), f) != NULL) {
        const char *p = line;
        while (*p != '\0' && isspace((unsigned char)*p)) {
            p++;
        }
        // Require Phase<Uppercase> so a bare "Phase" or a comment does not match.
        if (strncmp(p, "Phase", 5) != 0 || !isupper((unsigned char)p[5])) {
            continue;
        }
        const char *eq = strchr(p, '=');
        if (eq == NULL) {
            continue;
        }
        char ident[64];
        const char *q = p;
        if (!next_token(&q, ident, sizeof(ident))) {
            continue;
        }
        const char *vp = eq + 1;
        char val_tok[64];
        if (!next_token(&vp, val_tok, sizeof(val_tok))) {
            continue;
        }
        char *end = NULL;
        long v = strtol(val_tok, &end, 0);
        if (end == val_tok || v < 0 || v > 0xFF) {
            continue;
        }
        if (n < MAX_PHASES) {
            snprintf(out[n].name, sizeof(out[n].name), "%s", ident + 5);
            normalize(out[n].name);
            out[n].value = v;
            n++;
        }
    }
    fclose(f);
    return n;
}

static long find_value(const phase_entry_t *set, int count, const char *name) {
    for (int i = 0; i < count; ++i) {
        if (strcmp(set[i].name, name) == 0) {
            return set[i].value;
        }
    }
    return -1;
}

static void test_phase_constants_match(void) {
    phase_entry_t c[MAX_PHASES];
    phase_entry_t g[MAX_PHASES];

    int cn = parse_c_defines(PHASE_H_PATH, c);
    int gn = parse_go_consts(SERIAL_GO_PATH, g);

    if (cn < 0) {
        printf("    could not open %s\n", PHASE_H_PATH);
    }
    if (gn < 0) {
        printf("    could not open %s\n", SERIAL_GO_PATH);
    }

    // Both files must yield a non-empty set, and the two sets must be the same
    // size. A parser that silently found nothing would fail here.
    CHECK(cn > 0);
    CHECK(gn > 0);
    CHECK_EQ(cn, gn);

    // Anchor: the parser really extracted the phase table, not garbage.
    CHECK_EQ(find_value(c, cn, "PAIRED"), 0x01);

    // Every C phase has a Go counterpart with the same value.
    for (int i = 0; i < cn; ++i) {
        long gv = find_value(g, gn, c[i].name);
        if (gv < 0) {
            printf("    C phase %s (0x%02lX) has no Go counterpart\n",
                   c[i].name, c[i].value);
        }
        CHECK(gv >= 0);
        if (gv >= 0) {
            CHECK_EQ(c[i].value, gv);
        }
    }

    // Every Go phase has a C counterpart (catches a phase added only in Go).
    for (int j = 0; j < gn; ++j) {
        long cv = find_value(c, cn, g[j].name);
        if (cv < 0) {
            printf("    Go phase %s (0x%02lX) has no C counterpart\n",
                   g[j].name, g[j].value);
        }
        CHECK(cv >= 0);
    }
}

static const test_case_t k_phase_sync_tests[] = {
    TEST_CASE(test_phase_constants_match),
};

DEFINE_SUITE(phase_sync)
