// Firmware semantic-release config — tag-only (no build toolchain available)
module.exports = {
  branches: ['main'],
  tagFormat: 'fw/v${version}',
  plugins: [
    ['@semantic-release/commit-analyzer', {
      releaseRules: [
        // Scope globs use micromatch substring patterns so multi-scope commits
        // like fix(digitsd,firmware,server) trigger this release too.
        { scope: '*firmware*', type: 'feat', release: 'minor' },
        { scope: '*firmware*', type: 'fix', release: 'patch' },
        { scope: '*firmware*', type: 'perf', release: 'patch' },
        { scope: '*firmware*', breaking: true, release: 'major' },
        { scope: '!*firmware*', release: false },
      ],
    }],
    ['@semantic-release/release-notes-generator', {
      preset: 'conventionalcommits',
      parserOpts: {
        noteKeywords: ['BREAKING CHANGE', 'BREAKING CHANGES'],
      },
      writerOpts: {
        // Only include commits whose scope is or contains "firmware".
        // Multi-scope commits like fix(digitsd,firmware,server) are included.
        transform: (commit) => {
          if (!commit.scope || !/(^|,)firmware(,|$)/.test(commit.scope)) return;
          const typeMap = { feat: 'Features', fix: 'Bug Fixes', perf: 'Performance Improvements' };
          if (!typeMap[commit.type]) return;
          return { ...commit, type: typeMap[commit.type], shortHash: commit.hash && commit.hash.substring(0, 7) };
        },
      },
    }],
    ['@semantic-release/github', { assets: [], successComment: false, failComment: false }],
  ],
};
