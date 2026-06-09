// Server semantic-release config, runs from server/ working directory.
//
// Uses tools/semantic-release-squash-expander instead of the standard
// commit-analyzer / release-notes-generator pair so GitHub squash-merge
// commits get expanded into their per-bullet virtual commits before scope
// matching. See firmware/.releaserc-full.cjs for the rationale.
const path = require('path');
const squashExpander = path.resolve(__dirname, '..', 'tools', 'semantic-release-squash-expander.cjs');
// The expander lives outside server/node_modules; load the wrapped plugins
// here where they're installed and pass them through plugin options.
const wrapped = {
  commitAnalyzer: require('@semantic-release/commit-analyzer'),
  releaseNotesGenerator: require('@semantic-release/release-notes-generator'),
};

module.exports = {
  branches: ['main'],
  tagFormat: 'server/v${version}',
  plugins: [
    [squashExpander, {
      _wrapped: wrapped,
      pathScopes: { 'server/': 'server' },
      // Wrapped commit-analyzer options.
      releaseRules: [
        // Scope globs use micromatch substring patterns so multi-scope commits
        // like fix(digitsd,firmware,server) trigger this release too.
        { scope: '*server*', type: 'feat', release: 'minor' },
        { scope: '*server*', type: 'fix', release: 'patch' },
        { scope: '*server*', type: 'perf', release: 'patch' },
        { scope: '*server*', breaking: true, release: 'major' },
        { scope: '!*server*', release: false },
      ],
      // Shared parser opts (analyzer + notes-generator both honor these).
      parserOpts: {
        noteKeywords: ['BREAKING CHANGE', 'BREAKING CHANGES'],
      },
      // Wrapped release-notes-generator options.
      preset: 'conventionalcommits',
      writerOpts: {
        // Only include commits whose scope is or contains "server".
        // Multi-scope commits like fix(digitsd,firmware,server) are included.
        transform: (commit) => {
          if (!commit.scope || !/(^|,)server(,|$)/.test(commit.scope)) return;
          const typeMap = { feat: 'Features', fix: 'Bug Fixes', perf: 'Performance Improvements' };
          if (!typeMap[commit.type]) return;
          return { ...commit, type: typeMap[commit.type], shortHash: commit.hash && commit.hash.substring(0, 7) };
        },
      },
    }],
    ['@semantic-release/github', { assets: [], successComment: false, failComment: false }],
    ['@semantic-release/exec', {
      publishCmd: 'echo "new_release_published=true" >> $GITHUB_OUTPUT && echo "new_release_version=${nextRelease.version}" >> $GITHUB_OUTPUT && echo "new_release_git_tag=${nextRelease.gitTag}" >> $GITHUB_OUTPUT',
    }],
  ],
};
