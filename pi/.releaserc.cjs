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
      parserOpts: {
        noteKeywords: ['BREAKING CHANGE', 'BREAKING CHANGES'],
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
