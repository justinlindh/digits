#ifndef DIGITS_TEST_HARNESS_H
#define DIGITS_TEST_HARNESS_H

// Tiny plain-C test harness: no external dependency, no Pico SDK. Each test is
// a void(void) function registered with TEST(). The runner executes them all,
// counts CHECK failures, and returns nonzero if any failed.
//
// CHECK(cond) records a failure but keeps going within the test so one run can
// surface multiple problems. CHECK_EQ / CHECK_STREQ are conveniences that
// print the offending values.

#include <stdio.h>
#include <string.h>

typedef void (*test_fn_t)(void);

typedef struct {
    const char *name;
    test_fn_t fn;
} test_case_t;

// Table-entry shorthand: TEST_CASE(foo) expands to {"foo", foo}.
#define TEST_CASE(fn) {#fn, fn}

// Defines the suite accessor a per-module test file exposes to test_main.c. Use
// after declaring `static const test_case_t k_<suite>_tests[]`, e.g.
// DEFINE_SUITE(hook). The runner calls <suite>_tests(&count).
#define DEFINE_SUITE(suite)                                            \
    const test_case_t *suite##_tests(int *count);                      \
    const test_case_t *suite##_tests(int *count) {                     \
        *count = (int)(sizeof(k_##suite##_tests) /                     \
                       sizeof(k_##suite##_tests[0]));                  \
        return k_##suite##_tests;                                      \
    }

// Filled in by the runner; see test_main.c.
extern int g_test_failures;
extern int g_test_checks;

#define CHECK(cond)                                                            \
    do {                                                                       \
        g_test_checks++;                                                       \
        if (!(cond)) {                                                         \
            g_test_failures++;                                                 \
            printf("    FAIL %s:%d: CHECK(%s)\n", __FILE__, __LINE__, #cond);  \
        }                                                                      \
    } while (0)

#define CHECK_EQ(actual, expected)                                            \
    do {                                                                       \
        g_test_checks++;                                                       \
        long long _a = (long long)(actual);                                   \
        long long _e = (long long)(expected);                                 \
        if (_a != _e) {                                                       \
            g_test_failures++;                                                 \
            printf("    FAIL %s:%d: CHECK_EQ(%s, %s) got %lld want %lld\n",    \
                   __FILE__, __LINE__, #actual, #expected, _a, _e);           \
        }                                                                      \
    } while (0)

#define CHECK_STREQ(actual, expected)                                         \
    do {                                                                       \
        g_test_checks++;                                                       \
        const char *_a = (actual);                                            \
        const char *_e = (expected);                                          \
        if (_a == NULL || _e == NULL || strcmp(_a, _e) != 0) {                \
            g_test_failures++;                                                 \
            printf("    FAIL %s:%d: CHECK_STREQ(%s, %s) got \"%s\" want \"%s\"\n", \
                   __FILE__, __LINE__, #actual, #expected,                    \
                   _a ? _a : "(null)", _e ? _e : "(null)");                   \
        }                                                                      \
    } while (0)

#endif  // DIGITS_TEST_HARNESS_H
