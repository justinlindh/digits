// Smoke test for tools/semantic-release-squash-expander.cjs.
// Run with: node tools/semantic-release-squash-expander.test.cjs

const assert = require("assert");
const { _expandCommits, _splitSquashBullets } = require("./semantic-release-squash-expander.cjs");

function test(name, fn) {
    try {
        fn();
        console.log(`PASS ${name}`);
    } catch (e) {
        console.error(`FAIL ${name}`);
        console.error(e);
        process.exitCode = 1;
    }
}

test("non-squash commit is left untouched", () => {
    const commit = {
        hash: "abc123",
        message: "fix(server): handle NULL line_id\n\nDetail body line one.\nDetail body line two.",
        subject: "fix(server): handle NULL line_id",
        body: "Detail body line one.\nDetail body line two.",
    };
    const out = _expandCommits([commit]);
    assert.deepStrictEqual(out, [commit]);
});

test("commit with bullets that are not conventional commits is left untouched", () => {
    const commit = {
        hash: "abc123",
        message:
            "docs: update README\n\nChanges:\n* Cleaned up the install steps\n* Added a troubleshooting section",
    };
    const out = _expandCommits([commit]);
    assert.strictEqual(out.length, 1);
    assert.strictEqual(out[0].message, commit.message);
});

test("github squash with two bullets expands into two virtual commits", () => {
    const commit = {
        hash: "deadbeef",
        author: { name: "Alice" },
        message:
            "feat(image): V2 carrier board hardware bring-up (#308)\n\n* fix(image): correct SWD pin number for V2 carrier\n\nThe OpenOCD bitbang config still had the V1 prototype pinout.\nDetails about the fix.\n\n* feat(pi): add MONITOR mode to UART socket\n\ndigits-pico-monitor used to stop digitsd to grab /dev/serial0.\n",
    };
    const out = _expandCommits([commit]);
    assert.strictEqual(out.length, 2);
    assert.strictEqual(out[0].subject, "fix(image): correct SWD pin number for V2 carrier");
    assert.strictEqual(out[0].header, "fix(image): correct SWD pin number for V2 carrier");
    assert.strictEqual(
        out[0].body,
        "The OpenOCD bitbang config still had the V1 prototype pinout.\nDetails about the fix.",
    );
    assert.strictEqual(out[1].subject, "feat(pi): add MONITOR mode to UART socket");
    assert.strictEqual(out[1].body, "digits-pico-monitor used to stop digitsd to grab /dev/serial0.");
    // Non-message fields propagate (hash, author, etc.) so changelog links work.
    assert.strictEqual(out[0].hash, "deadbeef");
    assert.strictEqual(out[1].hash, "deadbeef");
    assert.strictEqual(out[0].author.name, "Alice");
});

test("squash with single bullet still expands", () => {
    const commit = {
        hash: "1234",
        message: "fix(server): minor (#42)\n\n* fix(server): handle NULL line_id\n\nbody",
    };
    const out = _expandCommits([commit]);
    assert.strictEqual(out.length, 1);
    assert.strictEqual(out[0].subject, "fix(server): handle NULL line_id");
    assert.strictEqual(out[0].body, "body");
});

test("split returns null for non-bullet body", () => {
    assert.strictEqual(_splitSquashBullets("subject\n\nbody only"), null);
    assert.strictEqual(_splitSquashBullets("subject only"), null);
    assert.strictEqual(_splitSquashBullets(""), null);
});

test("split recognizes bare type without scope", () => {
    const msg = "feat: cross-cutting (#9)\n\n* fix: nullguard\n\nfoo\n\n* feat: shiny\n\nbar";
    const bullets = _splitSquashBullets(msg);
    assert.strictEqual(bullets.length, 2);
    assert.ok(bullets[0].startsWith("fix: nullguard"));
    assert.ok(bullets[1].startsWith("feat: shiny"));
});

test("split recognizes breaking marker", () => {
    const msg = "feat: rewrites (#9)\n\n* feat(api)!: drop v1 endpoint\n\nbody";
    const bullets = _splitSquashBullets(msg);
    assert.strictEqual(bullets.length, 1);
    assert.ok(bullets[0].startsWith("feat(api)!: drop v1 endpoint"));
});

test("split recognizes multi-scope", () => {
    const msg =
        "feat(firmware,pi): re-anchor (#311)\n\n* feat(firmware,pi): re-anchor V2 release\n\nDetails";
    const bullets = _splitSquashBullets(msg);
    assert.strictEqual(bullets.length, 1);
    assert.ok(bullets[0].startsWith("feat(firmware,pi): re-anchor V2 release"));
});

if (process.exitCode) {
    process.exit(process.exitCode);
}
