// Host test runner. Collects the per-module test tables and executes them,
// printing a per-test pass/fail line and a final summary. Exits nonzero if any
// CHECK failed so CI and `make test` report failure.

#include <stdio.h>

#include "test_harness.h"

int g_test_failures = 0;
int g_test_checks = 0;

const test_case_t *hook_tests(int *count);
const test_case_t *keypad_tests(int *count);
const test_case_t *uart_tests(int *count);
const test_case_t *fsm_tests(int *count);

typedef const test_case_t *(*suite_fn_t)(int *count);

typedef struct {
    const char *name;
    suite_fn_t suite;
} suite_t;

static const suite_t k_suites[] = {
    {"hook", hook_tests},
    {"keypad", keypad_tests},
    {"uart_proto", uart_tests},
    {"phone_fsm", fsm_tests},
};

int main(void) {
    int total_tests = 0;
    int failed_tests = 0;

    for (unsigned s = 0; s < sizeof(k_suites) / sizeof(k_suites[0]); ++s) {
        int count = 0;
        const test_case_t *cases = k_suites[s].suite(&count);
        printf("[%s] %d test(s)\n", k_suites[s].name, count);
        for (int i = 0; i < count; ++i) {
            int before = g_test_failures;
            cases[i].fn();
            total_tests++;
            int delta = g_test_failures - before;
            if (delta == 0) {
                printf("  ok   %s\n", cases[i].name);
            } else {
                failed_tests++;
                printf("  FAIL %s (%d check failure(s))\n", cases[i].name, delta);
            }
        }
    }

    printf("\n%d test(s), %d check(s), %d failure(s)\n",
           total_tests, g_test_checks, g_test_failures);
    if (failed_tests > 0) {
        printf("RESULT: FAIL (%d test(s) failed)\n", failed_tests);
        return 1;
    }
    printf("RESULT: PASS\n");
    return 0;
}
