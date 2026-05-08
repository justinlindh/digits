// semantic-release plugin that expands GitHub squash-merge commits into
// virtual per-bullet commits before delegating to the standard
// commit-analyzer and release-notes-generator plugins.
//
// GitHub formats a squash merge as one commit whose subject is the PR title
// and whose body lists each original feature-branch commit as a bullet:
//
//   feat(image): V2 carrier board hardware bring-up (#308)
//
//   * fix(image): correct SWD pin number for V2 carrier
//
//   The OpenOCD bitbang config still had the V1 prototype pinout...
//
//   * feat(pi): add MONITOR mode to UART socket
//
//   ...
//
// The default conventional-commits parser only reads the squash commit's
// subject line, which loses every per-commit type/scope. Scope-filtered
// release configs (e.g. "scope: '*firmware*'") then see "no release" for
// cross-component PRs whose squash subject scope matches no single
// component.
//
// This plugin expands such squash commits into virtual commits so both the
// release-decision and changelog-generation steps see the original per-
// commit messages. Non-squash commits pass through unchanged.
//
// Path-based scope inference (pathScopes):
//
// When a commit's scope doesn't match the release rules but its changed
// files fall under a configured path prefix, the plugin injects a synthetic
// commit with the inferred scope. This catches cross-component PRs where
// the squash subject uses one component's scope but the diff touches
// another. Configure via pluginConfig.pathScopes:
//
//   pathScopes: { 'pi/': 'digitsd', 'firmware/': 'firmware' }

// A bullet's first line must look like a conventional-commit subject
// (type, optional scope, optional !, colon, space, then text). We only
// expand a body if at least one bullet matches; otherwise the bullets are
// probably ordinary documentation list items.
const CONVENTIONAL_SUBJECT = /^[a-z]+(?:\([\w*,!-]+\))?!?:\s+\S/;

function splitSquashBullets(message) {
    if (!message) return null;
    const newlineIdx = message.indexOf("\n");
    if (newlineIdx < 0) return null;
    const body = message.slice(newlineIdx + 1);
    // Split on lines that begin with "* " at column 0. The first chunk is
    // everything before the first bullet (typically blank); discard it.
    const parts = body.split(/^\* /m);
    if (parts.length < 2) return null;
    const bullets = parts.slice(1).map((p) => p.replace(/\s+$/, ""));
    if (!bullets.some((b) => CONVENTIONAL_SUBJECT.test(b))) return null;
    return bullets;
}

function expandCommits(commits) {
    return commits.flatMap((commit) => {
        const bullets = splitSquashBullets(commit.message);
        if (!bullets) return [commit];
        return bullets.map((bullet) => {
            const lineBreak = bullet.indexOf("\n");
            const subject = lineBreak < 0 ? bullet : bullet.slice(0, lineBreak);
            const body =
                lineBreak < 0 ? "" : bullet.slice(lineBreak + 1).replace(/^\n+/, "").replace(/\n+$/, "");
            return {
                ...commit,
                header: subject,
                subject: subject,
                body,
                message: body ? `${subject}\n\n${body}` : subject,
            };
        });
    });
}

const SCOPE_RE = /^([a-z]+)\(([^)]+)\)(!?:.*)$/;
const TYPE_RE = /^([a-z]+)(!?:.*)$/;

function inferPathScopes(commits, pathScopes) {
    if (!pathScopes || Object.keys(pathScopes).length === 0) return commits;

    const { execSync } = require("child_process");
    const result = [];

    for (const commit of commits) {
        result.push(commit);

        const subject = commit.subject || commit.header || "";
        const scopeMatch = subject.match(SCOPE_RE);
        const existingScopes = scopeMatch ? scopeMatch[2].split(",") : [];

        let changedFiles;
        try {
            changedFiles = execSync(
                `git diff-tree --no-commit-id --name-only -r ${commit.hash}`,
                { encoding: "utf8" },
            ).trim().split("\n").filter(Boolean);
        } catch {
            continue;
        }

        for (const [prefix, scope] of Object.entries(pathScopes)) {
            if (existingScopes.some((s) => s === scope)) continue;
            if (!changedFiles.some((f) => f.startsWith(prefix))) continue;

            const typeMatch = subject.match(SCOPE_RE) || subject.match(TYPE_RE);
            const type = typeMatch ? typeMatch[1] : "fix";
            const rest = scopeMatch ? scopeMatch[3] : (subject.match(TYPE_RE) || [])[2] || ": path-inferred release";
            const syntheticSubject = `${type}(${scope})${rest}`;

            result.push({
                ...commit,
                header: syntheticSubject,
                subject: syntheticSubject,
                body: `Path-inferred from files matching ${prefix}`,
                message: `${syntheticSubject}\n\nPath-inferred from files matching ${prefix}`,
            });
        }
    }
    return result;
}

// The wrapped plugins are passed in via pluginConfig._wrapped because this
// file lives outside any project's node_modules: a bare require() from
// here walks up only as far as repo-root, which is empty (semantic-release
// installs into firmware/node_modules or pi/node_modules depending on the
// release being run). The .releaserc that loads us is in the right place
// to do the resolution and forwards the loaded modules through.
function unwrap(pluginConfig, key) {
    if (!pluginConfig._wrapped || !pluginConfig._wrapped[key]) {
        throw new Error(
            `semantic-release-squash-expander: pluginConfig._wrapped.${key} is required. ` +
                "Have the calling .releaserc.cjs require('@semantic-release/" +
                (key === "commitAnalyzer" ? "commit-analyzer" : "release-notes-generator") +
                "') and pass it through plugin options.",
        );
    }
    return pluginConfig._wrapped[key];
}

module.exports = {
    async analyzeCommits(pluginConfig, context) {
        const commitAnalyzer = unwrap(pluginConfig, "commitAnalyzer");
        const expanded = expandCommits(context.commits);
        const withPaths = inferPathScopes(expanded, pluginConfig.pathScopes);
        return commitAnalyzer.analyzeCommits(pluginConfig, {
            ...context,
            commits: withPaths,
        });
    },
    async generateNotes(pluginConfig, context) {
        const releaseNotesGenerator = unwrap(pluginConfig, "releaseNotesGenerator");
        const expanded = expandCommits(context.commits);
        const withPaths = inferPathScopes(expanded, pluginConfig.pathScopes);
        return releaseNotesGenerator.generateNotes(pluginConfig, {
            ...context,
            commits: withPaths,
        });
    },
};

// Exposed for unit testing.
module.exports._expandCommits = expandCommits;
module.exports._splitSquashBullets = splitSquashBullets;
module.exports._inferPathScopes = inferPathScopes;
