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

// Wiring tests: mock the wrapped plugins via pluginConfig._wrapped and
// verify that analyzeCommits / generateNotes forward an expanded commit
// list and pass through return values.
const expander = require("./semantic-release-squash-expander.cjs");

test("analyzeCommits forwards expanded commits to wrapped commit-analyzer", async () => {
    let receivedCommits = null;
    const mockAnalyzer = {
        async analyzeCommits(cfg, ctx) {
            receivedCommits = ctx.commits;
            return "minor";
        },
    };
    const ctx = {
        commits: [
            {
                hash: "deadbeef",
                message:
                    "feat(image): squash subject (#1)\n\n* fix(firmware): real fix\n\nbody one\n\n* feat(pi): real feat\n\nbody two",
            },
        ],
    };
    const out = await expander.analyzeCommits({ _wrapped: { commitAnalyzer: mockAnalyzer } }, ctx);
    assert.strictEqual(out, "minor");
    assert.strictEqual(receivedCommits.length, 2);
    assert.strictEqual(receivedCommits[0].subject, "fix(firmware): real fix");
    assert.strictEqual(receivedCommits[1].subject, "feat(pi): real feat");
});

test("generateNotes forwards expanded commits to wrapped notes-generator", async () => {
    let receivedCommits = null;
    const mockNotes = {
        async generateNotes(cfg, ctx) {
            receivedCommits = ctx.commits;
            return "## Release Notes\n";
        },
    };
    const ctx = {
        commits: [
            {
                hash: "abc",
                message: "feat: subj\n\n* fix(server): nullguard\n\nguarded the foo",
            },
        ],
    };
    const out = await expander.generateNotes({ _wrapped: { releaseNotesGenerator: mockNotes } }, ctx);
    assert.strictEqual(out, "## Release Notes\n");
    assert.strictEqual(receivedCommits.length, 1);
    assert.strictEqual(receivedCommits[0].subject, "fix(server): nullguard");
});

test("missing _wrapped throws a helpful error", async () => {
    await assert.rejects(
        () => expander.analyzeCommits({}, { commits: [] }),
        /pluginConfig\._wrapped\.commitAnalyzer is required/,
    );
    await assert.rejects(
        () => expander.generateNotes({}, { commits: [] }),
        /pluginConfig\._wrapped\.releaseNotesGenerator is required/,
    );
});

// ── inferPathScopes tests ───────────────────────────────────────────

const { _inferPathScopes } = require("./semantic-release-squash-expander.cjs");
const { execSync } = require("child_process");

// These tests need a real git repo. If we're not in one, skip them.
let inGitRepo = false;
try {
    execSync("git rev-parse --git-dir", { encoding: "utf8" });
    inGitRepo = true;
} catch {}

if (inGitRepo) {
    test("inferPathScopes injects synthetic commit when files match but scope does not", () => {
        // Use the actual HEAD commit and a pathScopes map that will match
        // something in the repo. We don't know what files HEAD touched, so
        // use a prefix that matches the test file itself.
        const head = execSync("git rev-parse HEAD", { encoding: "utf8" }).trim();
        const files = execSync(`git diff-tree --no-commit-id --name-only -r ${head}`, {
            encoding: "utf8",
        }).trim().split("\n").filter(Boolean);

        if (files.length === 0) {
            console.log("SKIP inferPathScopes inject (HEAD has no files)");
            return;
        }

        // Pick a prefix from the first file
        const prefix = files[0].split("/")[0] + "/";
        const commits = [{
            hash: head,
            subject: "feat(unrelated): something",
            header: "feat(unrelated): something",
            message: "feat(unrelated): something",
        }];

        const result = _inferPathScopes(commits, { [prefix]: "testscope" });
        assert.strictEqual(result.length, 2, "should have original + synthetic");
        assert.strictEqual(result[0].subject, "feat(unrelated): something");
        assert.ok(result[1].subject.includes("testscope"), "synthetic has inferred scope");
        assert.ok(result[1].subject.startsWith("feat("), "preserves original type");
    });

    test("inferPathScopes does not inject when scope already matches", () => {
        const head = execSync("git rev-parse HEAD", { encoding: "utf8" }).trim();
        const commits = [{
            hash: head,
            subject: "feat(digitsd): something",
            header: "feat(digitsd): something",
            message: "feat(digitsd): something",
        }];

        const result = _inferPathScopes(commits, { "pi/": "digitsd" });
        assert.strictEqual(result.length, 1, "no synthetic when scope already present");
    });

    test("inferPathScopes is a no-op when pathScopes is empty or absent", () => {
        const commits = [{ hash: "abc", subject: "feat: x", header: "feat: x", message: "feat: x" }];
        assert.deepStrictEqual(_inferPathScopes(commits, {}), commits);
        assert.deepStrictEqual(_inferPathScopes(commits, undefined), commits);
        assert.deepStrictEqual(_inferPathScopes(commits, null), commits);
    });
}

if (process.exitCode) {
    process.exit(process.exitCode);
}
