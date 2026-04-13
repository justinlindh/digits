// Pi (digitsd) semantic-release config — runs from repo root
module.exports = {
  branches: ['main'],
  tagFormat: 'pi/v${version}',
  plugins: [
    ['@semantic-release/commit-analyzer', {
      releaseRules: [
        // Scope globs use micromatch substring patterns so multi-scope commits
        // like fix(digitsd,firmware,server) trigger this release too.
        { scope: '{*pi*,*digitsd*}', type: 'feat', release: 'minor' },
        { scope: '{*pi*,*digitsd*}', type: 'fix', release: 'patch' },
        { scope: '{*pi*,*digitsd*}', type: 'perf', release: 'patch' },
        { scope: '{*pi*,*digitsd*}', breaking: true, release: 'major' },
        { scope: '!{*pi*,*digitsd*}', release: false },
      ],
      parserOpts: {
        noteKeywords: ['BREAKING CHANGE', 'BREAKING CHANGES'],
      },
    }],
    ['@semantic-release/release-notes-generator', {
      preset: 'conventionalcommits',
      parserOpts: {
        noteKeywords: ['BREAKING CHANGE', 'BREAKING CHANGES'],
      },
      writerOpts: {
        // Only include commits whose scope is or contains "pi" or "digitsd".
        // Multi-scope commits like fix(digitsd,firmware,server) are included.
        transform: (commit) => {
          if (!commit.scope || !/(^|,)(pi|digitsd)(,|$)/.test(commit.scope)) return;
          const typeMap = { feat: 'Features', fix: 'Bug Fixes', perf: 'Performance Improvements' };
          if (!typeMap[commit.type]) return;
          return { ...commit, type: typeMap[commit.type], shortHash: commit.hash && commit.hash.substring(0, 7) };
        },
      },
    }],
    ['@semantic-release/exec', {
      prepareCmd: 'bash tools/build-pi.sh ${nextRelease.version}',
    }],
    ['@semantic-release/github', {
      assets: [
        { path: 'artifacts/digitsd-*-aarch64', label: 'digitsd (aarch64 Linux)' },
        { path: 'artifacts/digitsd-*-aarch64.sha256', label: 'digitsd SHA256 checksum' },
      ],
      successComment: false,
      failComment: false,
    }],
  ],
};
