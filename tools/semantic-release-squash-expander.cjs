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
        return commitAnalyzer.analyzeCommits(pluginConfig, {
            ...context,
            commits: expandCommits(context.commits),
        });
    },
    async generateNotes(pluginConfig, context) {
        const releaseNotesGenerator = unwrap(pluginConfig, "releaseNotesGenerator");
        return releaseNotesGenerator.generateNotes(pluginConfig, {
            ...context,
            commits: expandCommits(context.commits),
        });
    },
};

// Exposed for unit testing.
module.exports._expandCommits = expandCommits;
module.exports._splitSquashBullets = splitSquashBullets;
